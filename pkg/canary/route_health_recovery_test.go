package canary

// adr: 458

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type recoveryCheckStub struct {
	stubAPID
	result   api.CanaryRouteHealthRecoveryResponse
	checkErr error
	calls    int
	expected int
}

func (a *recoveryCheckStub) RecoverCanaryRouteHealth(_ context.Context, _ string, step int) (api.CanaryRouteHealthRecoveryResponse, error) {
	a.calls++
	a.expected = step
	return a.result, a.checkErr
}
func TestCriticalRouteRecoveryRunsBeforeTimersAndAggregateGates(t *testing.T) {
	for _, scenario := range []string{"aborted", "transport", "malformed", "unknown", "disabled", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now()
			row := mirrorCleanRow(t, now)
			row.CanaryStepStarted = now
			client := &recoveryCheckStub{}
			switch scenario {
			case "aborted":
				client.result = api.CanaryRouteHealthRecoveryResponse{Aborted: true, AuditID: "12", RouteHealth: &api.RouteHealthDecision{DeploymentID: row.ID, Mode: "enforce", OnRegression: "abort", Status: "aborted", HistoryID: "saved"}}
			case "transport":
				client.checkErr = errors.New("transport failed")
			case "malformed":
				client.result.Aborted = true
			case "unknown":
				client.result.RouteHealth = &api.RouteHealthDecision{Mode: "enforce", OnRegression: "abort", Status: "blocked"}
			case "stale":
				client.checkErr = api.ErrCanaryStepConflict(0, 1)
			}
			// A mirror error and fresh stage timer would otherwise stop this tick.
			store := &stubStore{rows: []CanaryRow{row}, mirrorErr: errors.New("mirror not ready")}
			p := NewProgression(store, client, nil, slog.Default())
			p.Now = func() time.Time { return now }
			stats, err := p.Once(t.Context())
			if err != nil || client.calls != 1 || client.expected != row.CanaryStep || len(client.advances) != 0 {
				t.Fatal(stats, err, client.calls)
			}
			if scenario == "aborted" && stats.Aborted != 1 {
				t.Fatal("route failure waited on another gate", stats)
			}
			if (scenario == "transport" || scenario == "malformed") && stats.Errors != 1 {
				t.Fatal("invalid recovery response did not hold", stats)
			}
			if scenario == "stale" && (stats.Errors != 0 || stats.SkippedRouteGate != 1) {
				t.Fatal("stale stage counted as worker failure", stats)
			}
		})
	}
}
