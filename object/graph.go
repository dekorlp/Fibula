package object

import (
	"fmt"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/path"
)

// Edge is one dependency between two assets, valid in the context of the
// manifest version the graph belongs to (E20).
//
// Path-based, not FileID-based, on purpose: when a texture is replaced in
// content the level loads the new version and nothing breaks, so binding edges
// to content would invalidate every edge on every save although nothing about
// the relationship changed. What actually breaks is a deleted or renamed path
// - and the reference inside a DCC file is a path.
type Edge struct {
	Source string
	Target string
	Type   string
}

// Extractor records that a given extractor ran over a given set of files
// (E23).
//
// This is not bookkeeping. Without it, "no known edges" would be
// indistinguishable from "no dependencies", and delete protection would turn
// into a false safety promise - "no references found, so it is safe to delete"
// - which is exactly the data loss the feature exists to prevent. With it the
// tool can say honestly: no known references, but nothing here parses .uasset.
type Extractor struct {
	Name    string
	Version string

	// Scope names the files the extractor covered, as a suffix or pattern.
	// Its exact shape follows the extractor interface, which is still open
	// (refinements/index.md) - this field is provisional.
	Scope string
}

// Graph is the advisory dependency graph between assets (E19, E21, E22).
//
// Advisory means it blocks nothing: an outdated extractor must never stop
// anyone from working, because a feature that stops people from working gets
// switched off and is then dead. It informs - delete protection, broken
// reference checks, impact analysis and partial checkout.
//
// The core never parses asset formats. Fibula stores what an extractor
// delivers; the extractors live outside the core, because a core that contains
// .blend parsing breaks with every Blender release and is no longer a stable
// foundation (E19).
type Graph struct {
	// Extractors are sorted by name, version, scope and are unique.
	Extractors []Extractor

	// Edges are sorted by source, target, type and are unique.
	Edges []Edge
}

// Marshal renders the graph in its canonical form: the extractor lines first,
// then the edges, each group sorted (E33).
func (g Graph) Marshal() ([]byte, error) {
	if err := g.validate(); err != nil {
		return nil, err
	}

	e := newEncoder(format.HeaderGraph)
	for _, x := range g.Extractors {
		e.line("extractor", x.Name, x.Version, x.Scope)
	}
	for _, edge := range g.Edges {
		e.line("edge", edge.Source, edge.Target, edge.Type)
	}
	return e.bytes(), nil
}

// UnmarshalGraph parses a canonical graph object.
func UnmarshalGraph(data []byte) (Graph, error) {
	d, err := newDecoder(data, format.HeaderGraph)
	if err != nil {
		return Graph{}, err
	}

	var g Graph
	for d.peek() == "extractor" {
		fields, err := d.keyed("extractor", 3)
		if err != nil {
			return Graph{}, err
		}
		g.Extractors = append(g.Extractors, Extractor{Name: fields[0], Version: fields[1], Scope: fields[2]})
	}
	for !d.done() {
		fields, err := d.keyed("edge", 3)
		if err != nil {
			return Graph{}, err
		}
		g.Edges = append(g.Edges, Edge{Source: fields[0], Target: fields[1], Type: fields[2]})
	}

	return g, g.validate()
}

// ID returns the GraphID, BLAKE3 over the canonical serialization (E21).
func (g Graph) ID() (hash.GraphID, error) {
	data, err := g.Marshal()
	if err != nil {
		return hash.GraphID{}, err
	}
	return hash.Graph(data), nil
}

func (g Graph) validate() error {
	if err := g.validateExtractors(); err != nil {
		return err
	}
	return g.validateEdges()
}

func (g Graph) validateExtractors() error {
	var previous string
	for i, x := range g.Extractors {
		fields := [][2]string{
			{"extractor name", x.Name},
			{"extractor version", x.Version},
			{"extractor scope", x.Scope},
		}
		for _, f := range fields {
			if err := checkText(f[0], f[1]); err != nil {
				return err
			}
		}

		key := x.Name + format.FieldSeparator + x.Version + format.FieldSeparator + x.Scope
		if i > 0 && key <= previous {
			return fmt.Errorf("%w: extractor %q follows %q, extractors must be sorted and unique",
				errs.ErrInconsistentObject, key, previous)
		}
		previous = key
	}
	return nil
}

func (g Graph) validateEdges() error {
	var previous string
	for _, e := range g.Edges {
		if err := path.Validate(e.Source); err != nil {
			return fmt.Errorf("edge source: %w", err)
		}
		if err := path.Validate(e.Target); err != nil {
			return fmt.Errorf("edge target: %w", err)
		}
		if err := checkText("edge type", e.Type); err != nil {
			return err
		}

		key := e.Source + format.FieldSeparator + e.Target + format.FieldSeparator + e.Type
		if previous != "" && key <= previous {
			return fmt.Errorf("%w: edge %q follows %q, edges must be sorted and unique",
				errs.ErrInconsistentObject, key, previous)
		}
		previous = key
	}
	return nil
}
