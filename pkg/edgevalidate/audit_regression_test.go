package edgevalidate_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/edgevalidate"
	"testing"
)

func TestAuditValidationPreservesIntegerPrecision(t *testing.T) {
	for _, tc := range []struct {
		name, schema, body string
		wantReject         bool
	}{
		{"body rounds down", `{"type":"integer","maximum":9007199254740992}`, `9007199254740993`, true},
		{"fraction rounds to integer", `{"type":"integer"}`, `1.0000000000000001`, true},
		{"fraction crosses maximum", `{"type":"number","maximum":1}`, `1.0000000000000001`, true},
		{"distinct integer const", `{"type":"integer","const":9007199254740992}`, `9007199254740993`, true},
		{"schema rounds down", `{"type":"integer","const":9007199254740993}`, `9007199254740992`, true},
		{"same large const control", `{"type":"integer","const":9007199254740993}`, `9007199254740993`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := edgevalidate.Compile([]byte(tc.schema), false)
			if err != nil {
				t.Fatal(err)
			}
			fe, err := compiled.Validate([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if (fe != nil) != tc.wantReject {
				t.Errorf("numeric precision changes acceptance: schema=%s body=%s fieldError=%+v", tc.schema, tc.body, fe)
			}
		})
	}
}

// A short, linear chain of nine schemas, only one over the declared bound.
// This never constructs a wide/expensive payload or contacts a service.
func auditCombinatorRefChain(keyword string, anchor bool) []byte {
	defs := map[string]any{}
	for i := 0; i < edgevalidate.MaxSchemaCombinatorDepth+1; i++ {
		name := fmt.Sprintf("level%d", i)
		var next any = map[string]any{"type": "integer"}
		if i < edgevalidate.MaxSchemaCombinatorDepth {
			ref := fmt.Sprintf("#/$defs/level%d", i+1)
			if anchor {
				ref = fmt.Sprintf("#level%d", i+1)
			}
			next = map[string]any{keyword: ref}
		}
		def := map[string]any{"allOf": []any{next}}
		if anchor {
			def["$anchor"] = name
		}
		defs[name] = def
	}
	ref := "#/$defs/level0"
	if anchor {
		ref = "#level0"
	}
	raw, err := json.Marshal(map[string]any{"$defs": defs, keyword: ref})
	if err != nil {
		panic(err)
	}
	return raw
}

func TestAuditSchemaDepthBoundFollowsAnchorsAndDynamicReferences(t *testing.T) {
	// Positive control: the equivalent pointer $ref chain is rejected.
	if _, err := edgevalidate.Compile(auditCombinatorRefChain("$ref", false), false); !errors.Is(err, edgevalidate.ErrSchemaTooComplex) {
		t.Fatalf("pointer control: %v", err)
	}
	for _, tc := range []struct {
		name, keyword string
		anchor        bool
	}{
		{"anchor refs", "$ref", true},
		{"dynamic pointer refs", "$dynamicRef", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := edgevalidate.Compile(auditCombinatorRefChain(tc.keyword, tc.anchor), false)
			if err == nil {
				if fe, validationErr := compiled.Validate([]byte(`"wrong type"`)); validationErr != nil || fe == nil {
					t.Fatalf("reference chain must actually apply integer schema: fieldError=%+v error=%v", fe, validationErr)
				}
			}
			if !errors.Is(err, edgevalidate.ErrSchemaTooComplex) {
				t.Errorf("nine combinator levels accepted past eight-level bound via %s: %v", tc.name, err)
			}
		})
	}
}
