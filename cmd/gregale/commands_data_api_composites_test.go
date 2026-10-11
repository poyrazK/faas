package main

import (
	"reflect"
	"testing"
)

func dataAPICompositeFixture() dataAPISnapshot {
	s := dataAPIDiffFixture()
	s.Version = 2
	s.Composites = []dataAPIComposite{{Schema: "api", Name: "location", Fields: []dataAPICompositeField{{Name: "x", Type: "number", Nullable: true}}}, {Schema: "api", Name: "profile", Fields: []dataAPICompositeField{{Name: "home", Type: `Database["api"]["CompositeTypes"]["location"]`, Nullable: true}}}}
	s.Functions[0].Returns = `Database["api"]["CompositeTypes"]["profile"]`
	return s
}

func TestDataAPICompositeDiffDetectsNestedChangesWithStableRPCReferences(t *testing.T) {
	for _, tc := range []struct {
		name, path, reason string
		modify             func(*dataAPISnapshot)
		breaking           bool
	}{
		{"unchanged", "", "", func(s *dataAPISnapshot) {}, false},
		{"removed", "api.location", "composite type removed; update readers, writes and RPC calls", func(s *dataAPISnapshot) { s.Composites = s.Composites[1:] }, true},
		{"field removed", "api.location.x", "composite field removed", func(s *dataAPISnapshot) { s.Composites[0].Fields = []dataAPICompositeField{} }, true},
		{"field type", "api.location.x", "composite field type changed; review nested values and RPCs", func(s *dataAPISnapshot) { s.Composites[0].Fields[0].Type = "string" }, true},
		{"field nullability", "api.location.x", "composite field nullability changed", func(s *dataAPISnapshot) { s.Composites[0].Fields[0].Nullable = false }, true},
		{"field added", "api.location.y", "composite field added; generated objects now require this key", func(s *dataAPISnapshot) {
			s.Composites[0].Fields = append(s.Composites[0].Fields, dataAPICompositeField{Name: "y", Type: "number", Nullable: true})
		}, true},
		{"type added", "api.new_type", "composite type added", func(s *dataAPISnapshot) {
			s.Composites = append(s.Composites, dataAPIComposite{Schema: "api", Name: "new_type", Fields: []dataAPICompositeField{}})
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := dataAPICompositeFixture(), dataAPICompositeFixture()
			tc.modify(&after)
			if _, err := parseDataAPIContract(dataAPIContractFixture(t, after)); err != nil {
				t.Fatal(err)
			}
			changes := diffDataAPISnapshots(before, after)
			expected := []dataAPIChange{}
			if tc.path != "" {
				expected = append(expected, dataAPIChange{Path: tc.path, Reason: tc.reason, Breaking: tc.breaking})
			}
			if !reflect.DeepEqual(changes, expected) {
				t.Fatalf("changes: %+v, expected: %+v", changes, expected)
			}
		})
	}
	legacy, current := dataAPIDiffFixture(), dataAPIDiffFixture()
	current.Version, current.Composites = 2, []dataAPIComposite{}
	if changes := diffDataAPISnapshots(legacy, current); len(changes) != 0 {
		t.Fatalf("format upgrade invented breaking changes: %+v", changes)
	}
}
