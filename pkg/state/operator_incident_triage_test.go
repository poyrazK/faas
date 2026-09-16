package state

import (
	"context"
	"strings"
	"testing"
)

func TestMemStoreOperatorIncidentTriageRoundTrip(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	rows, err := store.ListOperatorIncidentTriage(ctx, []string{"deployment:d1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("initial rows = %d, want 0", len(rows))
	}
	row, err := store.UpsertOperatorIncidentTriage(ctx, "deployment:d1", OperatorIncidentTriageAcknowledged, "oncall", "investigating", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != OperatorIncidentTriageAcknowledged || row.Owner != "oncall" {
		t.Fatalf("unexpected row: %+v", row)
	}
	rows, err = store.ListOperatorIncidentTriage(ctx, []string{"deployment:d1", "job_run:r1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rows["deployment:d1"]; got.Note != "investigating" {
		t.Fatalf("listed note = %q, want investigating", got.Note)
	}
	if _, ok := rows["job_run:r1"]; ok {
		t.Fatal("unexpected row for unknown dedupe key")
	}
}

func TestMemStoreOperatorIncidentTriageValidation(t *testing.T) {
	store := NewMemStore()
	if _, err := store.UpsertOperatorIncidentTriage(context.Background(), "deployment:d1", "unknown", "", "", "operator-1"); err == nil {
		t.Fatal("invalid status unexpectedly accepted")
	}
}

func TestMemStoreOperatorIncidentTriageMetadataValidation(t *testing.T) {
	store := NewMemStore()
	cases := []struct {
		name, key, owner, note, updatedBy string
	}{
		{name: "empty key", key: "", updatedBy: "operator-1"},
		{name: "control character owner", key: "deployment:d1", owner: "oncall\n", updatedBy: "operator-1"},
		{name: "oversized note", key: "deployment:d1", note: strings.Repeat("x", 1025), updatedBy: "operator-1"},
		{name: "empty actor", key: "deployment:d1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.UpsertOperatorIncidentTriage(context.Background(), tc.key, OperatorIncidentTriageOpen, tc.owner, tc.note, tc.updatedBy); err == nil {
				t.Fatal("invalid metadata unexpectedly accepted")
			}
		})
	}
}
