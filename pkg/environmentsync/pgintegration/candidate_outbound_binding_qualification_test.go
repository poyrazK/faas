// adr: 20261019152416 — outboundd probe receipts admit enforced candidates' outbound bindings.
package pgintegration_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type candidateOutboundTestStore interface {
	candidateBindingTestStore
	state.EnvironmentQualificationOutboundStore
	state.OutboundBindingStore
	state.OutboundBindingProbeStore
}

var candidateOutboundProbe = api.OutboundBindingProbePolicy{Method: "GET", Path: "/v1/health", ExpectedStatus: 200}

// seedCandidateOutboundBinding creates a customer integration with a sealed
// credential, binds it to the app and configures probe, when given.
func seedCandidateOutboundBinding(t *testing.T, basic gitOpsTestStore, accountID, appID string, probe *api.OutboundBindingProbePolicy) string {
	t.Helper()
	store := basic.(candidateOutboundTestStore)
	id := uuid.NewString()
	if _, err := store.CreateOutboundIntegration(t.Context(), state.OutboundIntegrationOffer{ID: id, AccountID: accountID, Name: "provider-" + id[:8],
		Origin: "https://api.example.com", AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer", RequestPolicy: api.DefaultOutboundRequestPolicy()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundCredential(t.Context(), accountID, id, []byte("sealed-credential")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindOutboundIntegration(t.Context(), accountID, appID, id); err != nil {
		t.Fatal(err)
	}
	if probe != nil {
		if err := store.SetOutboundBindingProbePolicy(t.Context(), accountID, id, probe); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// outboundCandidateFixture prepares an enforced cohort whose HTTP candidate
// has one customer outbound binding.
func outboundCandidateFixture(t *testing.T, basic gitOpsTestStore, probe *api.OutboundBindingProbePolicy) (state.EnvironmentGitOpsLease, environmentsync.Plan, []state.EnvironmentWorkloadQualificationRequest, state.EnvironmentWorkloadQualificationRequest, string) {
	t.Helper()
	lease, plan, requests, placement := candidateBindingFixture(t, basic, "", "")
	web := httpCandidate(t, requests)
	integrationID := seedCandidateOutboundBinding(t, basic, lease.Source.AccountID, web.AppID, probe)
	completeCandidateBindingCohort(t, basic.(candidateBindingTestStore), requests, placement, true)
	return lease, plan, requests, web, integrationID
}

func pendingOutboundProbe(t *testing.T, store candidateOutboundTestStore, web state.EnvironmentWorkloadQualificationRequest) api.OutboundQualificationProbe {
	t.Helper()
	probes, err := store.PendingEnvironmentQualificationOutboundProbes(t.Context(), 10)
	if err != nil || len(probes) != 1 {
		t.Fatalf("pending probes: %+v %v", probes, err)
	}
	p := probes[0]
	if p.RequestID != web.ID || p.Attempt != web.Attempt || p.AppID != web.AppID || p.Policy != candidateOutboundProbe || !p.Valid() {
		t.Fatalf("probe is not bound to the candidate binding: %+v", p)
	}
	return p
}

func TestCandidateBindingOutboundReceiptQualifiesBinding(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests, web, integrationID := outboundCandidateFixture(t, basic, &candidateOutboundProbe)
		store := basic.(candidateOutboundTestStore)
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
			t.Fatalf("unprobed outbound binding activated: %v %v", active, err)
		}
		assertCandidatesHeld(t, basic, requests)
		probe := pendingOutboundProbe(t, store, web)
		if probe.IntegrationID != integrationID {
			t.Fatalf("probe integration = %s, want %s", probe.IntegrationID, integrationID)
		}
		if err := store.RecordEnvironmentQualificationOutboundReceipt(t.Context(), probe, 204); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("receipt accepted an unexpected status", err)
		}
		stale := probe
		stale.Revision = strings.Repeat("0", 64)
		if err := store.RecordEnvironmentQualificationOutboundReceipt(t.Context(), stale, 200); !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatal("receipt accepted a different revision", err)
		}
		if err := store.RecordEnvironmentQualificationOutboundReceipt(t.Context(), probe, 200); err != nil {
			t.Fatal(err)
		}
		if again, err := store.PendingEnvironmentQualificationOutboundProbes(t.Context(), 10); err != nil || len(again) != 0 {
			t.Fatalf("probed binding still pending: %+v %v", again, err)
		}
		release, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
		if err != nil || !active || len(release.Members) != 2 {
			t.Fatalf("checked activation with outbound receipt: %+v %v %v", release, active, err)
		}
	})
}

func TestCandidateBindingOutboundCredentialChangeNeedsANewProbe(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests, web, integrationID := outboundCandidateFixture(t, basic, &candidateOutboundProbe)
		store := basic.(candidateOutboundTestStore)
		first := pendingOutboundProbe(t, store, web)
		if err := store.RecordEnvironmentQualificationOutboundReceipt(t.Context(), first, 200); err != nil {
			t.Fatal(err)
		}
		if err := store.SetOutboundCredential(t.Context(), lease.Source.AccountID, integrationID, []byte("rotated-credential")); err != nil {
			t.Fatal(err)
		}
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
			t.Fatalf("receipt for the previous credential activated: %v %v", active, err)
		}
		assertCandidatesHeld(t, basic, requests)
		second := pendingOutboundProbe(t, store, web)
		if second.Revision == first.Revision {
			t.Fatal("credential rotation kept the binding revision")
		}
		if err := store.RecordEnvironmentQualificationOutboundReceipt(t.Context(), second, 200); err != nil {
			t.Fatal(err)
		}
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !active {
			t.Fatalf("activation after a new probe: %v %v", active, err)
		}
	})
}

func TestCandidateBindingOutboundWithoutProbeStaysHeld(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests, _, _ := outboundCandidateFixture(t, basic, nil)
		store := basic.(candidateOutboundTestStore)
		if probes, err := store.PendingEnvironmentQualificationOutboundProbes(t.Context(), 10); err != nil || len(probes) != 0 {
			t.Fatalf("binding without a probe policy was offered: %+v %v", probes, err)
		}
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
			t.Fatalf("outbound binding without a probe activated: %v %v", active, err)
		}
		assertCandidatesHeld(t, basic, requests)
	})
}

func TestPgCandidateOutboundBindingCatalog(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	lease, plan, requests, web, integrationID := outboundCandidateFixture(t, store, &candidateOutboundProbe)
	// An operator attachment has no probe policy and stays blocked.
	if _, err := pool.Exec(t.Context(), "INSERT INTO outbound_integration_apps(integration_id,app_id) VALUES($1,$2)", integrationID, web.AppID); err != nil {
		t.Fatal(err)
	}
	if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
		t.Fatalf("operator attachment activated: %v %v", active, err)
	}
	assertCandidatesHeld(t, store, requests)
	if _, err := pool.Exec(t.Context(), "DELETE FROM outbound_integration_apps WHERE app_id=$1", web.AppID); err != nil {
		t.Fatal(err)
	}
	// A disabled integration is skipped, as binding verification skips it.
	if _, err := pool.Exec(t.Context(), "UPDATE outbound_integrations SET enabled=false WHERE id=$1", integrationID); err != nil {
		t.Fatal(err)
	}
	if probes, err := store.PendingEnvironmentQualificationOutboundProbes(t.Context(), 10); err != nil || len(probes) != 0 {
		t.Fatalf("disabled integration was offered: %+v %v", probes, err)
	}
	if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !active {
		t.Fatalf("disabled outbound binding blocked activation: %v %v", active, err)
	}
}

func TestPgCandidateOutboundReceiptsAreImmutable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, _, _, web, _ := outboundCandidateFixture(t, store, &candidateOutboundProbe)
	probe := pendingOutboundProbe(t, store, web)
	if err := store.RecordEnvironmentQualificationOutboundReceipt(t.Context(), probe, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE environment_qualification_outbound_receipts SET status=201 WHERE request_id=$1", web.ID); err == nil {
		t.Fatal("outbound receipt was mutable")
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM environment_qualification_outbound_receipts WHERE request_id=$1", web.ID); err == nil {
		t.Fatal("outbound receipt was deletable")
	}
	source, err := os.ReadFile("../../../migrations/20261019161558609_candidate_outbound_binding_qualification.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded outbound receipt history")
	}
}
