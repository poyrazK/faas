package main

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/dataapi"
)

func TestDataAPIResultRelationshipDiff(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after *dataapi.Setof
		breaking      bool
	}{
		{"added", nil, &dataapi.Setof{To: "notes"}, false},
		{"removed", &dataapi.Setof{To: "notes"}, nil, true},
		{"source changed", &dataapi.Setof{From: "notes", To: "notes"}, &dataapi.Setof{From: "comments", To: "notes"}, true},
		{"target changed", &dataapi.Setof{To: "notes"}, &dataapi.Setof{To: "comments"}, true},
		{"cardinality changed", &dataapi.Setof{From: "notes", To: "notes"}, &dataapi.Setof{From: "notes", To: "notes", OneToOne: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := dataAPIDiffFixture(), dataAPIDiffFixture()
			before.Functions[0].Setof, after.Functions[0].Setof = tc.before, tc.after
			changes := diffDataAPISnapshots(before, after)
			want := []dataAPIChange{{Path: before.Functions[0].Schema + "." + before.Functions[0].Name,
				Reason: "RPC result relationship metadata changed; review projections and computed embeds", Breaking: tc.breaking}}
			if !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes=%+v, want=%+v", changes, want)
			}
		})
	}
}
