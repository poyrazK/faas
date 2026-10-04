package globaldirectory

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type quotaFixture struct {
	store     Store
	quota     RuntimeQuotaStore
	authority state.Store
	owner     Owner
	actor     state.Account
	orgID     string
	request   RuntimeQuotaRequest
}

func seedQuotaFixture(t *testing.T, s Store, authority state.Store, actor state.Account, shared bool, regions int) quotaFixture {
	t.Helper()
	if err := authority.MarkAccountEmailVerified(t.Context(), actor.ID); err != nil {
		t.Fatal(err)
	}
	f := quotaFixture{store: s, quota: s.(RuntimeQuotaStore), authority: authority, actor: actor, owner: Owner{ID: actor.ID, AccountID: actor.ID}}
	if shared {
		org, err := authority.CreateOrg(t.Context(), state.Org{Slug: "quota-team", Name: "Quota Team", Plan: api.PlanPro})
		if err != nil {
			t.Fatal(err)
		}
		if err = authority.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
			t.Fatal(err)
		}
		f.orgID = org.ID
		f.owner, err = s.(OwnerStore).ResolveOrgOwner(t.Context(), org.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.(OwnerStore).CreateOrgApp(t.Context(), f.owner.ID, actor.ID, org.Plan, "quota-global", api.MustLimitsFor(org.Plan).DeployedApps); err != nil {
			t.Fatal(err)
		}
	} else if _, err := s.CreateApp(t.Context(), actor.ID, "quota-global", api.MustLimitsFor(actor.Plan).DeployedApps); err != nil {
		t.Fatal(err)
	}
	f.request = RuntimeQuotaRequest{OwnerID: f.owner.ID, ActorID: actor.ID, Slug: "quota-global", AllocationID: uuid.NewString(), ExpectedPlan: api.PlanPro}
	for _, region := range []string{"eu-fsn", "eu-hel"}[:regions] {
		cell, err := s.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: region, APIBase: "https://" + region + ".example.com", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		b := Binding{CellID: cell.ID, Region: region, CellGeneration: cell.Generation, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "quota-regional"}
		if shared {
			b.RegionalOrgID = uuid.NewString()
			_, err = s.(OrgTargetStore).AttachOrgTarget(t.Context(), f.owner.ID, actor.ID, f.request.Slug, b)
		} else {
			_, err = s.AttachTarget(t.Context(), actor.ID, f.request.Slug, b)
		}
		if err != nil {
			t.Fatal(err)
		}
		f.request.Shares = append(f.request.Shares, RuntimeQuotaShare{Region: region, Concurrency: 1})
	}
	return f
}

func memoryQuotaFixture(t *testing.T, shared bool, regions int) quotaFixture {
	t.Helper()
	a := state.NewMemStore()
	actor, err := a.CreateAccount(t.Context(), "quota-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	return seedQuotaFixture(t, NewMemoryStoreWithOrgAuthority(a), a, actor, shared, regions)
}

func assertQuotaConservation(t *testing.T, f quotaFixture, ids []string) {
	t.Helper()
	p, err := runtimeQuotaPolicy(f.request.ExpectedPlan)
	if err != nil {
		t.Fatal(err)
	}
	steady, peak := 0, 0
	for _, id := range ids {
		a, err := f.quota.RuntimeQuota(t.Context(), f.owner.ID, f.request.Slug, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range a.Targets {
			if target.ReleasedAt == nil {
				steady += target.Concurrency
				peak += target.Concurrency + target.RolloutConcurrency
			}
		}
	}
	if steady > p.Concurrency || peak > p.PeakConcurrency {
		t.Fatalf("quota exceeded: steady=%d/%d peak=%d/%d", steady, p.Concurrency, peak, p.PeakConcurrency)
	}
}

func exerciseRuntimeQuotaContract(t *testing.T, f quotaFixture) {
	t.Helper()
	req := f.request
	req.Shares = append([]RuntimeQuotaShare(nil), f.request.Shares...)
	for i := range req.Shares {
		req.Shares[i].Concurrency = 2
	}
	first, err := f.quota.ReserveRuntimeQuota(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 || len(first.Targets) != 2 || first.Policy.Plan != api.PlanPro {
		t.Fatalf("reservation: %+v", first)
	}
	reversed := req
	reversed.Shares = append([]RuntimeQuotaShare(nil), req.Shares...)
	reversed.Shares[0], reversed.Shares[1] = reversed.Shares[1], reversed.Shares[0]
	if retry, err := f.quota.ReserveRuntimeQuota(t.Context(), reversed); err != nil || retry.Generation != first.Generation {
		t.Fatalf("order-dependent retry: %+v %v", retry, err)
	}
	changed := req
	changed.Shares = append([]RuntimeQuotaShare(nil), req.Shares...)
	changed.Shares[0].Concurrency = 1
	if _, err = f.quota.ReserveRuntimeQuota(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("quota UUID input changed: %v", err)
	}
	if _, err = f.quota.RuntimeQuota(t.Context(), uuid.NewString(), req.Slug, req.AllocationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign quota visible: %v", err)
	}
	issued, err := f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, req.Slug, req.AllocationID, req.Shares[0].Region)
	if err != nil || issued.IssuedAt == nil {
		t.Fatalf("issue: %+v %v", issued, err)
	}
	original := *issued.IssuedAt
	*issued.IssuedAt = original.Add(-time.Hour)
	if again, err := f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, req.Slug, req.AllocationID, req.Shares[0].Region); err != nil || !again.IssuedAt.Equal(original) {
		t.Fatalf("issued timestamp rewritten: %+v %v", again, err)
	}
	second := f.request
	second.AllocationID = uuid.NewString()
	if _, err = f.quota.ReserveRuntimeQuota(t.Context(), second); !errors.Is(err, ErrRuntimeQuotaLimit) {
		t.Fatalf("outstanding quota overspent: %v", err)
	}
	retired, err := f.quota.RetireRuntimeQuota(t.Context(), f.owner.ID, f.actor.ID, req.Slug, req.AllocationID)
	if err != nil {
		t.Fatal(err)
	}
	if retired.RetiredAt == nil || retired.RetirementID == "" || retired.Targets[0].ReleasedAt != nil || retired.Targets[1].ReleasedAt == nil {
		t.Fatalf("unsafe retirement: %+v", retired)
	}
	if _, err = f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, req.Slug, req.AllocationID, req.Shares[1].Region); !errors.Is(err, ErrRuntimeQuotaRetired) {
		t.Fatalf("issued after retire: %v", err)
	}
	if tombstone, err := f.quota.ReserveRuntimeQuota(t.Context(), req); err != nil || tombstone.Generation != 1 || tombstone.RetirementID != retired.RetirementID {
		t.Fatalf("tombstone revived: %+v %v", tombstone, err)
	}
	next, err := f.quota.ReserveRuntimeQuota(t.Context(), second)
	if err != nil || next.Generation != 2 {
		t.Fatalf("unissued capacity not released: %+v %v", next, err)
	}
	third := second
	third.AllocationID = uuid.NewString()
	if _, err = f.quota.ReserveRuntimeQuota(t.Context(), third); !errors.Is(err, ErrRuntimeQuotaLimit) {
		t.Fatalf("issued retirement charge missing: %v", err)
	}
	target := retired.Targets[0]
	ack := RuntimeQuotaRetirement{AllocationID: req.AllocationID, RetirementID: retired.RetirementID, Generation: retired.Generation, Binding: target.Binding, APIBase: target.APIBase, AdmissionFrozen: true}
	for _, kind := range []string{"generation", "retirement", "binding", "origin", "not_frozen", "live", "negative_live"} {
		bad := ack
		switch kind {
		case "generation":
			bad.Generation++
		case "retirement":
			bad.RetirementID = uuid.NewString()
		case "binding":
			bad.Binding.RegionalAppID = uuid.NewString()
		case "origin":
			bad.APIBase = "https://other.example.com"
		case "not_frozen":
			bad.AdmissionFrozen = false
		case "live":
			bad.CountedInstances = 1
		case "negative_live":
			bad.CountedInstances = -1
		}
		if err = f.quota.ConfirmRuntimeQuotaRetirement(t.Context(), f.owner.ID, req.Slug, bad); !errors.Is(err, ErrRuntimeQuotaIdentity) {
			t.Fatalf("%s acknowledgement accepted: %v", kind, err)
		}
	}
	// Registry withdrawal never frees authority. The original identity still
	// authorizes only its verified retirement acknowledgement.
	cell, err := f.store.CellByRegion(t.Context(), target.Region)
	if err != nil {
		t.Fatal(err)
	}
	cell.Enabled = false
	if _, err = f.store.PutCell(t.Context(), cell); err != nil {
		t.Fatal(err)
	}
	if err = f.quota.ConfirmRuntimeQuotaRetirement(t.Context(), f.owner.ID, req.Slug, ack); err != nil {
		t.Fatal(err)
	}
	if err = f.quota.ConfirmRuntimeQuotaRetirement(t.Context(), f.owner.ID, req.Slug, ack); err != nil {
		t.Fatalf("duplicate acknowledgement: %v", err)
	}
	cell.Enabled = true
	cell, err = f.store.PutCell(t.Context(), cell)
	if err != nil {
		t.Fatal(err)
	}
	b := target.Binding
	b.CellGeneration = cell.Generation
	if f.orgID != "" {
		_, err = f.store.(OrgTargetStore).AttachOrgTarget(t.Context(), f.owner.ID, f.actor.ID, req.Slug, b)
	} else {
		_, err = f.store.AttachTarget(t.Context(), f.owner.ID, req.Slug, b)
	}
	if err != nil {
		t.Fatal(err)
	}
	thirdAllocation, err := f.quota.ReserveRuntimeQuota(t.Context(), third)
	if err != nil || thirdAllocation.Generation != 3 {
		t.Fatalf("failed admission advanced head or retained charge: %+v %v", thirdAllocation, err)
	}
	if _, err = f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, req.Slug, second.AllocationID, target.Region); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("stale snapshot issued: %v", err)
	}
	assertQuotaConservation(t, f, []string{req.AllocationID, second.AllocationID, third.AllocationID})
}

func exerciseQuotaPeakAndRace(t *testing.T, f quotaFixture) {
	t.Helper()
	p, _ := runtimeQuotaPolicy(f.request.ExpectedPlan)
	expected := p.PeakConcurrency / (1 + api.RolloutConcurrencyGrant)
	var wins atomic.Int32
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ids []string
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := f.request
			req.AllocationID = uuid.NewString()
			a, err := f.quota.ReserveRuntimeQuota(t.Context(), req)
			if err == nil {
				wins.Add(1)
				mu.Lock()
				ids = append(ids, a.AllocationID)
				mu.Unlock()
			} else if !errors.Is(err, ErrRuntimeQuotaLimit) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if int(wins.Load()) != expected {
		t.Fatalf("quota race winners: %d want %d", wins.Load(), expected)
	}
	assertQuotaConservation(t, f, ids)
	req := f.request
	req.AllocationID = uuid.NewString()
	_, err := f.quota.ReserveRuntimeQuota(t.Context(), req)
	var limit *RuntimeQuotaLimitError
	if !errors.As(err, &limit) || limit.Kind != "peak" || limit.Limit != int64(p.PeakConcurrency) {
		t.Fatalf("peak allowance multiplied: %v", err)
	}
}

func exerciseQuotaIssueRetireRace(t *testing.T, f quotaFixture) {
	t.Helper()
	req := f.request
	p, _ := runtimeQuotaPolicy(req.ExpectedPlan)
	req.Shares[0].Concurrency = p.Concurrency
	a, err := f.quota.ReserveRuntimeQuota(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, req.Slug, a.AllocationID, req.Shares[0].Region)
		if err != nil && !errors.Is(err, ErrRuntimeQuotaRetired) {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if _, err := f.quota.RetireRuntimeQuota(t.Context(), f.owner.ID, f.actor.ID, req.Slug, a.AllocationID); err != nil {
			t.Error(err)
		}
	}()
	close(start)
	wg.Wait()
	got, err := f.quota.RuntimeQuota(t.Context(), f.owner.ID, req.Slug, a.AllocationID)
	if err != nil {
		t.Fatal(err)
	}
	target := got.Targets[0]
	if (target.IssuedAt == nil) != (target.ReleasedAt != nil) {
		t.Fatalf("issue/retire conservation: %+v", target)
	}
	newReq := req
	newReq.AllocationID = uuid.NewString()
	_, err = f.quota.ReserveRuntimeQuota(t.Context(), newReq)
	if target.IssuedAt != nil && !errors.Is(err, ErrRuntimeQuotaLimit) {
		t.Fatalf("lost issued charge: %v", err)
	}
	if target.IssuedAt == nil && err != nil {
		t.Fatalf("unissued quota held: %v", err)
	}
}

func TestGlobalRuntimeQuotaMemoryContract(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "personal"
		if shared {
			name = "organization"
		}
		t.Run(name, func(t *testing.T) { exerciseRuntimeQuotaContract(t, memoryQuotaFixture(t, shared, 2)) })
	}
}
func TestGlobalRuntimeQuotaMemoryPeakRace(t *testing.T) {
	exerciseQuotaPeakAndRace(t, memoryQuotaFixture(t, false, 1))
}
func TestGlobalRuntimeQuotaMemoryIssueRetireRace(t *testing.T) {
	exerciseQuotaIssueRetireRace(t, memoryQuotaFixture(t, false, 1))
}

func exerciseQuotaAuthority(t *testing.T, f quotaFixture) {
	t.Helper()
	a, err := f.quota.ReserveRuntimeQuota(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if f.orgID != "" {
		admin, err := f.authority.CreateAccount(t.Context(), "quota-admin@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.authority.MarkAccountEmailVerified(t.Context(), admin.ID); err != nil {
			t.Fatal(err)
		}
		if err = f.authority.AddOrgMember(t.Context(), f.orgID, admin.ID, state.OrgRoleAdmin, nil); err != nil {
			t.Fatal(err)
		}
		req := f.request
		req.ActorID = admin.ID
		if retry, err := f.quota.ReserveRuntimeQuota(t.Context(), req); err != nil || retry.Policy.Plan != api.PlanPro {
			t.Fatalf("actor plan charged: %+v %v", retry, err)
		}
		if err = f.authority.UpdateOrgMemberRole(t.Context(), f.orgID, admin.ID, state.OrgRoleViewer); err != nil {
			t.Fatal(err)
		}
		if _, err = f.quota.ReserveRuntimeQuota(t.Context(), req); !errors.Is(err, ErrOwnerForbidden) {
			t.Fatalf("demoted consent replay: %v", err)
		}
		if _, err = f.quota.RetireRuntimeQuota(t.Context(), f.owner.ID, admin.ID, req.Slug, req.AllocationID); !errors.Is(err, ErrOwnerForbidden) {
			t.Fatalf("demoted retirement: %v", err)
		}
		if err = f.authority.UpdateOrgPlan(t.Context(), f.orgID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
	} else if err = f.authority.UpdateAccountPlan(t.Context(), f.actor.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	if _, err = f.quota.ReserveRuntimeQuota(t.Context(), f.request); !errors.Is(err, ErrOwnerPlanChanged) {
		t.Fatalf("stale tenant plan: %v", err)
	}
	if _, err = f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, f.request.Shares[0].Region); !errors.Is(err, ErrOwnerPlanChanged) {
		t.Fatalf("old-plan authority issued: %v", err)
	}
	if _, err = f.quota.RetireRuntimeQuota(t.Context(), f.owner.ID, f.actor.ID, f.request.Slug, a.AllocationID); err != nil {
		t.Fatalf("cannot retire after downgrade: %v", err)
	}
}
func TestGlobalRuntimeQuotaMemoryAuthority(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "personal"
		if shared {
			name = "organization"
		}
		t.Run(name, func(t *testing.T) { exerciseQuotaAuthority(t, memoryQuotaFixture(t, shared, 1)) })
	}
}
