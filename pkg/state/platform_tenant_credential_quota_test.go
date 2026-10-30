package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// tenantKeySubjectFixture is a company on the Hobby plan that adopted billing,
// whose owner's company app holds all but one of the company's consumer keys.
type tenantKeySubjectFixture struct {
	store   state.Store
	orgID   string
	suffix  string
	members state.APIConsumer
	limit   int
}

// tenantKeyOwner is a company member who runs a platform tenant on the
// member's personal and company apps.
type tenantKeyOwner struct {
	account           state.Account
	tenantID          string
	personal, company state.APIConsumer
}

func newTenantKeySubjectFixture(t *testing.T, store state.Store, adopt func(orgID string)) tenantKeySubjectFixture {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	owner, err := store.CreateAccount(ctx, "owner-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	// A Free organization starts with billing adopted and keeps it when it
	// upgrades (ADR-202610171904).
	org, err := store.CreateOrg(ctx, state.Org{Slug: "tenant-keys-" + suffix, Name: "Tenant Keys Co", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if adopt != nil {
		adopt(org.ID)
	}
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	f := tenantKeySubjectFixture{store: store, orgID: org.ID, suffix: suffix, limit: api.PlanHobby.ConsumerKeysPerAccount()}
	f.members = f.consumer(t, owner.ID, org.ID, "members")
	for i := range f.limit - 1 {
		_, prefix, hash, err := api.GenerateConsumerKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateConsumerKeyForConsumer(ctx, owner.ID, f.members.ID, fmt.Sprintf("fill-%d", i), prefix, hash, []string{"read"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f tenantKeySubjectFixture) consumer(t *testing.T, accountID, orgID, slug string) state.APIConsumer {
	t.Helper()
	app, err := f.store.CreateApp(t.Context(), state.App{AccountID: accountID, OrgID: orgID, Slug: slug + "-" + f.suffix, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.store.CreateAPIConsumer(t.Context(), accountID, app.ID, slug, slug)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// owner adds a company member, on the Pro plan, who runs a platform tenant.
func (f tenantKeySubjectFixture) owner(t *testing.T, name string) tenantKeyOwner {
	t.Helper()
	ctx := t.Context()
	account, err := f.store.CreateAccount(ctx, name+"-"+f.suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddOrgMember(ctx, f.orgID, account.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	o := tenantKeyOwner{account: account, personal: f.consumer(t, account.ID, "", name+"-personal"),
		company: f.consumer(t, account.ID, f.orgID, name+"-company")}
	tenants := f.store.(credentialFixtureStore)
	tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, "tenant", "Tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	o.tenantID = tenant.ID
	for _, c := range []state.APIConsumer{o.personal, o.company} {
		if _, err := tenants.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

// apply issues o's tenant one key on consumerID, revoking revokeIDs in the
// same bundle.
func (f tenantKeySubjectFixture) apply(ctx context.Context, o tenantKeyOwner, consumerID, name string, revokeIDs ...string) (state.ApplyPlatformTenantCredentialsResult, error) {
	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		return state.ApplyPlatformTenantCredentialsResult{}, err
	}
	return f.store.(state.PlatformTenantCredentialStore).ApplyPlatformTenantCredentials(ctx, state.ApplyPlatformTenantCredentialsParams{
		AccountID: o.account.ID, TenantID: o.tenantID, RevokeKeyIDs: revokeIDs,
		Keys: []state.PlatformTenantCredentialIntent{{ConsumerID: consumerID, Name: name, Prefix: prefix, Hash: hash, Scopes: []string{"read"}}}})
}

// A platform tenant's keys spend the allowance of each key's app's billing
// subject (ADR-202610171904), as keys issued on the app's own consumer routes
// do. Once a company adopted billing, a tenant key on a member's company app
// counts toward the company, at the company's plan, alongside every other
// member's keys, and never toward the member's personal allowance.
func platformTenantKeySubjectContract(t *testing.T, store state.Store, adopt func(orgID string)) {
	t.Helper()
	f := newTenantKeySubjectFixture(t, store, adopt)
	o := f.owner(t, "creator")
	ctx := t.Context()
	if _, err := f.apply(ctx, o, o.personal.ID, "personal"); err != nil {
		t.Fatalf("a tenant key on the member's personal app: %v", err)
	}
	last, err := f.apply(ctx, o, o.company.ID, "last")
	if err != nil {
		t.Fatalf("the company's last key, issued to the tenant: %v", err)
	}
	_, err = f.apply(ctx, o, o.company.ID, "over")
	var quota *state.PlatformTenantCredentialQuotaError
	if !errors.As(err, &quota) || quota.Scope != api.PlanQuotaScopeAccount || quota.Plan != api.PlanHobby ||
		quota.Limit != f.limit || quota.Observed != f.limit {
		t.Fatalf("a tenant key past the company's allowance: %v (%+v), want the company's %s account quota of %d", err, quota, api.PlanHobby, f.limit)
	}
	if _, err := f.apply(ctx, o, o.company.ID, "rotated", last.Keys[0].Key.ID); err != nil {
		t.Fatalf("rotating the tenant's company key at the company's allowance: %v", err)
	}
}

// raceGate holds the tenants' rows, which a tenant's issue locks last, so
// the racing issues queue behind it; open waits until they have, then
// releases them together. It is nil where issues cannot overlap.
type raceGate func(t *testing.T, tenantIDs []string, racers int) (open func())

// Several members' tenants spend the company's one allowance, so issuing keys
// concurrently never takes the company past it, and the company's own
// consumer routes then find it spent.
func platformTenantKeySubjectRaceContract(t *testing.T, store state.Store, adopt func(orgID string), gate raceGate) {
	t.Helper()
	f := newTenantKeySubjectFixture(t, store, adopt)
	owners := make([]tenantKeyOwner, 4)
	tenantIDs := make([]string, len(owners))
	for i := range owners {
		owners[i] = f.owner(t, fmt.Sprintf("member-%d", i))
		tenantIDs[i] = owners[i].tenantID
	}
	const rounds = 3
	open := func() {}
	if gate != nil {
		open = gate(t, tenantIDs, rounds*len(owners))
	}
	ctx := t.Context()
	var (
		mu       sync.Mutex
		issued   int
		failures []error
		wg       sync.WaitGroup
	)
	for i := range rounds {
		for _, o := range owners {
			wg.Go(func() {
				_, err := f.apply(ctx, o, o.company.ID, fmt.Sprintf("tenant-%d", i))
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					issued++
				case !errors.As(err, new(*state.PlatformTenantCredentialQuotaError)):
					failures = append(failures, err)
				}
			})
		}
	}
	open()
	wg.Wait()
	if len(failures) > 0 {
		t.Fatalf("concurrent issues failed: %v", failures)
	}
	if issued != 1 {
		t.Fatalf("%d concurrent keys were issued into the company's last slot, want 1", issued)
	}
	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateConsumerKeyForConsumerIfUnderQuota(ctx, f.members.AccountID, f.members.ID, "route",
		prefix, hash, []string{"read"}, nil, state.ConsumerKeyQuota{PerApp: f.limit, PerAccount: f.limit})
	var quota *state.ConsumerKeyQuotaError
	if !errors.As(err, &quota) || quota.Scope != api.PlanQuotaScopeAccount {
		t.Fatalf("a consumer route key after the tenants spent the company's allowance: %v, want the account quota", err)
	}
}

// adr: 202610171904
func TestMemPlatformTenantKeysSpendTheBillingSubjectsAllowance(t *testing.T) {
	platformTenantKeySubjectContract(t, state.NewMemStore(), nil)
}

// MemStore issues under one mutex, so its issues never overlap.
func TestMemPlatformTenantKeysAndMemberKeysShareOneAllowance(t *testing.T) {
	platformTenantKeySubjectRaceContract(t, state.NewMemStore(), nil, nil)
}
