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
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeTrafficSecurityRoster struct {
	nodes   []state.ComputeNode
	reports []state.ServingGatewayTrafficRuntime
	err     error
}

func (f *fakeTrafficSecurityRoster) ActiveComputeNodes(context.Context) ([]state.ComputeNode, error) {
	return f.nodes, f.err
}

func (f *fakeTrafficSecurityRoster) ListServingGatewayTrafficRuntime(context.Context) ([]state.ServingGatewayTrafficRuntime, error) {
	return f.reports, f.err
}

// computeNode is a compute_nodes row as the dialer sees it; the dialer has no
// role filter, unlike the observation roster.
func computeNode(name string, active bool, target string) state.ComputeNode {
	node := state.ComputeNode{Name: name, Active: active}
	if target != "" {
		node.GatewayTargetURL = &target
	}
	return node
}

func gatewayReport(name string, reportedAgo time.Duration, revocation bool) state.ServingGatewayTrafficRuntime {
	now := time.Now()
	row := state.ServingGatewayTrafficRuntime{NodeName: name, Generation: 3, ReportedAt: now.Add(-reportedAgo), DatabaseNow: now}
	row.SecurityRevocation = revocation
	return row
}

func TestFleetTrafficSecurityMembersMatchTheDialer(t *testing.T) {
	nodes := []state.ComputeNode{
		computeNode("fsn-2.faas", true, "tcp://fsn-2.gregale.dev:8080"),
		computeNode("legacy.faas", true, "tcp://legacy:8080"), // e.g. no role: dialable, not in the observation roster
		computeNode("drained.faas", false, "tcp://drained:8080"),
		computeNode("no-gateway.faas", true, ""),
		computeNode("blank.faas", true, "  "),
	}
	if got := fleetTrafficSecurityMembers(nodes); !slices.Equal(got, []string{"fsn-2.faas", "legacy.faas"}) {
		t.Fatalf("members = %v", got)
	}
	if got := localTrafficSecurityMembers(" fsn-1.faas ")(nodes); !slices.Equal(got, []string{"fsn-1.faas"}) {
		t.Fatalf("local members = %v", got)
	}
	if got := localTrafficSecurityMembers("")(nodes); got != nil {
		t.Fatalf("an unnamed local gateway = %v, want none", got)
	}
}

func TestAllStampTrafficSecurity(t *testing.T) {
	both := []string{"fsn-2.faas", "fsn-3.faas"}
	tests := []struct {
		name       string
		members    []string
		reports    []state.ServingGatewayTrafficRuntime
		want       bool
		wantLegacy []string
	}{
		{"no gateway to tell by", nil, nil, false, nil},
		{"every gateway fresh with revocation", both, []state.ServingGatewayTrafficRuntime{gatewayReport("fsn-2.faas", time.Second, true), gatewayReport("fsn-3.faas", 0, true)}, true, nil},
		{"an N-1 gateway that never reported", both, []state.ServingGatewayTrafficRuntime{gatewayReport("fsn-2.faas", time.Second, true), {NodeName: "fsn-3.faas", DatabaseNow: time.Now()}}, false, []string{"fsn-3.faas"}},
		{"a dialable gateway missing from the roster", both, []state.ServingGatewayTrafficRuntime{gatewayReport("fsn-2.faas", time.Second, true)}, false, []string{"fsn-3.faas"}},
		{"a gateway without revocation wiring", both[:1], []state.ServingGatewayTrafficRuntime{gatewayReport("fsn-2.faas", time.Second, false)}, false, []string{"fsn-2.faas"}},
		{"a rolled-back gateway's stale report", both[:1], []state.ServingGatewayTrafficRuntime{gatewayReport("fsn-2.faas", api.TrafficRuntimeObservationFreshness+time.Second, true)}, false, []string{"fsn-2.faas"}},
		{"a retired report", both[:1], []state.ServingGatewayTrafficRuntime{{NodeName: "fsn-2.faas", Generation: 3, DatabaseNow: time.Now()}}, false, []string{"fsn-2.faas"}},
		{"a report from the future", both[:1], []state.ServingGatewayTrafficRuntime{gatewayReport("fsn-2.faas", -time.Second, true)}, false, []string{"fsn-2.faas"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, legacy := allStampTrafficSecurity(tt.members, tt.reports)
			if got != tt.want || !slices.Equal(legacy, tt.wantLegacy) {
				t.Fatalf("required=%v legacy=%v, want %v %v", got, legacy, tt.want, tt.wantLegacy)
			}
		})
	}
}

func TestTrafficSecurityPeersFollowTheFleet(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	nodes := []state.ComputeNode{computeNode("fsn-2.faas", true, "tcp://fsn-2:8080"), computeNode("fsn-3.faas", true, "tcp://fsn-3:8080")}
	roster := &fakeTrafficSecurityRoster{err: errors.New("db down")}
	peers := newTrafficSecurityPeers(t.Context(), roster, fleetTrafficSecurityMembers, log)
	if peers.Required() {
		t.Fatal("an unread roster must not refuse every success")
	}
	// rc.296 on fsn-2, rc.294 held on fsn-3: unverified.
	roster.err, roster.nodes, roster.reports = nil, nodes, []state.ServingGatewayTrafficRuntime{gatewayReport("fsn-2.faas", time.Second, true)}
	peers.refresh(t.Context())
	if peers.Required() || testutil.ToFloat64(peers.legacy) != 1 || testutil.ToFloat64(peers.enforced) != 0 {
		t.Fatalf("mixed fleet: required=%v legacy=%v", peers.Required(), testutil.ToFloat64(peers.legacy))
	}
	// fsn-3 rolled too: enforced.
	roster.reports = append(roster.reports, gatewayReport("fsn-3.faas", 0, true))
	peers.refresh(t.Context())
	if !peers.Required() || testutil.ToFloat64(peers.legacy) != 0 || testutil.ToFloat64(peers.enforced) != 1 {
		t.Fatalf("upgraded fleet: required=%v legacy=%v", peers.Required(), testutil.ToFloat64(peers.legacy))
	}
	// A failed read keeps the decision.
	roster.err = errors.New("db down")
	peers.refresh(t.Context())
	if !peers.Required() {
		t.Fatal("a failed read flipped the decision")
	}
	// fsn-3 rolled back: its gatewayd-internal retires its report on shutdown.
	roster.err, roster.reports[1] = nil, state.ServingGatewayTrafficRuntime{NodeName: "fsn-3.faas", Generation: 4, DatabaseNow: time.Now()}
	peers.refresh(t.Context())
	if peers.Required() {
		t.Fatal("a retired gateway must make answers pass unverified")
	}
	peers.Unverified()
	if testutil.ToFloat64(peers.unverified) != 1 {
		t.Fatal("unverified answers are not counted")
	}
}
