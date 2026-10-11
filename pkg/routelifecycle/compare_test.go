package routelifecycle

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeclarationChanges(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	operation := map[string]any{"deprecated": true, "x-gregale-deprecated-at": "2026-10-01T00:00:00Z", "x-gregale-sunset-at": "2026-11-01T00:00:00Z", "x-gregale-successor": "https://example.com/new"}
	document := func(op map[string]any) []byte {
		paths := map[string]any{}
		if op != nil {
			paths["/old"] = map[string]any{"get": op}
		}
		raw, _ := json.Marshal(map[string]any{"openapi": "3.1.0", "paths": paths})
		return raw
	}
	baseline := document(operation)
	for _, tc := range []struct {
		name, key string
		value     any
		remove    bool
		code      string
	}{
		{"unchanged", "", "", false, ""}, {"deprecation removed", "deprecated", false, false, "deprecation_removed"},
		{"date removed", "x-gregale-sunset-at", nil, true, "lifecycle_field_removed:x-gregale-sunset-at"},
		{"sunset accelerated", "x-gregale-sunset-at", "2026-10-09T00:00:00Z", false, "sunset_moved_earlier"},
		{"sunset extended", "x-gregale-sunset-at", "2026-12-01T00:00:00Z", false, ""},
		{"changed successor", "x-gregale-successor", "https://example.com/other", false, "successor_changed_requires_review"},
		{"invalid date", "x-gregale-sunset-at", "bad", false, "candidate_lifecycle_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := map[string]any{}
			for k, v := range operation {
				op[k] = v
			}
			if tc.key != "" {
				if tc.remove {
					delete(op, tc.key)
				} else {
					op[tc.key] = tc.value
				}
			}
			review := Compare(baseline, document(op), now)
			found := false
			for _, f := range review.Findings {
				if f.Code == tc.code {
					found = true
				}
			}
			if tc.code == "" {
				if review.Blocking() {
					t.Fatalf("unexpected findings %+v", review)
				}
			} else if !found {
				t.Fatalf("missing %s: %+v", tc.code, review)
			}
		})
	}
	review := Compare(baseline, document(nil), now)
	if !review.Blocking() || review.Findings[0].Code != "operation_removed_before_sunset" {
		t.Fatalf("premature removal %+v", review)
	}
	review = Compare(baseline, document(nil), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if review.Blocking() {
		t.Fatalf("elapsed sunset %+v", review)
	}
	review = Compare(nil, baseline, now)
	if review.Outcome != "incomplete" {
		t.Fatal("missing baseline was clear")
	}
	review = Compare([]byte(`{"openapi":"3.1.0","paths":{"/old":{"$ref":"#/other"}}}`), baseline, now)
	if review.Outcome != "incomplete" {
		t.Fatal("path reference was clear")
	}
}
