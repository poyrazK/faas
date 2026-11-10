package state_test

// adr: 20261110120000 — session bindings gate environment qualification like
// read_write; data_api and auth carry their own qualified contracts.

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCandidatePostgresDeadlineCoversServingModes(t *testing.T) {
	recorded := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		access string
		gated  bool
		err    error
	}{
		{access: "read_write", gated: true},
		{access: "session", gated: true},
		{access: "migration", gated: true},
		{access: "data_api"},
		{access: "auth"},
		{access: "unknown", err: state.ErrBindingReleaseRequired},
	} {
		t.Run(tc.access, func(t *testing.T) {
			deadline, err := state.CandidatePostgresDeadlineForAccess(tc.access, recorded, time.Hour)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.gated && !deadline.Equal(recorded.Add(time.Hour)) || !tc.gated && !deadline.IsZero() {
				t.Fatalf("deadline = %v, gated %t", deadline, tc.gated)
			}
		})
	}
}
