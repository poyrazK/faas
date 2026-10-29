package state

import (
	"errors"
	"testing"
)

// Hunt 20 H20-2: a stamp moves a rollout only from an in-flight state, so an
// abort is never overwritten. Re-stamping the state a rollout already has
// changes no outcome and stays allowed; tests and fixtures re-stamp a
// complete rollout's times.
func TestCheckRolloutStamp(t *testing.T) {
	for _, tc := range []struct {
		current, next string
		allowed       bool
	}{
		{"pending", "rolling_out", true},
		{"", "rolling_out", true},
		{"pending", "complete", true},
		{"rolling_out", "complete", true},
		{"pending", "aborted", true},
		{"rolling_out", "aborted", true},
		{"complete", "complete", true},
		{"aborted", "aborted", true},
		{"rolling_out", "rolling_out", true},
		{"aborted", "complete", false},
		{"aborted", "rolling_out", false},
		{"complete", "rolling_out", false},
		{"complete", "aborted", false},
	} {
		err := checkRolloutStamp(tc.current, tc.next)
		if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, ErrRolloutStateInvalid) {
			t.Errorf("checkRolloutStamp(%q, %q) = %v, want allowed %v", tc.current, tc.next, err, tc.allowed)
		}
	}
}
