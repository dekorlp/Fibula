package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/store"
	"github.com/dekorlp/fibula/store/fs"
)

// Layout of the local state (E16).
const (
	// SpaceDir holds everything Fibula knows about a working directory. It is
	// what "clear space" keeps: the space retains its identity and history,
	// and restoring becomes a pure download.
	SpaceDir = ".fibula"

	configFile   = "config"
	headFile     = "head"
	cacheFile    = "cache"
	manifestFile = "manifest"
	filesDir     = "files"

	statePerm = 0o755
	filePerm  = 0o644
)

// Config is the per-space configuration (E16).
type Config struct {
	// Store is where objects live. For the filesystem backend it is a
	// directory; a second disk or a NAS share is a fully valid store.
	Store string

	// Budget is the storage budget in bytes that triggers a snapshot and a
	// suggestion to clear (E15). Zero means no budget.
	//
	// It never triggers a deletion. The space manager suggests, the user
	// decides - deleting assets because a number was exceeded is exactly the
	// behaviour that would make people distrust the feature.
	Budget int64
}

// Head is where the space currently stands (E16).
type Head struct {
	Ref     store.RefName
	Version hash.VersionID

	// Base is the deliberate version this working directory descends from.
	//
	// It differs from Version whenever an auto snapshot has been recorded
	// since, because a snapshot moves what is in the directory without moving
	// what the next commit builds on - snapshots carry no parent at all (E12).
	// Keeping the two apart is what lets a commit tell "I am up to date" from
	// "someone else moved the ref", which is the check that stops it silently
	// discarding their work (E13.3, TP-005 EC-401).
	//
	// Zero means the space has never committed.
	Base hash.VersionID
}

// Space is a working copy: a directory, its local state and a storage budget
// (E15). It is not a separate concept from the working directory - it is the
// working directory, plus what Fibula remembers about it.
type Space struct {
	root   string
	config Config

	objects store.ObjectStore
	refs    store.RefStore

	// local holds file objects for the working tree, so that the chunk list of
	// an unchanged file is known without re-chunking it (E16). It is a chunk
	// list cache, not a chunk cache: a real chunk cache would double the disk
	// requirement, which for binary assets is unacceptable.
	local store.ObjectStore
}

// Init creates a space in root, pointing at the given store.
func Init(root, storePath string) (*Space, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve space root: %w", err)
	}
	if storePath == "" {
		return nil, fmt.Errorf("%w: no store configured", errs.ErrNotASpace)
	}

	dir := filepath.Join(abs, SpaceDir)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%w: %s already exists", errs.ErrNotASpace, dir)
	}
	if err := os.MkdirAll(dir, statePerm); err != nil {
		return nil, fmt.Errorf("create space directory: %w", err)
	}

	absStore, err := filepath.Abs(storePath)
	if err != nil {
		return nil, fmt.Errorf("resolve store path: %w", err)
	}
	if err := writeConfig(dir, Config{Store: absStore}); err != nil {
		return nil, err
	}
	if _, err := fs.Create(absStore); err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}
	if _, err := fs.CreateRefs(absStore); err != nil {
		return nil, fmt.Errorf("create refs: %w", err)
	}
	if _, err := fs.Create(filepath.Join(abs, SpaceDir, filesDir)); err != nil {
		return nil, fmt.Errorf("create local file objects: %w", err)
	}
	return Open(abs)
}

// Open loads the space containing dir, searching upwards the way a user
// expects when they run a command from somewhere inside their project.
func Open(dir string) (*Space, error) {
	root, err := findRoot(dir)
	if err != nil {
		return nil, err
	}

	config, err := readConfig(filepath.Join(root, SpaceDir))
	if err != nil {
		return nil, err
	}

	objects, err := fs.Open(config.Store)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	refs, err := fs.OpenRefs(config.Store)
	if err != nil {
		return nil, fmt.Errorf("open refs: %w", err)
	}
	local, err := fs.Open(filepath.Join(root, SpaceDir, filesDir))
	if err != nil {
		return nil, fmt.Errorf("open local file objects: %w", err)
	}

	return &Space{root: root, config: config, objects: objects, refs: refs, local: local}, nil
}

func findRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve directory: %w", err)
	}

	for {
		if info, err := os.Stat(filepath.Join(abs, SpaceDir)); err == nil && info.IsDir() {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("%w: no %s found in %s or any parent", errs.ErrNotASpace, SpaceDir, dir)
		}
		abs = parent
	}
}

// Root is the working directory the space manages.
func (s *Space) Root() string { return s.root }

// Config returns the space configuration.
func (s *Space) Config() Config { return s.config }

// Objects is the store the space reads from and writes to.
func (s *Space) Objects() store.ObjectStore { return s.objects }

// Refs is the ref store of the configured store.
func (s *Space) Refs() store.RefStore { return s.refs }

func (s *Space) stateDir() string { return filepath.Join(s.root, SpaceDir) }

// Head reads where the space stands. A space that has never snapshotted has no
// head yet, which is reported as ErrRefNotFound.
func (s *Space) Head() (Head, error) {
	data, err := os.ReadFile(filepath.Join(s.stateDir(), headFile))
	if errors.Is(err, os.ErrNotExist) {
		return Head{}, fmt.Errorf("%w: the space has no head yet", errs.ErrRefNotFound)
	}
	if err != nil {
		return Head{}, fmt.Errorf("read head: %w", err)
	}

	fields, err := parseFields(string(data), "ref", "version")
	if err != nil {
		return Head{}, fmt.Errorf("head: %w", err)
	}
	ref, err := store.LocalRef(fields["ref"])
	if err != nil {
		return Head{}, fmt.Errorf("head: %w", err)
	}
	version, err := hash.ParseVersionID(fields["version"])
	if err != nil {
		return Head{}, fmt.Errorf("head: %w", err)
	}

	head := Head{Ref: ref, Version: version, Base: version}
	// base is optional: a space written before it existed falls back to its
	// recorded version, which is what the old code effectively used. That is
	// right whenever the last operation was a commit and too permissive when
	// it was a snapshot - i.e. no worse than before, and self-correcting on
	// the next commit.
	if raw, ok := fields["base"]; ok {
		base, err := hash.ParseVersionID(raw)
		if err != nil {
			return Head{}, fmt.Errorf("head: %w", err)
		}
		head.Base = base
	}
	return head, nil
}

// SetHead records where the space stands.
func (s *Space) SetHead(h Head) error {
	body := "ref\t" + h.Ref.Name() +
		"\nversion\t" + h.Version.String() +
		"\nbase\t" + h.Base.String() + "\n"
	return writeFileAtomic(filepath.Join(s.stateDir(), headFile), []byte(body))
}

func writeConfig(dir string, c Config) error {
	body := "store\t" + c.Store + "\nbudget\t" + strconv.FormatInt(c.Budget, 10) + "\n"
	return writeFileAtomic(filepath.Join(dir, configFile), []byte(body))
}

func readConfig(dir string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(dir, configFile)) //nolint:gosec // the fixed config of a space
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("%w: %s has no %s", errs.ErrNotASpace, dir, configFile)
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	fields, err := parseFields(string(data), "store", "budget")
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	budget, err := strconv.ParseInt(fields["budget"], 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("config: budget: %w", err)
	}
	return Config{Store: fields["store"], Budget: budget}, nil
}

// parseFields reads the tab-separated key/value form used for local state.
//
// Local state is deliberately not a format object: it is never shared, never
// content-addressed and never hashed, so it carries no framing line and may
// change shape freely. It uses the same tab-and-LF conventions anyway, because
// two serialization styles in one codebase is one too many.
func parseFields(body string, required ...string) (map[string]string, error) {
	fields := make(map[string]string, len(required))

	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("malformed line %q", line)
		}
		fields[key] = value
	}

	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("missing field %q", key)
		}
	}
	return fields, nil
}

// writeFileAtomic replaces a local state file through a temporary file and a
// rename, so that an interrupted write cannot leave the space with a
// half-written head or cache.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, statePerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	f, err := os.CreateTemp(dir, "state-*")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	temp := f.Name()
	//nolint:errcheck // a no-op once the rename succeeded
	defer os.Remove(temp)

	if _, err := f.Write(data); err != nil {
		_ = f.Close() //nolint:errcheck // the write already failed and is being reported
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() //nolint:errcheck // the sync already failed and is being reported
		return fmt.Errorf("flush %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Chmod(temp, filePerm); err != nil {
		return fmt.Errorf("set permissions on %s: %w", path, err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("commit %s: %w", path, err)
	}
	return nil
}
