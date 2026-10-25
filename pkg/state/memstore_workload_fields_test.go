package state_test

import (
	"context"
	"net/netip"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreWorkloadFieldsDefaultsAndUpdate(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	account, err := m.CreateAccount(ctx, "workload-fields-mem@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	created, err := m.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "workload-fields-mem-create",
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if created.WorkloadClass != state.WorkloadClassHTTP {
		t.Fatalf("CreateApp default WorkloadClass = %q, want %q", created.WorkloadClass, state.WorkloadClassHTTP)
	}

	quotaCreated, err := m.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID,
		Slug:      "workload-fields-mem-quota",
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	if quotaCreated.WorkloadClass != state.WorkloadClassHTTP {
		t.Fatalf("CreateAppIfUnderQuota default WorkloadClass = %q, want %q", quotaCreated.WorkloadClass, state.WorkloadClassHTTP)
	}

	class := state.WorkloadClassWorker
	updated, err := m.UpdateApp(ctx, created.ID, state.UpdateAppParams{WorkloadClass: &class})
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if updated.WorkloadClass != class {
		t.Fatalf("UpdateApp WorkloadClass = %q, want %q", updated.WorkloadClass, class)
	}
}

func TestMemCreateAppIfUnderQuotaPersistsAccessGates(t *testing.T) {
	testCreateAppIfUnderQuotaPersistsAccessGates(t, state.NewMemStore())
}

// PR previews copy their parent's access and egress gates and are created
// through the quota-aware insert. PostgreSQL's version of that insert wrote
// only require_authn and public_auth_mode, so a preview of an IP-allowlisted,
// password-protected or egress-restricted app came up without those gates.
func testCreateAppIfUnderQuotaPersistsAccessGates(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "access-gates-"+uuid.NewString()+"@example.test", api.PlanScale)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	egress := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	ingress := []netip.Prefix{netip.MustParsePrefix("198.51.100.7/32")}
	got, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "access-gates-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
		EgressAllowlist: egress, PublicAuthIPAllowlist: ingress, PublicAuthMode: api.AppPublicAuthModeIPAllowlist,
		PublicAuthBasicSealed: []byte("sealed-basic"), RequireSigned: true, RequireAuthn: true,
		ConsumerAuthMode: state.ConsumerAuthModeRequired,
	}, api.MustLimitsFor(api.PlanScale))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	stored, err := store.AppByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if !slices.Equal(stored.EgressAllowlist, egress) || !slices.Equal(stored.PublicAuthIPAllowlist, ingress) ||
		stored.PublicAuthMode != api.AppPublicAuthModeIPAllowlist || string(stored.PublicAuthBasicSealed) != "sealed-basic" ||
		!stored.RequireSigned || !stored.RequireAuthn || stored.ConsumerAuthMode != state.ConsumerAuthModeRequired {
		t.Fatalf("stored gates = egress %v ingress %v mode %q basic %q signed %v authn %v consumer %q",
			stored.EgressAllowlist, stored.PublicAuthIPAllowlist, stored.PublicAuthMode, stored.PublicAuthBasicSealed,
			stored.RequireSigned, stored.RequireAuthn, stored.ConsumerAuthMode)
	}
}
