package apidserver

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// ADR-20261127103000: a deployment-success value under target degrades the
// Deployments component only while platform failures are recent. Each row is
// one combination of the 24-hour aggregate and the last-hour count.
func TestDeploymentBreachIsCurrent(t *testing.T) {
	cases := []struct {
		name           string
		tenantsCounted bool
		failedAccounts int
		recencyKnown   bool
		recentFailures int
		want           bool
	}{
		{name: "prometheus fallback cannot tell", tenantsCounted: false, want: true},
		{name: "one failing account", tenantsCounted: true, failedAccounts: 1, recencyKnown: true, recentFailures: 1, want: false},
		{name: "two accounts and a failure this hour", tenantsCounted: true, failedAccounts: 2, recencyKnown: true, recentFailures: 1, want: true},
		{name: "two accounts but none this hour", tenantsCounted: true, failedAccounts: 2, recencyKnown: true, recentFailures: 0, want: false},
		{name: "recent count unavailable keeps the breach", tenantsCounted: true, failedAccounts: 2, recencyKnown: false, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := statusEvaluation{
				legacy:                   StatusPage{APIAvailabilityPct: 100, BuildSuccessPct: 88.9},
				states:                   publicstatus.Evaluate(nil, nil),
				indicatorAvailable:       map[string]bool{"api_availability": true, "build_success": true},
				deploymentTenantsCounted: tc.tenantsCounted,
				deploymentFailedAccounts: tc.failedAccounts,
				deploymentRecencyKnown:   tc.recencyKnown,
				deploymentRecentFailures: tc.recentFailures,
			}
			if got := deploymentBreachIsCurrent(&snap); got != tc.want {
				t.Fatalf("deploymentBreachIsCurrent = %v, want %v", got, tc.want)
			}
			applyStatusIndicatorBreaches(&snap)
			wantState := publicstatus.StateOperational
			if tc.want {
				wantState = publicstatus.StateDegraded
			}
			if got := snap.states[publicstatus.ComponentDeployments]; got != wantState || snap.legacy.Degraded != tc.want {
				t.Fatalf("deployments = %q degraded = %v, want %q / %v", got, snap.legacy.Degraded, wantState, tc.want)
			}
		})
	}
}

// windowOutcomeStore answers the 24-hour and the last-hour deployment
// aggregates separately, as production did on 2026-11-27: three rollout
// failures from two accounts in the day, none after 09:15Z.
type windowOutcomeStore struct {
	MemStore  *state.MemStore
	day, hour state.DeploymentOutcomeCounts
	hourErr   error
	calls     []time.Duration
}

func (s *windowOutcomeStore) CountDeploymentOutcomesSince(_ context.Context, since time.Time, _ ...string) (state.DeploymentOutcomeCounts, error) {
	age := time.Since(since).Round(time.Hour)
	s.calls = append(s.calls, age)
	if age > 2*time.Hour {
		return s.day, nil
	}
	return s.hour, s.hourErr
}

func TestStatusDeploymentsRecoverOnceFailuresAreOld(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"100"]}]}}`))
	}))
	defer prom.Close()

	day := state.DeploymentOutcomeCounts{Succeeded: 24, Failed: 3, FailedAccounts: 2}
	cases := []struct {
		name     string
		hour     state.DeploymentOutcomeCounts
		hourErr  error
		degraded bool
	}{
		{name: "no failure in the last hour", hour: state.DeploymentOutcomeCounts{Succeeded: 9}, degraded: false},
		{name: "a failure in the last hour", hour: state.DeploymentOutcomeCounts{Succeeded: 9, Failed: 1, FailedAccounts: 1}, degraded: true},
		{name: "last-hour count failed", hourErr: errors.New("timeout"), degraded: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &windowOutcomeStore{MemStore: state.NewMemStore(), day: day, hour: tc.hour, hourErr: tc.hourErr}
			snap, err := newStatusCacheWithStore(prom.URL, statetest.NewMem(store), slog.Default()).fetch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if want := float64(24) / 27 * 100; math.Abs(snap.legacy.BuildSuccessPct-want) > 0.001 {
				t.Fatalf("deployment success = %.3f%%, want the 24-hour %.3f%%", snap.legacy.BuildSuccessPct, want)
			}
			wantState := publicstatus.StateOperational
			if tc.degraded {
				wantState = publicstatus.StateDegraded
			}
			if got := snap.states[publicstatus.ComponentDeployments]; got != wantState || snap.legacy.Degraded != tc.degraded {
				t.Fatalf("deployments = %q degraded = %v, want %q / %v", got, snap.legacy.Degraded, wantState, tc.degraded)
			}
			if len(store.calls) != 2 || store.calls[0] != 24*time.Hour || store.calls[1] != time.Hour {
				t.Fatalf("aggregate windows = %v, want [24h 1h]", store.calls)
			}
		})
	}
}
