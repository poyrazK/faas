// adr: 570
package gateway

import (
	"context"
	"testing"
	"time"
)

// A snapshot may become stale between discovery and selecting a TCP hop.
// Keep the real backend's current readiness and wake identity authoritative.
type tcpWithdrawalProvider struct {
	*PGBackend
	afterSnapshot func()
}

func (p *tcpWithdrawalProvider) ServiceEndpoints(ctx context.Context, appID string) (ServiceEndpointsSnapshot, error) {
	snapshot, err := p.PGBackend.ServiceEndpoints(ctx, appID)
	if p.afterSnapshot != nil {
		withdraw := p.afterSnapshot
		p.afterSnapshot = nil
		withdraw()
	}
	return snapshot, err
}

func TestServiceTCPProxyRefusesWithdrawnSnapshotAndRecovers(t *testing.T) {
	for _, change := range []string{"readiness", "wake", "node"} {
		t.Run(change, func(t *testing.T) {
			h := newTCPTestHarness(t)
			target, _ := repairReadyTarget()
			target.AppID, target.WakeID, target.Port = "db", "old", 8080
			backend := NewPGBackend(nil, nil, nil)
			backend.RecordTarget("db", target)
			provider := &tcpWithdrawalProvider{PGBackend: backend}
			provider.afterSnapshot = func() {
				switch change {
				case "readiness":
					backend.SetInstanceReadinessForTarget("db", target.InstanceID, target.WakeID, target.NodeID,
						"primary_app", "unready", time.Now(), 100)
				case "wake":
					target.WakeID = "replacement"
					backend.RecordTarget("db", target)
				case "node":
					target.NodeID = "replacement"
					backend.RecordTarget("db", target)
				}
			}
			h.proxy.cfg.Services.provider = provider
			if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason != "no_replica" {
				t.Fatalf("stale snapshot reason = %q, want no_replica", reason)
			}
			if len(h.hops) != 0 || h.wakes != 0 {
				t.Fatalf("stale snapshot reached a guest or triggered a wake: hops=%+v wakes=%d", h.hops, h.wakes)
			}
			backend.SetInstanceReadinessForTarget("db", target.InstanceID, target.WakeID, target.NodeID,
				"primary_app", "ready", time.Now().Add(time.Second), 101)
			if outcome, reason := h.serve(t, "caller", "198.19.0.7:5432"); outcome != "success" || reason != "" {
				t.Fatalf("recovery outcome=%q reason=%q", outcome, reason)
			}
			if len(h.hops) != 1 || h.hops[0].WakeID != target.WakeID || h.hops[0].NodeID != target.NodeID {
				t.Fatalf("recovery used the old endpoint identity: %+v", h.hops)
			}
		})
	}
}

func TestServiceTCPProxyRetainsZeroTrafficReleasePin(t *testing.T) {
	for _, parked := range []bool{false, true} {
		name := "warm"
		if parked {
			name = "parked"
		}
		t.Run(name, func(t *testing.T) {
			h := newTCPTestHarness(t)
			h.release = "retained"
			backend := NewPGBackend(nil, nil, nil).WithStore(serviceDiscoveryWeightStore{rows: []DeploymentWeightsRow{
				{ID: "retained", TrafficPercent: 0}, {ID: "current", TrafficPercent: 100},
			}})
			retained := Target{InstanceID: "old", NodeID: "node", WakeID: "retained-wake", DeploymentID: "retained", Port: 8080}
			backend.RecordTarget("db", Target{InstanceID: "new", NodeID: "node", WakeID: "current-wake", DeploymentID: "current", Port: 8080})
			if !parked {
				backend.RecordTarget("db", retained)
			}
			services := h.proxy.cfg.Services
			services.provider = backend
			deploymentWakes := 0
			services.wakeDeployment = func(_ context.Context, appID, deploymentID string) error {
				if appID != "db" || deploymentID != "retained" {
					t.Fatalf("wake escaped the release pin: app=%q deployment=%q", appID, deploymentID)
				}
				deploymentWakes++
				backend.RecordTarget("db", retained)
				return nil
			}
			if outcome, reason := h.serve(t, "caller", "198.19.0.7:5432"); outcome != "success" || reason != "" {
				t.Fatalf("pinned outcome=%q reason=%q", outcome, reason)
			}
			wantWakes := 0
			if parked {
				wantWakes = 1
			}
			if len(h.hops) != 1 || h.hops[0].InstanceID != "old" || h.hops[0].DeploymentID != "retained" ||
				h.hops[0].WakeID != "retained-wake" || deploymentWakes != wantWakes || h.wakes != 0 {
				t.Fatalf("pin used a sibling or unscoped wake: hops=%+v exact wakes=%d app wakes=%d", h.hops, deploymentWakes, h.wakes)
			}
			h.release = ""
			if outcome, reason := h.serve(t, "caller", "198.19.0.7:5432"); outcome != "success" || reason != "" {
				t.Fatalf("ordinary outcome=%q reason=%q", outcome, reason)
			}
			if len(h.hops) != 2 || h.hops[1].InstanceID != "new" || h.hops[1].DeploymentID != "current" {
				t.Fatalf("zero-traffic retained deployment leaked into ordinary traffic: %+v", h.hops)
			}
		})
	}
}
