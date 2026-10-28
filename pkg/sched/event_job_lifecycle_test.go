package sched_test

// adr: 20261028095548

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventJobFailureLetsTheSweepContinue(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"backed-off job", &state.EventJobError{JobID: "job-1", Code: "internal", Err: errors.New("boom")}, true},
		{"ended job, wrapped", fmt.Errorf("sweep: %w", &state.EventJobError{JobID: "job-2", Code: "invalid_job", Ended: true, Err: errors.New("bad")}), true},
		{"store unavailable", errors.New("connection refused"), false},
	}
	l := &sched.Loop{}
	for _, tc := range cases {
		if got := l.XEventJobContinue("event replay backfill page", tc.err); got != tc.want {
			t.Errorf("%s: continue = %t, want %t", tc.name, got, tc.want)
		}
	}
}
