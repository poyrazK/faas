package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// One read says which subjects have an event of a kind since a time; other
// kinds, older events and other subjects are left out.
func TestListEventSubjectsWithKind(t *testing.T) {
	ctx := t.Context()
	s := state.NewMemStore()
	now := time.Now().UTC()
	crashed, parked, old, other := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, e := range []struct {
		subject, kind string
		at            time.Time
	}{
		{crashed, "liveness_failed", now.Add(-time.Minute)},
		{parked, "state_transition", now.Add(-time.Minute)},
		{old, "liveness_failed", now.Add(-time.Hour)},
		{other, "liveness_failed", now.Add(-time.Minute)},
	} {
		subject := e.subject
		if err := s.AppendEventAt(ctx, "schedd", e.kind, &subject, []byte(`{}`), e.at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListEventSubjectsWithKind(ctx, []string{crashed, parked, old}, "liveness_failed", now.Add(-15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[crashed] {
		t.Fatalf("subjects = %v, want only %s", got, crashed)
	}
	if empty, err := s.ListEventSubjectsWithKind(ctx, nil, "liveness_failed", now); err != nil || len(empty) != 0 {
		t.Fatalf("no subjects = %v, %v", empty, err)
	}
}
