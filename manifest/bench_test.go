package manifest

import (
	"fmt"
	"testing"

	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/object"
)

func benchEntries(n int) []object.Entry {
	entries := make([]object.Entry, n)
	for i := range entries {
		entries[i] = object.Entry{
			Path: fmt.Sprintf("assets/pack_%03d/asset_%05d.png", i/100, i),
			File: hash.File([]byte(fmt.Sprintf("asset-%d", i))),
			Size: int64(i) + 1,
		}
	}
	return entries
}

func BenchmarkBuild(b *testing.B) {
	entries := benchEntries(50_000)

	b.ResetTimer()
	for b.Loop() {
		var builder Builder
		for _, e := range entries {
			if err := builder.Add(e.Path, e.File, e.Size); err != nil {
				b.Fatal(err)
			}
		}
		if _, err := builder.Build(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalManifest(b *testing.B) {
	data, err := object.Manifest{Entries: benchEntries(50_000)}.Marshal()
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))

	b.ResetTimer()
	for b.Loop() {
		if _, err := object.UnmarshalManifest(data); err != nil {
			b.Fatal(err)
		}
	}
}
