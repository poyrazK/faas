package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The rc.344 production gate's last poll landed on the wait deadline, and the
// read was bounded by that deadline. It started with an expired context, every
// source failed, and the report said source_unavailable for all three checks
// instead of the serving result the polls had observed.
func TestObsDeploymentFinalReadKeepsLastObservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   string
		code   int
		state  string
		reason string
	}{
		{"confirmed fault survives the deadline", "fault", 1, "degraded", "serving_probe_fault"},
		{"unknown evidence keeps its health reason", "unknown", 3, "unknown", "serving_expectations_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: 2 * time.Second, poll: time.Second}
			calls := 0
			fetch := func(ctx context.Context) obsDeploymentSnapshot {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("read has no deadline")
				}
				s := deploymentSnapshotTest(now)
				switch tc.mode {
				case "fault":
					deploymentProbeTestState(&s, "missing", "result_missing")
				case "unknown":
					// A fresh inventory explicitly marked unconfigured, as on
					// production before serving probes were set up.
					no, zero := false, 0
					v := &s.serving.value
					v.Inventory.Configured, v.Inventory.ExpectedPairs = &no, &zero
					v.Counts, v.Probes = nil, []api.ObsServingProbe{}
					v.State, v.Reason = "partial", "inventory_not_configured"
				}
				if deadline.Before(now.Add(api.ObsPostDeployHTTPTimeout)) {
					// What the real HTTP client does when the context expires
					// before apid can answer.
					s.serving.err, s.notificationErr = errors.New("expired"), errors.New("expired")
				}
				return s
			}
			wait := func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }
			report, code := runObsDeploymentVerification(context.Background(), opts, fetch, func() time.Time { return now }, wait, func(obsDeploymentReport) error { return nil })
			if code != tc.code || report.State != tc.state || calls != 3 {
				t.Fatalf("code=%d state=%s calls=%d report=%+v", code, report.State, calls, report)
			}
			if got := report.Checks[0]; got.Reason != tc.reason {
				t.Fatalf("serving check = %+v, want reason %s", got, tc.reason)
			}
			for _, check := range report.Checks {
				if check.Reason == "source_unavailable" {
					t.Fatalf("final report lost the last observation: %+v", report.Checks)
				}
			}
		})
	}
}
