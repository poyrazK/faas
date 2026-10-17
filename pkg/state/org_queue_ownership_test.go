package state_test

// adr: 202610171908

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type companyQueueStore interface {
	state.Store
	state.QueueBindingConsumerStore
	state.QueueLeaseStore
	state.OrgQueueUsageStore
}

func companyQueueApp(t *testing.T, f offboardingFixture, slug, project string) state.App {
	t.Helper()
	app, err := f.store.CreateApp(t.Context(), state.App{AccountID: f.actor.ID, OrgID: f.org.ID, ProjectID: project,
		Slug: slug, WorkloadName: slug, Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker,
		Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func companyQueueBinding(t *testing.T, store companyQueueStore, app state.App, mode string) state.QueueBinding {
	t.Helper()
	result, err := store.CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: app.AccountID, AppID: app.ID,
		Name: "jobs", QueueName: "jobs", Mode: mode, WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding.OrgID != app.OrgID {
		t.Fatalf("binding lost organization: %+v", result.Binding)
	}
	return result.Binding
}

func enqueueCompanyQueue(t *testing.T, store state.Store, app state.App, binding string) state.Invocation {
	t.Helper()
	inv, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: app.AccountID, AppID: app.ID, Source: state.InvocationQueue,
		QueueName: "jobs", QueueBindingID: binding, DueAt: time.Now().Add(-time.Second), Payload: json.RawMessage(`{"retained":"company"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestMemOrgQueueOwnership(t *testing.T) { orgQueueOwnership(t, state.NewMemStore()) }

func orgQueueOwnership(t *testing.T, base state.Store) {
	t.Helper()
	f := setupOffboarding(t, base)
	adoptObjectCompany(t, f)
	store := base.(companyQueueStore)
	pull := companyQueueApp(t, f, "company-pull", "")
	push := companyQueueApp(t, f, "company-push", "")
	binding := companyQueueBinding(t, store, pull, "pull")
	pushBinding := companyQueueBinding(t, store, push, "push")
	personal, err := base.CreateApp(t.Context(), state.App{AccountID: f.actor.ID, Slug: "private-pull", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	privateBinding := companyQueueBinding(t, store, personal, "pull")
	privateMessage := enqueueCompanyQueue(t, base, personal, privateBinding.ID)
	held := enqueueCompanyQueue(t, base, pull, binding.ID)
	pending := enqueueCompanyQueue(t, base, pull, binding.ID)
	leases, err := store.LeaseQueueMessages(t.Context(), state.QueueLeaseRequest{AccountID: pull.AccountID, AppID: pull.ID, QueueName: "jobs", MaxMessages: 1})
	if err != nil || len(leases) != 1 || leases[0].Invocation.ID != held.ID {
		t.Fatalf("initial lease: %+v %v", leases, err)
	}
	inflight := enqueueCompanyQueue(t, base, push, pushBinding.ID)
	claimed, err := base.ClaimInvocationWithCap(t.Context(), inflight.ID, "", 30, 1)
	if err != nil || claimed.AsyncQuotaSubjectID != f.org.ID {
		t.Fatalf("company claim: %+v %v", claimed, err)
	}
	dead := enqueueCompanyQueue(t, base, push, pushBinding.ID)
	if _, err := base.ClaimInvocationWithCap(t.Context(), dead.ID, "", 30, 1); err != nil {
		t.Fatal(err)
	}
	if err := base.FailInvocation(t.Context(), dead.ID, "retained failure", time.Nanosecond, 1); err != nil {
		t.Fatal(err)
	}
	var stale context.Context
	snapshot, err := base.OrgAccessSnapshot(t.Context(), f.org.ID, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := base.SoftDeleteOrg(t.Context(), f.org.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("orphaned queue on org deletion: %v", err)
	}
	if err := base.RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func() error{
		func() error { _, e := store.ListQueueBindingsForApp(stale, pull.AccountID, pull.ID); return e },
		func() error {
			_, e := store.LeaseQueueMessages(stale, state.QueueLeaseRequest{AccountID: pull.AccountID, AppID: pull.ID, QueueName: "jobs"})
			return e
		},
		func() error {
			_, e := store.AckQueueLeases(stale, pull.AccountID, pull.ID, "jobs", []string{leases[0].Receipt})
			return e
		},
		func() error {
			_, e := store.NackQueueLeases(stale, pull.AccountID, pull.ID, "jobs", []string{leases[0].Receipt}, 0, "revoked")
			return e
		},
		func() error {
			_, e := store.ExtendQueueLeases(stale, pull.AccountID, pull.ID, "jobs", []string{leases[0].Receipt}, 60)
			return e
		},
		func() error {
			_, e := store.DeleteQueueBindingWithConsumer(stale, push.AccountID, push.ID, pushBinding.ID)
			return e
		},
		func() error { return base.CompleteInvocation(stale, inflight.ID, nil) },
	} {
		ctx, cancel := state.WithOrgAccessFence(t.Context(), state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: snapshot.Revision})
		stale = ctx
		err := mutate()
		cancel(nil)
		if !errors.Is(err, state.ErrOrgAccessRevoked) {
			t.Fatalf("stale queue access: %v", err)
		}
	}
	if err := base.MarkAccountDeletionPending(t.Context(), f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := base.DeleteAccount(t.Context(), f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := base.AccountByID(t.Context(), f.actor.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("identity retained: %v", err)
	}
	if _, err := store.QueueBindingByID(t.Context(), personal.AccountID, personal.ID, privateBinding.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("personal binding survived erasure: %v", err)
	}
	if _, err := base.InvocationByID(t.Context(), privateMessage.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("personal message survived erasure: %v", err)
	}
	ledger, err := base.ListDeadLetterEvents(t.Context(), push.ID, 10, "")
	if err != nil || len(ledger) != 1 || ledger[0].SourceID != dead.ID {
		t.Fatalf("failure history lost: %+v %v", ledger, err)
	}
	replayed, err := base.RetryQueueDeadLetter(t.Context(), push.AccountID, dead.ID)
	if err != nil || replayed.ID != dead.ID || replayed.ReplayGeneration != 1 || string(replayed.Payload) != string(dead.Payload) {
		t.Fatalf("replay after erasure: %+v %v", replayed, err)
	}
	saved, err := store.QueueBindingByID(t.Context(), pull.AccountID, pull.ID, binding.ID)
	if err != nil || saved.ID != binding.ID || saved.AccountID != f.actor.ID || saved.OrgID != f.org.ID {
		t.Fatalf("binding changed: %+v %v", saved, err)
	}
	if out, err := store.AckQueueLeases(t.Context(), pull.AccountID, pull.ID, "jobs", []string{leases[0].Receipt}); err != nil || len(out.Succeeded) != 1 {
		t.Fatalf("retained receipt: %+v %v", out, err)
	}
	leases, err = store.LeaseQueueMessages(t.Context(), state.QueueLeaseRequest{AccountID: pull.AccountID, AppID: pull.ID, QueueName: "jobs"})
	if err != nil || len(leases) != 1 || leases[0].Invocation.ID != pending.ID {
		t.Fatalf("backlog lost: %+v %v", leases, err)
	}
	if err := base.CompleteInvocation(t.Context(), inflight.ID, json.RawMessage(`{"done":true}`)); err != nil {
		t.Fatal(err)
	}
	_, cur, err := base.GetAccountAsyncQuota(t.Context(), f.org.ID)
	if err != nil || cur != 0 {
		t.Fatalf("reservation leak after erasure: %d %v", cur, err)
	}
	usage, err := store.OrgQueueUsage(t.Context(), f.org.ID)
	if err != nil || usage.Bindings != 2 || usage.Processing != 1 || usage.Pending != 1 || usage.ReservedAsync != 0 {
		t.Fatalf("company usage: %+v %v", usage, err)
	}
}

func TestMemOrgQueueAdoption(t *testing.T) { orgQueueAdoption(t, state.NewMemStore()) }

func orgQueueAdoption(t *testing.T, base state.Store) {
	f := setupOffboarding(t, base)
	app := companyQueueApp(t, f, "adopted-queue", "")
	personal, err := base.CreateApp(t.Context(), state.App{AccountID: f.actor.ID, Slug: "personal-queue"})
	if err != nil {
		t.Fatal(err)
	}
	company := enqueueCompanyQueue(t, base, app, "")
	private := enqueueCompanyQueue(t, base, personal, "")
	for _, inv := range []state.Invocation{company, private} {
		if _, err := base.ClaimInvocationWithCap(t.Context(), inv.ID, "", 30, 3); err != nil {
			t.Fatal(err)
		}
	}
	adoptObjectCompany(t, f)
	for _, tc := range []struct {
		id   string
		want int
	}{{f.actor.ID, 1}, {f.org.ID, 1}} {
		_, cur, err := base.GetAccountAsyncQuota(t.Context(), tc.id)
		if err != nil || cur != tc.want {
			t.Fatalf("adoption counter %s: %d %v", tc.id, cur, err)
		}
	}
	got, err := base.InvocationByID(t.Context(), company.ID)
	if err != nil || got.AsyncQuotaSubjectID != f.org.ID {
		t.Fatalf("adoption snapshot: %+v %v", got, err)
	}
	if err := base.FailInvocation(t.Context(), company.ID, "poison", time.Nanosecond, 1); err != nil {
		t.Fatal(err)
	}
	if _, cur, err := base.GetAccountAsyncQuota(t.Context(), f.org.ID); err != nil || cur != 0 {
		t.Fatalf("adopted retry leaked %d %v", cur, err)
	}
	if _, cur, err := base.GetAccountAsyncQuota(t.Context(), f.actor.ID); err != nil || cur != 1 {
		t.Fatalf("released personal reservation %d %v", cur, err)
	}
	if _, err := base.RetryQueueDeadLetter(t.Context(), app.AccountID, company.ID); err != nil {
		t.Fatal(err)
	}
	if err := base.UpdateOrgStatus(t.Context(), f.org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := base.ClaimInvocationWithCap(t.Context(), company.ID, "", 30, 999999); err == nil {
		t.Fatal("suspended company claimed work")
	}
	if err := base.UpdateOrgStatus(t.Context(), f.org.ID, state.OrgStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := base.ClaimInvocationWithCap(t.Context(), company.ID, "", 30, 0); err != nil {
		t.Fatalf("caller cap replaced company policy: %v", err)
	}
	if err := base.CompleteInvocation(t.Context(), company.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := base.CompleteInvocation(t.Context(), private.ID, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMemOrgStageQueueOwnership(t *testing.T) { orgStageQueueOwnership(t, state.NewMemStore()) }

func TestMemOrgQueueCompanyCap(t *testing.T) { orgQueueCompanyCap(t, state.NewMemStore()) }

func TestMemOrgQueueTriggerQuota(t *testing.T) { orgQueueTriggerQuota(t, state.NewMemStore()) }

func orgQueueTriggerQuota(t *testing.T, base state.Store) {
	f := setupOffboarding(t, base)
	adoptObjectCompany(t, f)
	store := base.(companyQueueStore)
	limits := api.MustLimitsFor(api.PlanHobby)
	var apps []state.App
	namespaces := []string{f.actor.ID, f.owner.ID, f.org.ID}
	for i := 0; i < (limits.TriggerLimitPerAccount+limits.TriggerLimitPerApp-1)/limits.TriggerLimitPerApp+1; i++ {
		namespace := namespaces[i%len(namespaces)]
		app, err := base.CreateApp(t.Context(), state.App{AccountID: namespace, OrgID: f.org.ID, Slug: fmt.Sprintf("trigger-owner-%d", i), Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
	}
	if err := base.UpdateOrgPlan(t.Context(), f.org.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	limit := limits.TriggerLimitPerAccount
	create := func(app state.App, index int) error {
		name := fmt.Sprintf("jobs-%d", index)
		_, err := store.CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: app.AccountID, AppID: app.ID, Name: name, QueueName: name, Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: false, MaxConcurrency: 1})
		return err
	}
	for i := 0; i < limit-1; i++ {
		if err := create(apps[i%len(apps)], i); err != nil {
			t.Fatal(err)
		}
	}
	var group sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Go(func() { <-start; results <- create(apps[len(apps)-1-i], limit+i) })
	}
	close(start)
	group.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
			continue
		}
		var quota *state.TriggerQuotaError
		if !errors.As(err, &quota) || quota.Scope != state.TriggerQuotaScopeAccount {
			t.Fatal(err)
		}
		rejected++
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("shared trigger boundary: accepted %d rejected %d", accepted, rejected)
	}
	private, err := base.CreateApp(t.Context(), state.App{AccountID: f.actor.ID, Slug: "personal-trigger-cap", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	if err := create(private, limit+3); err != nil {
		t.Fatalf("company triggers consumed personal quota: %v", err)
	}
}

func orgQueueCompanyCap(t *testing.T, base state.Store) {
	f := setupOffboarding(t, base)
	adoptObjectCompany(t, f)
	if err := base.UpdateOrgPlan(t.Context(), f.org.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	app := companyQueueApp(t, f, "creator-queue-cap", "")
	other, err := base.CreateApp(t.Context(), state.App{AccountID: f.org.ID, OrgID: f.org.ID, Slug: "org-queue-cap"})
	if err != nil {
		t.Fatal(err)
	}
	cap := api.MustLimitsFor(api.PlanFree).MaxAsyncInvocationsPerAccount
	var claimed []state.Invocation
	for i := 0; i < cap; i++ {
		target := app
		if i%2 == 0 {
			target = other
		}
		inv := enqueueCompanyQueue(t, base, target, "")
		got, err := base.ClaimInvocationWithCap(t.Context(), inv.ID, "", 30, 0)
		if err != nil || got.AsyncQuotaSubjectID != f.org.ID {
			t.Fatalf("shared reservation %d: %+v %v", i, got, err)
		}
		claimed = append(claimed, got)
	}
	// An obsolete worker's cached cap cannot override current org policy.
	if _, _, err := base.EnsureAccountAsyncQuota(t.Context(), f.org.ID, cap*2); err != nil {
		t.Fatal(err)
	}
	inv := enqueueCompanyQueue(t, base, app, "")
	if _, err := base.ClaimInvocationWithCap(t.Context(), inv.ID, "", 30, cap*2); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("company cap bypass: %v", err)
	}
	for _, inv := range claimed {
		if err := base.CompleteInvocation(t.Context(), inv.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, cur, err := base.GetAccountAsyncQuota(t.Context(), f.org.ID); err != nil || cur != 0 {
		t.Fatalf("company counter leaked: %d %v", cur, err)
	}
}

func TestMemOrgQueueExternalTriggerGuard(t *testing.T) {
	orgQueueExternalTriggerGuard(t, state.NewMemStore())
}

func orgQueueExternalTriggerGuard(t *testing.T, base state.Store) {
	f := setupOffboarding(t, base)
	adoptObjectCompany(t, f)
	app := companyQueueApp(t, f, "legacy-company-trigger", "")
	if _, err := base.CreateTriggerIfUnderQuota(t.Context(), app.ID, "queue", "legacy", true, []byte(`{}`), "queue", 1, 10, 3, 1024, "commit", api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	if err := base.MarkAccountDeletionPending(t.Context(), f.actor.ID); err != nil {
		if !errors.Is(err, state.ErrOrgAppOwnershipPending) {
			t.Fatal(err)
		}
		return
	}
	if err := base.DeleteAccount(t.Context(), f.actor.ID); !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrOrgAppOwnershipPending) {
		t.Fatalf("unreviewed trigger lost ownership: %v", err)
	}
}

func orgStageQueueOwnership(t *testing.T, base state.Store) {
	f := setupOffboarding(t, base)
	adoptObjectCompany(t, f)
	store := base.(environmentQueueDeliveryTestStore)
	project, err := base.CreateProject(t.Context(), state.Project{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: "company-stage"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.actor.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	app := companyQueueApp(t, f, "company-stage-worker", project.ID)
	spec, err := state.ReplaceEnvironmentQueueBindings(t.Context(), store, app, "stage", 0, []state.ProjectEnvironmentQueueDefinition{{Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 2, RetryPolicyJSON: []byte(`{"max_attempts":3,"base_seconds":0.000001}`)}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := base.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := base.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	set, err := store.PrepareProjectEnvironmentQueueConsumers(t.Context(), app.AccountID, project.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture := queueConsumerFixture{account: f.actor, project: project, app: app, spec: spec, dep: dep}
	inv := enqueueStageQueue(t.Context(), t, store, fixture)
	req := state.ProjectEnvironmentQueueDeliveryRequest{ProjectEnvironmentQueueDeliveryScope: state.ProjectEnvironmentQueueDeliveryScope{AccountID: app.AccountID, ProjectID: project.ID, DeploymentID: dep.ID, BindingName: "orders"}, Mode: "pull", LeaseSeconds: 30}
	claim, err := store.ClaimNextProjectEnvironmentQueueDelivery(t.Context(), req)
	if err != nil || claim.Invocation.ID != inv.ID || claim.Invocation.AsyncQuotaSubjectID != f.org.ID {
		t.Fatalf("stage claim: %+v %v", claim, err)
	}
	if err := base.RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := base.MarkAccountDeletionPending(t.Context(), f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := base.DeleteAccount(t.Context(), f.actor.ID); err != nil {
		t.Fatal(err)
	}
	current, err := store.ProjectEnvironmentQueueConsumersForDeployment(t.Context(), app.AccountID, project.ID, dep.ID)
	if err != nil || current.ID != set.ID || current.Consumers[0].ID != set.Consumers[0].ID {
		t.Fatalf("stage runtime identity changed: %+v %v", current, err)
	}
	if err := store.RetryProjectEnvironmentQueueDelivery(t.Context(), req.ProjectEnvironmentQueueDeliveryScope, inv.ID, claim.Receipt, "retry after erasure"); err != nil {
		t.Fatal(err)
	}
	claim, err = store.ClaimNextProjectEnvironmentQueueDelivery(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteProjectEnvironmentQueueDelivery(t.Context(), req.ProjectEnvironmentQueueDeliveryScope, inv.ID, claim.Receipt, nil); err != nil {
		t.Fatal(err)
	}
	enqueueStageQueue(t.Context(), t, store, fixture)
	if _, cur, err := base.GetAccountAsyncQuota(t.Context(), f.org.ID); err != nil || cur != 0 {
		t.Fatalf("stage leaked quota: %d %v", cur, err)
	}
}
