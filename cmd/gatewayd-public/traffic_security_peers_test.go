// adr: 570
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeTrafficSecurityRoster struct {
	rows []state.ServingGatewayTrafficRuntime
	err  error
}

func (f *fakeTrafficSecurityRoster) ListServingGatewayTrafficRuntime(context.Context) ([]state.ServingGatewayTrafficRuntime, error) {
	return f.rows, f.err
}

func servingGateway(name string, reportedAgo time.Duration, revocation bool) state.ServingGatewayTrafficRuntime {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	row := state.ServingGatewayTrafficRuntime{NodeName: name, Generation: 3, ReportedAt: now.Add(-reportedAgo), DatabaseNow: now}
	row.SecurityRevocation = revocation
	return row
}

func TestAllStampTrafficSecurity(t *testing.T) {
	n1 := state.ServingGatewayTrafficRuntime{NodeName: "fsn-3.faas", DatabaseNow: time.Now()} // never reported
	tests := []struct {
		name       string
		rows       []state.ServingGatewayTrafficRuntime
		want       bool
		wantLegacy []string
	}{
		{"single-host edge", nil, true, nil},
		{"every gateway fresh with revocation", []state.ServingGatewayTrafficRuntime{servingGateway("fsn-2.faas", time.Second, true), servingGateway("fsn-3.faas", 0, true)}, true, nil},
		{"an N-1 gateway that never reported", []state.ServingGatewayTrafficRuntime{servingGateway("fsn-2.faas", time.Second, true), n1}, false, []string{"fsn-3.faas"}},
		{"a gateway without revocation wiring", []state.ServingGatewayTrafficRuntime{servingGateway("fsn-2.faas", time.Second, false)}, false, []string{"fsn-2.faas"}},
		{"a rolled-back gateway's stale report", []state.ServingGatewayTrafficRuntime{servingGateway("fsn-2.faas", api.TrafficRuntimeObservationFreshness+time.Second, true)}, false, []string{"fsn-2.faas"}},
		{"a report from the future", []state.ServingGatewayTrafficRuntime{servingGateway("fsn-2.faas", -time.Second, true)}, false, []string{"fsn-2.faas"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, legacy := allStampTrafficSecurity(tt.rows)
			if got != tt.want || !slices.Equal(legacy, tt.wantLegacy) {
				t.Fatalf("required=%v legacy=%v, want %v %v", got, legacy, tt.want, tt.wantLegacy)
			}
		})
	}
}

func TestTrafficSecurityPeersKeepsTheLastDecisionWhenTheRosterFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	roster := &fakeTrafficSecurityRoster{err: errors.New("db down")}
	peers := newTrafficSecurityPeers(t.Context(), roster, log)
	if !peers.Required() {
		t.Fatal("an unread roster must keep the snapshot required")
	}
	roster.err, roster.rows = nil, []state.ServingGatewayTrafficRuntime{{NodeName: "fsn-3.faas", DatabaseNow: time.Now()}}
	peers.refresh(t.Context())
	if peers.Required() {
		t.Fatal("an N-1 gateway in the roster must make the snapshot optional")
	}
	roster.err = errors.New("db down")
	peers.refresh(t.Context())
	if peers.Required() {
		t.Fatal("a failed read flipped the decision")
	}
	roster.err, roster.rows = nil, []state.ServingGatewayTrafficRuntime{servingGateway("fsn-3.faas", time.Second, true)}
	roster.rows[0].ReportedAt, roster.rows[0].DatabaseNow = time.Now().Add(-time.Second), time.Now()
	peers.refresh(t.Context())
	if !peers.Required() {
		t.Fatal("a fully upgraded fleet must require the snapshot again")
	}
}
