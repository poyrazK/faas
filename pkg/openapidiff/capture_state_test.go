package openapidiff

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestEdgeRuleSnapshotHoldsOnlyTheAppSurface pins H9-168: a deployment with no
// imported OpenAPI document stored Gregale's own embedded API (890 paths,
// /v1/apps, /login, ...) plus its edge-rule routes, ~760 KB per deployment on
// production-us. The snapshot must hold the app's edge-rule surface only.
func TestEdgeRuleSnapshotHoldsOnlyTheAppSurface(t *testing.T) {
	snap, source, err := snapshotFromDocument("dep-1", "app-1", "prod", nil, []api.CreateEdgeRuleRequest{{
		Kind: "route", MatchHost: "demo.example.com", MatchPath: "/orders", MatchMethods: []string{"GET", "POST"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if source != SnapshotSourceEdgeRules {
		t.Fatalf("source = %q, want %q", source, SnapshotSourceEdgeRules)
	}
	if len(snap.Snapshot) > 8<<10 {
		t.Fatalf("edge-rule snapshot is %d bytes; it should hold one route", len(snap.Snapshot))
	}
	var doc struct {
		Spec struct {
			Paths map[string]json.RawMessage `json:"paths"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(snap.Snapshot, &doc); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(doc.Spec.Paths) != 1 {
		paths := make([]string, 0, 3)
		for p := range doc.Spec.Paths {
			if len(paths) == 3 {
				break
			}
			paths = append(paths, p)
		}
		t.Fatalf("snapshot paths = %d (e.g. %v), want only the app's route", len(doc.Spec.Paths), paths)
	}
	if _, ok := doc.Spec.Paths["demo.example.com/orders"]; !ok {
		t.Fatalf("snapshot paths = %v, want demo.example.com/orders", doc.Spec.Paths)
	}
}
