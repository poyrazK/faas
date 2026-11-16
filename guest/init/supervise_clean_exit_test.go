package main

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Deploy-path review P4 (prod test 2026-11-15): an app that exited 0 without
// listening printed nothing, so the host could not tell it from a guest
// failure and the deploy failed as internal. A clean exit the policy does not
// restart runs OnCleanExit; a requested stop, a failure and a restarted clean
// exit do not.
func TestSupervisorReportsACleanExitItDoesNotRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy string
		err    error
		stop   bool
		want   int32
	}{
		{name: "clean exit on-failure", policy: api.RestartPolicyOnFailure, want: 1},
		{name: "clean exit no", policy: api.RestartPolicyNo, want: 1},
		{name: "requested stop", policy: api.RestartPolicyOnFailure, stop: true},
		{name: "failure", policy: api.RestartPolicyNo, err: errors.New("exit status 1")},
		{name: "always restarts until the budget", policy: api.RestartPolicyAlways},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reported atomic.Int32
			s := &Supervisor{Max: 1, Policy: tc.policy}
			s.Start = func() error {
				if tc.stop {
					s.RequestStop()
				}
				return tc.err
			}
			s.OnCleanExit = func() { reported.Add(1) }
			_ = s.Run()
			if got := reported.Load(); got != tc.want {
				t.Fatalf("OnCleanExit calls = %d, want %d", got, tc.want)
			}
		})
	}
}
