// adr: 570
package gateway

import (
	"context"
	"testing"
	"time"
)

// legacyRosterRouting is a project app whose production lane still reads its
// roster from the legacy default scope: requests resolve in "production", the
// selected deployment lives in "default".
func legacyRosterRouting() PublicRoutingSnapshot {
	return PublicRoutingSnapshot{AppID: "app", SelectedDeploymentID: "selected", Scope: "production", RosterScope: "default",
		SelectionReason: "weighted", Weights: []DeploymentWeightsRow{{ID: "selected", TrafficPercent: 100}}}
}

type scopeRecordingBurstBackend struct {
	*PGBackend
	scopes chan string
}

func (b *scopeRecordingBurstBackend) AdmitDeploymentBurst(_ context.Context, _, _, scope, _ string, _, _ int) (int, error) {
	b.scopes <- scope
	return 0, nil
}

// PulseWatch F11: 185 open SSE streams on a compose-project app never woke a
// second VM. Every burst admission asked schedd for the selected deployment in
// the request's "production" scope; the deployment lives in "default", so
// schedd answered "the app has no live deployment to wake" (319 times on
// production-us, 2026-10-31). TestPublicRoutingAdmitsInTheRosterScope pins the
// same rule for the first wake.
func TestPublicRoutingBurstAdmitsInRosterScope(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	b := &scopeRecordingBurstBackend{PGBackend: NewPGBackend(nil, nil, nil), scopes: make(chan string, 4)}
	h.backend = b
	h.burstPressure.state(fixture.app.ID).inflight.Store(2)
	ctx, cancel := context.WithTimeout(t.Context(), 3*routableTargetPollInterval)
	defer cancel()
	routing := legacyRosterRouting()
	routing.AppID = fixture.app.ID
	_, _ = h.maybePublicRoutingBurst(ctx, fixture.app, 2, 1, routing)
	select {
	case scope := <-b.scopes:
		if scope != "default" {
			t.Fatalf("burst admitted the selected deployment in scope %q, want its roster scope %q", scope, "default")
		}
	case <-time.After(time.Second):
		t.Fatal("burst admission did not run")
	}
}

func TestPublicStaleRecoveryAdmitsInRosterScope(t *testing.T) {
	scheduler := &capturedRecoveryScheduler{calls: make(chan capturedRecoveryCall, 1)}
	b := NewPGBackend(nil, scheduler, nil).WithStore(serviceDiscoveryWeightStore{})
	ctx := context.WithValue(t.Context(), publicRoutingSnapshotKey{}, legacyRosterRouting())
	b.RecoverStaleTarget(ctx, "app", "production", 2)
	select {
	case call := <-scheduler.calls:
		if call.deployment != "selected" || call.scope != "default" {
			t.Fatalf("stale recovery admitted %+v, want deployment selected in its roster scope default", call)
		}
	case <-time.After(time.Second):
		t.Fatal("stale recovery did not admit a replacement")
	}
}
