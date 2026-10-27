package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"reflect"
	"testing"
)

func dataAPICompositeFixture() main.DataAPISnapshot {
	s := main.DataAPIDiffFixture()
	s.Version = 2
	s.Composites = []main.DataAPIComposite{{Schema: "api", Name: "location", Fields: []main.DataAPICompositeField{{Name: "x", Type: "number", Nullable: true}}}, {Schema: "api", Name: "profile", Fields: []main.DataAPICompositeField{{Name: "home", Type: `Database["api"]["CompositeTypes"]["location"]`, Nullable: true}}}}
	s.Functions[0].Returns = `Database["api"]["CompositeTypes"]["profile"]`
	return s
}

func TestDataAPICompositeDiffDetectsNestedChangesWithStableRPCReferences(t *testing.T) {
	for _, tc := range []struct {
		name, path, reason string
		modify             func(*main.DataAPISnapshot)
		breaking           bool
	}{
		{"unchanged", "", "", func(s *main.DataAPISnapshot) {}, false},
		{"removed", "api.location", "composite type removed; update readers, writes and RPC calls", func(s *main.DataAPISnapshot) { s.Composites = s.Composites[1:] }, true},
		{"field removed", "api.location.x", "composite field removed", func(s *main.DataAPISnapshot) { s.Composites[0].Fields = []main.DataAPICompositeField{} }, true},
		{"field type", "api.location.x", "composite field type changed; review nested values and RPCs", func(s *main.DataAPISnapshot) { s.Composites[0].Fields[0].Type = "string" }, true},
		{"field nullability", "api.location.x", "composite field nullability changed", func(s *main.DataAPISnapshot) { s.Composites[0].Fields[0].Nullable = false }, true},
		{"field added", "api.location.y", "composite field added; generated objects now require this key", func(s *main.DataAPISnapshot) {
			s.Composites[0].Fields = append(s.Composites[0].Fields, main.DataAPICompositeField{Name: "y", Type: "number", Nullable: true})
		}, true},
		{"type added", "api.new_type", "composite type added", func(s *main.DataAPISnapshot) {
			s.Composites = append(s.Composites, main.DataAPIComposite{Schema: "api", Name: "new_type", Fields: []main.DataAPICompositeField{}})
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := dataAPICompositeFixture(), dataAPICompositeFixture()
			tc.modify(&after)
			if _, err := main.ParseDataAPIContract(main.DataAPIContractFixture(t, after)); err != nil {
				t.Fatal(err)
			}
			changes := main.DiffDataAPISnapshots(before, after)
			expected := []main.DataAPIChange{}
			if tc.path != "" {
				expected = append(expected, main.DataAPIChange{Path: tc.path, Reason: tc.reason, Breaking: tc.breaking})
			}
			if !reflect.DeepEqual(changes, expected) {
				t.Fatalf("changes: %+v, expected: %+v", changes, expected)
			}
		})
	}
	legacy, current := main.DataAPIDiffFixture(), main.DataAPIDiffFixture()
	current.Version, current.Composites = 2, []main.DataAPIComposite{}
	if changes := main.DiffDataAPISnapshots(legacy, current); len(changes) != 0 {
		t.Fatalf("format upgrade invented breaking changes: %+v", changes)
	}
}
