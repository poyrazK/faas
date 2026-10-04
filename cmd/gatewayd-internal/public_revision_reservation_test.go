// adr: 531
package main

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublicRevisionReservationPostgresRefusesFallbackAndStaleDispatch(t *testing.T) {
	for _, mode := range []string{"failed", "cancelled", "superseded", "cleared", "deleted-owner", "internal-owner"} {
		t.Run(mode, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			deployment := f.deployment(t, "production", "sha256:revision-reservation")
			f.ingress(t, deployment.ID, 8081)
			label := fmt.Sprintf("deploy-%d-%s", deployment.Revision, f.app.Slug)
			seedApp(t, f.store, label, api.PlanPro)
			router := pgRouter{store: f.store, appsSuffix: ".gregale.dev", deploySuffix: ".gregale.dev"}
			host := gateway.BuildDeploymentPreviewURL(router.deploySuffix, deployment.Revision, f.app.Slug)
			backend := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)
			old, found, err := backend.LookupHostPolicy(t.Context(), host)
			if err != nil || !found || old.ID != f.app.ID || old.PinnedDeploymentID != deployment.ID || old.PrimaryIngressPort != 8081 || old.PublicPolicySource == nil || old.PublicPolicySource.CanSubstitute {
				t.Fatalf("initial immutable revision: %+v found=%v err=%v", old, found, err)
			}
			inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production", HostDeploymentID: deployment.ID, HostScope: "production"}
			if snapshot, err := newPublicRoutingPinner(f.store)(t.Context(), old, inputs); err != nil || !snapshot.HostAllowed {
				t.Fatalf("initial immutable dispatch: %+v %v", snapshot, err)
			}
			if mode == "superseded" {
				if err := f.store.UpdateDeploymentStatus(t.Context(), deployment.ID, state.DeploySuperseded, "withdrawn"); err != nil {
					t.Fatal(err)
				}
			} else {
				makeAliasUnavailable(t, f.store, f.app, deployment, mode)
			}
			fresh, found, err := backend.LookupHostPolicy(t.Context(), host)
			if err != nil || found || fresh.ID != "" || fresh.PublicPolicySource == nil || fresh.PublicPolicySource.CanSubstitute {
				t.Fatalf("unavailable revision exposed a primary or synthetic route: %+v found=%v err=%v", fresh, found, err)
			}
			if snapshot, err := newPublicRoutingPinner(f.store)(t.Context(), old, inputs); err == nil || len(snapshot.Weights) != 0 {
				t.Fatalf("stale immutable revision reached dispatch: %+v %v", snapshot, err)
			}
			if f.pool.Stat().AcquiredConns() != 0 {
				t.Fatal("revision lookup or refused dispatch retained its transaction")
			}
		})
	}
}
