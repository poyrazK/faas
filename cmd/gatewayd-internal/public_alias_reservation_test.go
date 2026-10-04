// adr: 531
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

var aliasReservationModes = []string{"failed", "cancelled", "cleared", "deleted-owner", "internal-owner"}

func makeAliasUnavailable(t *testing.T, store state.Store, app state.App, deployment state.Deployment, mode string) {
	t.Helper()
	var err error
	switch mode {
	case "failed":
		err = store.UpdateDeploymentStatus(t.Context(), deployment.ID, state.DeployFailed, "test failure")
	case "cancelled":
		err = store.UpdateDeploymentStatus(t.Context(), deployment.ID, state.DeployCancelled, "test cancellation")
	case "cleared":
		if err = store.UpdateDeploymentStatus(t.Context(), deployment.ID, state.DeploySuperseded, ""); err == nil {
			err = store.ClearDeployment(t.Context(), deployment.ID, "test clear")
		}
	case "deleted-owner":
		status := state.AppDeleted
		_, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Status: &status})
	case "internal-owner":
		visibility := api.AppVisibilityInternal
		_, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Visibility: &visibility, SetVisibility: true})
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestPgRouter_DeploymentAliasReservationPreventsForeignFallback(t *testing.T) {
	for _, mode := range aliasReservationModes {
		t.Run(mode, func(t *testing.T) {
			store := state.NewMemStore(state.WithTrafficAppsDomain("apps.gregale.dev"))
			app := seedApp(t, store, "alias-owner", api.PlanPro)
			deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetDeploymentAlias(t.Context(), app.ID, "x", deployment.ID); err != nil {
				t.Fatal(err)
			}
			label, _ := api.DeploymentAliasHostLabel(app.ID, "x")
			peer := seedApp(t, store, label, api.PlanPro)
			router := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}
			host := label + router.appsSuffix
			if got, found, err := router.ResolveHost(t.Context(), host); err != nil || !found || got.ID != app.ID || got.PinnedDeploymentID != deployment.ID {
				t.Fatalf("initial alias: %+v found=%v err=%v", got, found, err)
			}
			makeAliasUnavailable(t, store, app, deployment, mode)
			if got, found, err := router.ResolveHost(t.Context(), host); err != nil || found || got.ID != "" {
				t.Fatalf("unavailable alias routed a foreign primary: %+v found=%v err=%v", got, found, err)
			}
			if reserved, err := store.DeploymentAliasReserved(t.Context(), label); err != nil || !reserved {
				t.Fatalf("unavailable alias lost reservation: %v %v", reserved, err)
			}
			if err := store.DeleteDeploymentAlias(t.Context(), app.ID, "x"); err != nil {
				t.Fatal(err)
			}
			if got, found, err := router.ResolveHost(t.Context(), host); err != nil || !found || got.ID != peer.ID || got.PinnedDeploymentID != "" {
				t.Fatalf("genuinely missing alias lost legacy fallback: %+v found=%v err=%v", got, found, err)
			}
		})
	}
}

func TestPublicAliasReservationPostgresPreventsFallbackAndStaleDispatch(t *testing.T) {
	for _, mode := range aliasReservationModes {
		t.Run(mode, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			f.store = state.NewPgStore(f.pool, state.WithTrafficAppsDomain("apps.gregale.dev"))
			deployment := f.deployment(t, "production", "sha256:alias-reservation")
			if _, err := f.store.SetDeploymentAlias(t.Context(), f.app.ID, "x", deployment.ID); err != nil {
				t.Fatal(err)
			}
			label, _ := api.DeploymentAliasHostLabel(f.app.ID, "x")
			peer := seedApp(t, f.store, label, api.PlanPro)
			router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev"}
			backend := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)
			host := label + router.appsSuffix
			old, found, err := backend.LookupHostPolicy(t.Context(), host)
			if err != nil || !found || old.ID != f.app.ID || old.PublicPolicySource == nil || old.PublicPolicySource.CanSubstitute {
				t.Fatalf("initial alias claim: %+v found=%v err=%v", old, found, err)
			}
			inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production", HostDeploymentID: deployment.ID, HostScope: "production"}
			if snapshot, err := newPublicRoutingPinner(f.store)(t.Context(), old, inputs); err != nil || !snapshot.HostAllowed {
				t.Fatalf("unchanged alias pin refused: %+v %v", snapshot, err)
			}
			before := aliasReservationRevision(t, f.store, label, true)
			makeAliasUnavailable(t, f.store, f.app, deployment, mode)
			if got, found, err := backend.LookupHostPolicy(t.Context(), host); err != nil || found || got.ID != "" || got.PublicPolicySource == nil || got.PublicPolicySource.CanSubstitute {
				t.Fatalf("unavailable alias routed or permitted substitution: %+v found=%v err=%v", got, found, err)
			}
			if snapshot, err := newPublicRoutingPinner(f.store)(t.Context(), old, inputs); err == nil || len(snapshot.Weights) != 0 {
				t.Fatalf("stale alias admitted dispatch: %+v %v", snapshot, err)
			}
			if aliasReservationRevision(t, f.store, label, true) != before {
				t.Fatal("target eligibility changed the raw alias reservation")
			}
			if err := f.store.DeleteDeploymentAlias(t.Context(), f.app.ID, "x"); err != nil {
				t.Fatal(err)
			}
			if aliasReservationRevision(t, f.store, label, false) == before {
				t.Fatal("alias removal did not change its reservation fingerprint")
			}
			if got, found, err := backend.LookupHostPolicy(t.Context(), host); err != nil || !found || got.ID != peer.ID || got.PinnedDeploymentID != "" {
				t.Fatalf("removed alias lost safe primary fallback: %+v found=%v err=%v", got, found, err)
			}
			if f.pool.Stat().AcquiredConns() != 0 {
				t.Fatal("alias projection retained a transaction")
			}
		})
	}
}

func aliasReservationRevision(t *testing.T, store *state.PgStore, label string, want bool) string {
	t.Helper()
	var revision string
	err := store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		reserved, err := reader.DeploymentAliasReserved(t.Context(), label)
		if err != nil || reserved != want {
			t.Fatalf("alias reservation: want=%v got=%v err=%v", want, reserved, err)
		}
		revision = reader.PublicHostPolicyRevision()
		return nil
	})
	if err != nil || revision == "" {
		t.Fatalf("reservation evidence: revision=%q err=%v", revision, err)
	}
	return revision
}

type aliasReservationUnavailableStore struct{ *state.PgStore }
type aliasReservationUnavailableReader struct{ state.PublicHostPolicyReader }

func (s aliasReservationUnavailableStore) WithPublicHostPolicySnapshot(ctx context.Context, read func(state.PublicHostPolicyReader) error) error {
	return s.PgStore.WithPublicHostPolicySnapshot(ctx, func(reader state.PublicHostPolicyReader) error {
		return read(aliasReservationUnavailableReader{PublicHostPolicyReader: reader})
	})
}

var errAliasReservationUnavailable = errors.New("alias reservation unavailable")

func (aliasReservationUnavailableReader) DeploymentAliasReserved(context.Context, string) (bool, error) {
	return false, errAliasReservationUnavailable
}

func TestPublicAliasReservationPostgresReaderFailureClosesFallback(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	const label = "tag-legacy-primary"
	seedApp(t, f.store, label, api.PlanPro)
	router := pgRouter{store: aliasReservationUnavailableStore{PgStore: f.store}, appsSuffix: ".apps.gregale.dev"}
	if got, found, err := router.ResolveHost(t.Context(), label+router.appsSuffix); !errors.Is(err, errAliasReservationUnavailable) || found || got.ID != "" {
		t.Fatalf("reservation outage admitted fallback: %+v found=%v err=%v", got, found, err)
	}
	if f.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("failed reservation reader retained its transaction")
	}
}
