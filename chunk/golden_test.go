package chunk

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/tuning"
)

// updateGolden exists here and deliberately not in package object. A boundary
// vector that stops matching is an unintended dedup regression, not a format
// break (E40): the right response is to find the cause and then, if the change
// was wanted, reset the fixture on purpose and say so in the review document.
// A serialization vector never gets that option.
var updateGolden = flag.Bool("update-boundaries", false,
	"reset the chunk boundary vectors after a deliberate parameter change (E40)")

// TestBuzhashTableIsStable pins the derived table (E39). The vector is the
// hash of the table rather than the table itself - 256 random words are not
// something a reviewer can check by reading them, but one line of hex is.
func TestBuzhashTableIsStable(t *testing.T) {
	raw := make([]byte, 0, tableSize*8)
	for _, v := range table {
		raw = binary.LittleEndian.AppendUint64(raw, v)
	}

	got := hash.Chunk(raw).String()
	compareGolden(t, "buzhash-table", got+"\n")

	// The table must also be what the specified derivation produces, so that
	// the vector cannot drift together with a changed derivation.
	want := hash.DeriveBytes(tuning.BuzhashTableContext, tableSize*8)
	if !bytes.Equal(raw, want) {
		t.Error("the table is not the BLAKE3 output the refinement specifies (E39)")
	}
}

// TestBoundaryVectors records where the chunker cuts a fixed input. If this
// fails after a parameter change it is doing its job; if it fails without one,
// something changed the boundaries by accident.
func TestBoundaryVectors(t *testing.T) {
	tests := []struct {
		name   string
		params Params
		data   []byte
	}{
		{"default-params", DefaultParams(), deterministicBytes(101, 12*1024*1024)},
		{"manifest-params", ManifestParams(), deterministicBytes(102, 512*1024)},
		{"small-params", smallParams(), deterministicBytes(103, 200_000)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			for _, c := range split(t, tc.data, tc.params) {
				fmt.Fprintf(&b, "%d\t%s\n", len(c), hash.Chunk(c))
			}
			compareGolden(t, "boundaries-"+tc.name, b.String())
		})
	}
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()

	golden := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatalf("write golden vector: %v", err)
		}
		t.Logf("reset %s deliberately - record this in the review document (E40)", golden)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden vector: %v", err)
	}
	if got != string(want) {
		t.Errorf("chunk boundaries changed - a dedup regression unless it was deliberate (E40).\n"+
			"Find the cause first; only then re-run with -update-boundaries.\n"+
			"got %d lines, want %d lines", strings.Count(got, "\n"), bytes.Count(want, []byte("\n")))
	}
}
