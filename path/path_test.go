package path

import (
	"errors"
	"sort"
	"testing"

	"github.com/dekorlp/fibula/errs"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "a plain relative path is already canonical",
			input: "assets/char/hero_diffuse.png",
			want:  "assets/char/hero_diffuse.png",
		},
		{
			// The macOS case: APFS hands out decomposed names, so the same
			// file would otherwise produce a different manifest entry there
			// than on Windows (E8.3). Both spellings are written as explicit
			// escapes, because they are indistinguishable in an editor.
			name:  "NFD input is composed to NFC",
			input: "assets/ba\u0308ume/gru\u0308n.png",
			want:  "assets/b\u00e4ume/gr\u00fcn.png",
		},
		{
			name:  "already composed input is left alone",
			input: "assets/b\u00e4ume/gr\u00fcn.png",
			want:  "assets/b\u00e4ume/gr\u00fcn.png",
		},
		{
			name:  "case is preserved, not folded",
			input: "Assets/Char/Hero.PNG",
			want:  "Assets/Char/Hero.PNG",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeIsIdempotent matters because normalization happens once on the
// way in and validation happens on every parse afterwards: if the two ever
// disagreed, a manifest we wrote ourselves would fail to load.
func TestNormalizeIsIdempotent(t *testing.T) {
	inputs := []string{
		"assets/ba\u0308ume/gru\u0308n.png",
		"assets/char/hero.fbx",
		"a/b/c/d/e/f.png",
	}

	for _, input := range inputs {
		once, err := Normalize(input)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", input, err)
		}
		twice, err := Normalize(once)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", once, err)
		}
		if once != twice {
			t.Errorf("Normalize is not idempotent: %q then %q", once, twice)
		}
		if err := Validate(once); err != nil {
			t.Errorf("Validate rejects the output of Normalize %q: %v", once, err)
		}
	}
}

func TestRejectedPaths(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"absolute", "/assets/hero.png"},
		{"parent segment", "assets/../../etc/passwd"},
		{"parent segment alone", ".."},
		{"current segment", "./assets/hero.png"},
		{"current segment in the middle", "assets/./hero.png"},
		{"empty segment", "assets//hero.png"},
		{"trailing separator", "assets/hero.png/"},
		{"tab", "assets/hero\tdiffuse.png"},
		{"line feed", "assets/hero\ndiffuse.png"},
		{"carriage return", "assets/hero\rdiffuse.png"},
		{"NUL", "assets/hero\x00.png"},
		{"DEL", "assets/hero\x7f.png"},
		{"backslash separator", `assets\char\hero.png`},
		{"backslash in a name", `assets/hero\diffuse.png`},
		{"invalid UTF-8", "assets/hero\xff.png"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Normalize(tc.input); !errors.Is(err, errs.ErrInvalidPath) {
				t.Errorf("Normalize(%q) err = %v, want errs.ErrInvalidPath", tc.input, err)
			}
			if err := Validate(tc.input); !errors.Is(err, errs.ErrInvalidPath) {
				t.Errorf("Validate(%q) err = %v, want errs.ErrInvalidPath", tc.input, err)
			}
		})
	}
}

// TestValidateRejectsNonCanonicalUnicode is the difference between Validate
// and Normalize: parsing a stored object must reject a decomposed path rather
// than repair it, because the object's ID was computed over the bytes as they
// stand.
func TestValidateRejectsNonCanonicalUnicode(t *testing.T) {
	decomposed := "assets/gru\u0308n.png"

	if err := Validate(decomposed); !errors.Is(err, errs.ErrInvalidPath) {
		t.Errorf("Validate(%q) err = %v, want errs.ErrInvalidPath", decomposed, err)
	}
	if _, err := Normalize(decomposed); err != nil {
		t.Errorf("Normalize(%q) should repair it, got %v", decomposed, err)
	}
}

func TestCheckCollisions(t *testing.T) {
	tests := []struct {
		name    string
		paths   []string
		wantErr bool
	}{
		{
			name:  "distinct paths",
			paths: []string{"assets/hero.png", "assets/villain.png"},
		},
		{
			name:    "differing only in case",
			paths:   []string{"assets/Textur.png", "assets/textur.png"},
			wantErr: true,
		},
		{
			name:    "differing only in case of a directory",
			paths:   []string{"Assets/hero.png", "assets/hero.png"},
			wantErr: true,
		},
		{
			name:    "full case folding, not just ASCII lowering",
			paths:   []string{"assets/STRASSE.png", "assets/strasse.png"},
			wantErr: true,
		},
		{
			name:  "the identical path twice is not a collision",
			paths: []string{"assets/hero.png", "assets/hero.png"},
		},
		{
			name:  "an umlaut is not a collision with its base letter",
			paths: []string{"assets/gr\u00fcn.png", "assets/grun.png"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckCollisions(tc.paths)

			if tc.wantErr && !errors.Is(err, errs.ErrCaseCollision) {
				t.Errorf("err = %v, want errs.ErrCaseCollision", err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestSortIsBytewise pins E8.5. The interesting entries are the ones where a
// locale-aware collation would disagree: it would sort case-insensitively and
// would place "ä" next to "a" instead of after "z".
func TestSortIsBytewise(t *testing.T) {
	paths := []string{
		"assets/\u00e4lter.png",
		"assets/Zebra.png",
		"assets/apple.png",
		"assets/Apple.png",
		"assets/zebra.png",
	}
	want := []string{
		"assets/Apple.png",
		"assets/Zebra.png",
		"assets/apple.png",
		"assets/zebra.png",
		"assets/\u00e4lter.png",
	}

	Sort(paths)

	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("sorted = %q, want %q", paths, want)
		}
	}
}

// TestSortAgreesWithRawByteOrder guards against Less ever growing a clever
// rule: the ordering must stay the one a plain bytewise comparison produces,
// because that is what an independent implementation would do.
func TestSortAgreesWithRawByteOrder(t *testing.T) {
	paths := []string{"b", "A", "\u00e4", "a/b", "ab", "a"}

	fibula := append([]string(nil), paths...)
	Sort(fibula)

	plain := append([]string(nil), paths...)
	sort.Strings(plain)

	for i := range plain {
		if fibula[i] != plain[i] {
			t.Fatalf("Sort = %q, plain bytewise sort = %q", fibula, plain)
		}
	}
}
