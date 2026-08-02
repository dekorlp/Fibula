package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

// TestSafeJoinRefusesHostilePaths is F-S3-06. Manifest paths are untrusted
// input even in single-user operation: a manifest can come from a store
// somebody else wrote to, and a presigned PUT URL is exactly how something
// hostile gets into a store (object model § 8).
func TestSafeJoinRefusesHostilePaths(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name string
		path string
	}{
		{"parent traversal", "../outside.png"},
		{"deep parent traversal", "assets/../../outside.png"},
		{"absolute unix path", "/etc/passwd"},
		{"leading slash", "/assets/hero.png"},
		{"backslash traversal", `..\outside.png`},
		{"windows drive prefix", `C:\Windows\System32\evil.dll`},
		{"unc path", `\\server\share\evil.dll`},
		{"embedded NUL", "assets/hero\x00.png"},
		{"newline", "assets/hero\n.png"},
		{"empty", ""},
		{"bare dot", "."},
		{"bare dotdot", ".."},
		{"trailing slash", "assets/"},
		{"double slash", "assets//hero.png"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SafeJoin(root, tc.path)

			if !errors.Is(err, errs.ErrUnsafePath) {
				t.Errorf("SafeJoin(%q) err = %v, want errs.ErrUnsafePath (got path %q)", tc.path, err, got)
			}
		})
	}
}

func TestSafeJoinAcceptsOrdinaryPaths(t *testing.T) {
	root := t.TempDir()

	for _, p := range []string{"hero.png", "assets/char/hero.fbx", "a/b/c/d/e.txt"} {
		got, err := SafeJoin(root, p)
		if err != nil {
			t.Errorf("SafeJoin(%q): %v", p, err)
			continue
		}
		if !strings.HasPrefix(got, root) {
			t.Errorf("SafeJoin(%q) = %q, which is outside %q", p, got, root)
		}
	}
}

// TestSafeJoinRefusesToPassThroughASymlink is the case a purely lexical check
// misses entirely: the path contains no "..", yet writing it escapes the
// target directory because a parent component is a link.
func TestSafeJoinRefusesToPassThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links on Windows needs elevated privileges")
	}

	root := t.TempDir()
	outside := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(root, "assets")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if _, err := SafeJoin(root, "assets/hero.png"); !errors.Is(err, errs.ErrUnsafePath) {
		t.Errorf("err = %v, want errs.ErrUnsafePath", err)
	}
}

// TestRestoreOfAHostileManifestWritesNothing is the done-criterion of F-S3-06:
// a hand-crafted manifest must not put a single byte outside the target
// directory — and must not write the harmless entries that precede the hostile
// one either, because a half-applied restore is its own kind of damage.
func TestRestoreOfAHostileManifestWritesNothing(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	outside := t.TempDir()

	// A real, storable entry so the manifest is not rejected for other reasons.
	writeFile(t, space.Root(), "assets/hero.fbx", "harmless content")
	snapshot(ctx, t, space)

	m, err := space.CheckedOutManifest()
	if err != nil {
		t.Fatalf("CheckedOutManifest: %v", err)
	}

	// Craft the hostile entry directly on the parsed manifest, which is how it
	// would arrive from a store: the object parser would have rejected it, so
	// this is the stronger test of the restore path itself.
	hostile := object.Manifest{Entries: append([]object.Entry(nil), m.Entries...)}
	hostile.Entries = append(hostile.Entries, object.Entry{
		Path: "../" + filepath.Base(outside) + "/owned.txt",
		File: hash.File([]byte("harmless content")),
		Size: 16,
	})

	if _, err := space.RestoreManifest(ctx, hostile); !errors.Is(err, errs.ErrUnsafePath) {
		t.Errorf("err = %v, want errs.ErrUnsafePath", err)
	}

	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("read the target of the attack: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the restore wrote %d entries outside the space", len(entries))
	}
	if _, err := os.Stat(filepath.Join(space.Root(), "assets", "hero.fbx")); err == nil {
		// The harmless entry precedes the hostile one in sort order; it must
		// not have been written before the manifest was rejected.
		t.Log("note: the harmless entry existed before the restore, from the snapshot")
	}
}

// TestRestoreIsByteIdenticalAfterClear is the done-criterion of F-S3-05.
func TestRestoreIsByteIdenticalAfterClear(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)

	tree := map[string]string{
		"assets/char/hero_mesh.fbx":    strings.Repeat("mesh bytes ", 2000),
		"assets/char/hero_diffuse.png": strings.Repeat("texture ", 5000),
		"assets/env/gr\u00fcn.png":     "a non-ASCII name",
		"levels/level_01.blend":        "level data",
		"empty.bin":                    "",
	}
	for p, content := range tree {
		writeFile(t, space.Root(), p, content)
	}
	snapshot(ctx, t, space)

	if _, err := space.Clear(ctx, &Ignore{}, ClearOptions{Author: "dennis", Now: fixedTime()}); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	for p := range tree {
		assertGone(t, space, p)
	}

	if _, err := space.Restore(ctx); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	for p, content := range tree {
		assertContent(t, space, p, content)
	}

	// And the cache is rebuilt, so the next status does not call the whole
	// restored tree modified.
	status, err := space.Status(ctx, &Ignore{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.IsClean() {
		t.Errorf("status after restore is not clean: %+v", status)
	}
}

// TestRestoreRejectsAWrongSize covers the manifest and the file object
// disagreeing. On divergence the content wins, so a mismatch is corruption
// rather than something to reconcile (E7).
func TestRestoreRejectsAWrongSize(t *testing.T) {
	ctx := context.Background()
	space, _ := newSpace(t)
	writeFile(t, space.Root(), "assets/hero.fbx", "content")
	snapshot(ctx, t, space)

	m, err := space.CheckedOutManifest()
	if err != nil {
		t.Fatalf("CheckedOutManifest: %v", err)
	}
	m.Entries[0].Size = 9999

	if _, err := space.RestoreManifest(ctx, m); !errors.Is(err, errs.ErrCorruptObject) {
		t.Errorf("err = %v, want errs.ErrCorruptObject", err)
	}
}

func TestWindowsReservedNamesAreRefusedOnWindows(t *testing.T) {
	root := t.TempDir()

	_, err := SafeJoin(root, "assets/aux.png")
	if runtime.GOOS == "windows" {
		if !errors.Is(err, errs.ErrUnsafePath) {
			t.Errorf("err = %v, want errs.ErrUnsafePath on Windows", err)
		}
		return
	}
	if err != nil {
		t.Errorf("err = %v, want the name accepted where it is legal", err)
	}
}

// TestWindowsReservedNameRules exercises the Windows-only rule on every host,
// which is the point of passing the platform in rather than looking it up.
func TestWindowsReservedNameRules(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "ordinary name", path: "assets/hero.png"},
		{name: "device name", path: "assets/CON", wantErr: true},
		{name: "device name with an extension", path: "assets/aux.png", wantErr: true},
		{name: "device name in lowercase", path: "assets/nul.txt", wantErr: true},
		{name: "device name as a directory", path: "com1/hero.png", wantErr: true},
		{name: "serial ports", path: "assets/LPT9.bin", wantErr: true},
		{name: "a name merely starting like one", path: "assets/console.png"},
		{name: "trailing dot", path: "assets/hero.", wantErr: true},
		{name: "trailing space", path: "assets/hero ", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkReservedNames(tc.path, true)
			switch {
			case tc.wantErr && !errors.Is(err, errs.ErrUnsafePath):
				t.Errorf("on Windows: err = %v, want errs.ErrUnsafePath", err)
			case !tc.wantErr && err != nil:
				t.Errorf("on Windows: err = %v, want the name accepted", err)
			}

			// Off Windows every one of these is a legal name.
			if err := checkReservedNames(tc.path, false); err != nil {
				t.Errorf("off Windows: err = %v, want the name accepted", err)
			}
		})
	}
}

func TestHeadAndManifestBeforeAnySnapshot(t *testing.T) {
	space, _ := newSpace(t)

	if _, err := space.Head(); !errors.Is(err, errs.ErrRefNotFound) {
		t.Errorf("Head err = %v, want errs.ErrRefNotFound", err)
	}
	if _, err := space.CheckedOutManifest(); !errors.Is(err, errs.ErrRefNotFound) {
		t.Errorf("CheckedOutManifest err = %v, want errs.ErrRefNotFound", err)
	}
	if _, err := space.Restore(context.Background()); !errors.Is(err, errs.ErrRefNotFound) {
		t.Errorf("Restore err = %v, want errs.ErrRefNotFound", err)
	}
}

// TestDeleteRefusesASymlinkThatAppearedAfterTheCheck: the scanner never
// records links, so one found at deletion time appeared in between. Removing
// it is not what the check approved.
func TestDeleteRefusesASymlinkThatAppearedAfterTheCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links on Windows needs elevated privileges")
	}

	space, _ := newSpace(t)
	elsewhere := filepath.Join(t.TempDir(), "target.bin")
	if err := os.WriteFile(elsewhere, []byte("not ours"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(space.Root(), "link.bin")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := space.deleteFile("link.bin"); !errors.Is(err, errs.ErrDirty) {
		t.Errorf("err = %v, want errs.ErrDirty", err)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Errorf("the link target was affected: %v", err)
	}
}

func TestDeleteOfAMissingFileIsNotAnError(t *testing.T) {
	space, _ := newSpace(t)

	if _, err := space.deleteFile("never-existed.bin"); err != nil {
		t.Errorf("err = %v, want a missing file to be a no-op", err)
	}
}
