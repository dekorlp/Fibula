package object

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dekorlp/fibula/hash"
)

// unmarshalers dispatches a golden vector to the parser for its type.
var unmarshalers = map[string]func([]byte) (interface{ Marshal() ([]byte, error) }, error){
	"file":                      func(b []byte) (interface{ Marshal() ([]byte, error) }, error) { return UnmarshalFile(b) },
	"file-empty":                func(b []byte) (interface{ Marshal() ([]byte, error) }, error) { return UnmarshalFile(b) },
	"manifest":                  func(b []byte) (interface{ Marshal() ([]byte, error) }, error) { return UnmarshalManifest(b) },
	"version-deliberate":        func(b []byte) (interface{ Marshal() ([]byte, error) }, error) { return UnmarshalVersion(b) },
	"version-snapshot":          func(b []byte) (interface{ Marshal() ([]byte, error) }, error) { return UnmarshalVersion(b) },
	"version-multiline-message": func(b []byte) (interface{ Marshal() ([]byte, error) }, error) { return UnmarshalVersion(b) },
	"graph":                     func(b []byte) (interface{ Marshal() ([]byte, error) }, error) { return UnmarshalGraph(b) },
	"signature":                 func(b []byte) (interface{ Marshal() ([]byte, error) }, error) { return UnmarshalSignature(b) },
}

// TestRoundTripIsByteIdentical is the property the whole format rests on:
// parsing and re-serializing must reproduce the input exactly. If it did not,
// an object could change its ID by passing through a client.
func TestRoundTripIsByteIdentical(t *testing.T) {
	for name, unmarshal := range unmarshalers {
		t.Run(name, func(t *testing.T) {
			golden := filepath.Join("testdata", name+".golden")
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden vector: %v", err)
			}

			obj, err := unmarshal(want)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got, err := obj.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			if string(got) != string(want) {
				t.Errorf("round trip changed the bytes\n got: %q\nwant: %q", got, want)
			}
		})
	}
}

// TestUnmarshalRecoversTheValues checks that the round trip is not vacuously
// true - the parser has to actually produce the fields, not merely echo bytes.
func TestUnmarshalRecoversTheValues(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		data := mustRead(t, "file")

		got, err := UnmarshalFile(data)
		if err != nil {
			t.Fatalf("UnmarshalFile: %v", err)
		}
		if got.Size != 3145728 {
			t.Errorf("Size = %d, want 3145728", got.Size)
		}
		if len(got.Chunks) != 2 {
			t.Fatalf("got %d chunks, want 2", len(got.Chunks))
		}
		if got.Chunks[0].ID != hash.Chunk([]byte("chunk a")) {
			t.Errorf("first chunk id = %s", got.Chunks[0].ID)
		}
		if got.Chunks[1].Length != 1048576 {
			t.Errorf("second chunk length = %d, want 1048576", got.Chunks[1].Length)
		}
	})

	t.Run("version with a multi-line message", func(t *testing.T) {
		data := mustRead(t, "version-multiline-message")

		got, err := UnmarshalVersion(data)
		if err != nil {
			t.Fatalf("UnmarshalVersion: %v", err)
		}
		want := "Reworked hero rig\n\nUVs repacked, normals flipped on the left glove."
		if got.Message != want {
			t.Errorf("Message = %q, want %q", got.Message, want)
		}
		if !got.Expiry.IsZero() {
			t.Errorf("Expiry = %v, want the zero value on a deliberate version", got.Expiry)
		}
		if len(got.Parents) != 0 {
			t.Errorf("Parents = %v, want none", got.Parents)
		}
	})

	t.Run("auto snapshot", func(t *testing.T) {
		data := mustRead(t, "version-snapshot")

		got, err := UnmarshalVersion(data)
		if err != nil {
			t.Fatalf("UnmarshalVersion: %v", err)
		}
		if got.Message != "" {
			t.Errorf("Message = %q, want empty on an auto snapshot", got.Message)
		}
		want := time.Date(2026, 8, 3, 14, 20, 31, 0, time.UTC)
		if !got.Expiry.Equal(want) {
			t.Errorf("Expiry = %v, want %v", got.Expiry, want)
		}
		if !got.Graph.IsZero() {
			t.Errorf("Graph = %s, want unset", got.Graph)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		got, err := UnmarshalFile(mustRead(t, "file-empty"))
		if err != nil {
			t.Fatalf("UnmarshalFile: %v", err)
		}
		if got.Size != 0 || len(got.Chunks) != 0 {
			t.Errorf("got size %d with %d chunks, want an empty file", got.Size, len(got.Chunks))
		}
	})
}

// TestIDsAreOverTheCanonicalSerialization guards E9, E11 and E21: the ID of a
// manifest, version or graph is the hash of its canonical bytes, so computing
// it must be identical to marshaling and hashing by hand.
func TestIDsAreOverTheCanonicalSerialization(t *testing.T) {
	manifest, err := UnmarshalManifest(mustRead(t, "manifest"))
	if err != nil {
		t.Fatalf("UnmarshalManifest: %v", err)
	}

	got, err := manifest.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if want := hash.Manifest(mustRead(t, "manifest")); got != want {
		t.Errorf("ID = %s, want %s", got, want)
	}
}

// TestTimestampsAreTruncatedToSeconds documents the one lossy step in the
// codec: RFC 3339 with second resolution has no room for sub-second precision
// (E33), so Marshal truncates rather than failing.
func TestTimestampsAreTruncatedToSeconds(t *testing.T) {
	v := Version{
		Manifest: hash.Manifest([]byte("m")),
		Author:   "dennis",
		Time:     time.Date(2026, 8, 2, 14, 20, 31, 999_999_999, time.UTC),
		Message:  "sub-second precision is dropped",
	}

	data, err := v.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalVersion(data)
	if err != nil {
		t.Fatalf("UnmarshalVersion: %v", err)
	}

	want := time.Date(2026, 8, 2, 14, 20, 31, 0, time.UTC)
	if !got.Time.Equal(want) {
		t.Errorf("Time = %v, want %v", got.Time, want)
	}
}

// TestNonUTCTimestampsAreNormalized checks that a timestamp in another zone
// does not produce a second spelling of the same instant.
func TestNonUTCTimestampsAreNormalized(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*60*60)
	v := Version{
		Manifest: hash.Manifest([]byte("m")),
		Author:   "dennis",
		Time:     time.Date(2026, 8, 2, 16, 20, 31, 0, berlin),
		Message:  "written in a local zone",
	}

	data, err := v.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if want := "time\t2026-08-02T14:20:31Z\n"; !strings.Contains(string(data), want) {
		t.Errorf("serialization %q does not contain %q", data, want)
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name+".golden"))
	if err != nil {
		t.Fatalf("read golden vector %s: %v", name, err)
	}
	return data
}
