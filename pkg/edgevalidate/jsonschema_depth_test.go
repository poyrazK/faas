package edgevalidate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

// nestedAnyOf builds {"anyOf":[{"anyOf":[... {"type":"integer"} ...]},{"type":"string"}]}
// with the given number of anyOf levels.
func nestedAnyOf(levels int) string {
	schema := `{"type":"integer"}`
	for range levels {
		schema = `{"anyOf":[` + schema + `,{"type":"string"}]}`
	}
	return schema
}

// A 1.8 KB schema of 22 nested anyOf levels took 2.6 s to validate {"a":1}
// in the shared gateway. Combinator nesting is bounded at compile, which apid
// runs when a rule is created and the gateway runs again (bug hunt 2026-10-28
// H58).
func TestCompile_RejectsDeeplyNestedCombinators(t *testing.T) {
	if _, err := edgevalidate.Compile([]byte(nestedAnyOf(22)), false); !errors.Is(err, edgevalidate.ErrSchemaTooComplex) {
		t.Fatalf("22 nested anyOf levels: err = %v, want ErrSchemaTooComplex", err)
	}
	if _, err := edgevalidate.Compile([]byte(nestedAnyOf(edgevalidate.MaxSchemaCombinatorDepth)), false); err != nil {
		t.Fatalf("%d nested anyOf levels: %v", edgevalidate.MaxSchemaCombinatorDepth, err)
	}
	// Nesting hidden behind local $refs counts too.
	var defs []string
	for i := range 12 {
		next := `{"type":"integer"}`
		if i < 11 {
			next = `{"$ref":"#/$defs/l` + itoa(i+1) + `"}`
		}
		defs = append(defs, `"l`+itoa(i)+`":{"oneOf":[`+next+`,{"type":"string"}]}`)
	}
	viaRefs := `{"$defs":{` + strings.Join(defs, ",") + `},"$ref":"#/$defs/l0"}`
	if _, err := edgevalidate.Compile([]byte(viaRefs), false); !errors.Is(err, edgevalidate.ErrSchemaTooComplex) {
		t.Fatalf("12 levels through $refs: err = %v, want ErrSchemaTooComplex", err)
	}
	// A recursive schema (a tree) stays acceptable.
	tree := `{"$defs":{"node":{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/node"}}},"anyOf":[{"required":["children"]},{"required":["leaf"]}]}},"$ref":"#/$defs/node"}`
	if _, err := edgevalidate.Compile([]byte(tree), false); err != nil {
		t.Fatalf("recursive tree schema: %v", err)
	}
}

func itoa(i int) string {
	return string(rune('a' + i))
}
