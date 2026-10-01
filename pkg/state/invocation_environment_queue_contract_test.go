// adr: 375
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentQueueInvocationTestStore interface {
	environmentQueueConsumerTestStore
	state.ProjectEnvironmentQueueInvocationStore
	state.InvocationEnvironmentQueueAdmissionReader
	RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
}

func enqueueStageQueue(t *testing.T, store environmentQueueInvocationTestStore, f queueConsumerFixture) state.Invocation {
	t.Helper()
	inv, err := store.EnqueueProjectEnvironmentQueueInvocation(t.Context(), f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{Method: "POST", Path: "/orders", Payload: []byte(`{"order":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestMemEnvironmentQueueAdmissionIsOwnedPinnedAndPrivate(t *testing.T) {
	testEnvironmentQueueAdmission(t, state.NewMemStore())
}
func testEnvironmentQueueAdmission(t *testing.T, store environmentQueueInvocationTestStore) {
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{}); !errors.Is(err, state.ErrProjectEnvironmentQueuePreparationUnavailable) {
		t.Fatalf("unprepared admission: %v", err)
	}
	set, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"retry", "missing"} {
		if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, name, state.Invocation{}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("disabled/missing binding %s: %v", name, err)
		}
	}
	for _, input := range []state.Invocation{
		{AppID: uuid.NewString()}, {AccountID: uuid.NewString()}, {EnvironmentID: uuid.NewString()}, {QueueName: "other"}, {Source: state.InvocationCron},
		{State: state.InvocationDispatching}, {Attempts: 1}, {QuotaReserved: true}, {CreatedAt: time.Now()}, {AckURL: "https://example.test"},
		{WorkPolicyName: "keyed"}, {WorkKeyDigest: []byte{1}}, {OnSuccessDestinationID: uuid.NewString()}, {Payload: []byte(`{`)},
		{RetryPolicyJSON: []byte(`{"max_attempts":9}`)}, {Headers: []byte(`{"X-Gregale-Revision":"bogus"}`)},
	} {
		if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", input); err == nil {
			t.Fatalf("injected input admitted: %+v", input)
		}
	}
	inv := enqueueStageQueue(t, store, f)
	owner, err := store.InvocationEnvironmentQueueAdmission(ctx, inv.ID)
	if err != nil || owner.EnvironmentID != set.EnvironmentID || owner.RuntimeSetID != set.ID || owner.ConsumerID != set.Consumers[0].ID || owner.WorkloadSpecID != f.spec.ID || owner.SettingsHash != f.spec.Hash ||
		owner.DefinitionHash != set.Consumers[0].DefinitionHash || owner.DeploymentID != f.dep.ID || inv.EnvironmentID != set.EnvironmentID || !owner.AdmittedAt.Equal(inv.CreatedAt) || inv.Source != state.InvocationQueue || inv.QueueName != "orders" {
		t.Fatalf("ownership: %+v %+v %v", inv, owner, err)
	}
	resolved, version, err := state.ResolveInvocationVersion(ctx, store, inv)
	if err != nil || version.DeploymentID != f.dep.ID || version.Scope != "stage" || resolved.EnvironmentID != inv.EnvironmentID {
		t.Fatalf("pinned delivery: %+v %v", version, err)
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{ID: inv.ID}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("fresh-only invocation ID: %v", err)
	}
	production, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{ID: uuid.MustParse(production.ID).String()}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("production UUID spelling reused by stage admission: %v", err)
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, uuid.NewString(), f.project.ID, f.dep.ID, "orders", state.Invocation{}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account: %v", err)
	}
	if _, err := store.EnqueueInvocation(ctx, inv); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
		t.Fatalf("generic producer bypass: %v", err)
	}
	due, err := store.ListDueInvocations(ctx, time.Now().Add(time.Second), 64)
	if err != nil || len(due) != 0 {
		t.Fatalf("generic drain exposed unactivated consumers: %+v %v", due, err)
	}
	if _, err := store.ClaimInvocation(ctx, inv.ID, "", 30); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("bare claim bypass: %v", err)
	}
	unchanged, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || unchanged.State != state.InvocationPending || unchanged.Attempts != 0 || unchanged.QuotaReserved {
		t.Fatalf("rejected claim mutated row: %+v %v", unchanged, err)
	}
	for _, change := range []func(*state.Invocation){
		func(i *state.Invocation) { i.Source = state.InvocationAsyncInvoke; i.QueueName = "" },
		func(i *state.Invocation) { i.EnvironmentID = "" }, func(i *state.Invocation) { i.QueueName = "retry" },
		func(i *state.Invocation) { i.RetryPolicyJSON = []byte(`{"max_attempts":9}`) }, func(i *state.Invocation) { i.CreatedAt = i.CreatedAt.Add(time.Second) },
		func(i *state.Invocation) { i.Headers = []byte(`{}`) }, func(i *state.Invocation) { i.WorkPolicyName = "keyed" },
	} {
		bad := inv
		change(&bad)
		if _, _, err := state.ResolveInvocationVersion(ctx, store, bad); err == nil {
			t.Fatalf("delivery accepted altered ownership: %+v", bad)
		}
	}
	bindings := append([]state.ProjectEnvironmentQueueDefinition(nil), f.spec.Settings.QueueBindings.Bindings...)
	bindings[0].RetryPolicyJSON = []byte(`{"max_attempts":8}`)
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "stage", f.spec.Revision, bindings); err != nil {
		t.Fatal(err)
	}
	next := enqueueStageQueue(t, store, f)
	var policy api.RetryPolicyDTO
	if json.Unmarshal(next.RetryPolicyJSON, &policy) != nil || policy.MaxAttempts != 4 {
		t.Fatalf("desired edit rewrote pinned retry: %s", next.RetryPolicyJSON)
	}
	release, err := store.PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "stage", 1800, []state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: f.dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	headers, _ := json.Marshal(map[string]string{api.ReleaseHeader: release.ID})
	fromRelease, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{Headers: headers})
	if err != nil {
		t.Fatal(err)
	}
	_, version, err = state.ResolveInvocationVersion(ctx, store, fromRelease)
	if err != nil || version.ReleaseID != release.ID || version.DeploymentID != f.dep.ID {
		t.Fatalf("release-owned queue: %+v %v", version, err)
	}
	alternative, err := store.PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "stage", 1800, []state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: f.dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	changedRelease := fromRelease
	changedRelease.Headers, _ = json.Marshal(map[string]string{api.ReleaseHeader: alternative.ID})
	if _, _, err := state.ResolveInvocationVersion(ctx, store, changedRelease); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
		t.Fatalf("different release graph with same deployment bypassed frozen admission pin: %v", err)
	}
	next.Payload[0] = '!'
	fresh, err := store.InvocationByID(ctx, next.ID)
	if err != nil || !environmentQueueJSONEqual(fresh.Payload, []byte(`{"order":1}`)) {
		t.Fatalf("returned payload aliases storage: %s %v", fresh.Payload, err)
	}
	if got, err := store.QueueBindingByID(ctx, f.account.ID, f.app.ID, f.prod.ID); err != nil || !reflect.DeepEqual(got, f.prod) {
		t.Fatalf("production binding changed: %+v %v", got, err)
	}
}

func TestMemEnvironmentQueueClaimsShareConcurrencyAndQuota(t *testing.T) {
	testEnvironmentQueueClaims(t, state.NewMemStore())
}
func testEnvironmentQueueClaims(t *testing.T, store environmentQueueInvocationTestStore) {
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	var rows []state.Invocation
	for range 8 {
		rows = append(rows, enqueueStageQueue(t, store, f))
	}
	var won atomic.Int32
	var wg sync.WaitGroup
	for _, inv := range rows {
		wg.Go(func() {
			got, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", -1, 4)
			if err == nil {
				won.Add(1)
				if got.EnvironmentID != f.spec.EnvironmentID || !got.QuotaReserved {
					t.Errorf("claim lost ownership: %+v", got)
				}
			} else if !errors.Is(err, state.ErrQuotaExceeded) {
				t.Errorf("claim: %v", err)
			}
		})
	}
	wg.Wait()
	if won.Load() != 2 {
		t.Fatalf("consumer concurrency raced: %d", won.Load())
	}
	_, cur, err := store.GetAccountAsyncQuota(ctx, f.account.ID)
	if err != nil || cur != 2 {
		t.Fatalf("cap failures leaked quota: %d %v", cur, err)
	}
	pending := enqueueStageQueue(t, store, f)
	if _, err := store.ClaimInvocationWithCap(ctx, pending.ID, "", 30, 4); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("expired unreaped leases freed consumer slots: %v", err)
	}
	n, err := store.RequeueExpiredInvocations(ctx, time.Now(), 64)
	if err != nil || n != 2 {
		t.Fatalf("reap owned queue: %d %v", n, err)
	}
	_, cur, err = store.GetAccountAsyncQuota(ctx, f.account.ID)
	if err != nil || cur != 0 {
		t.Fatalf("reap leaked quota: %d %v", cur, err)
	}
	first, err := store.ClaimInvocationWithCap(ctx, pending.ID, "", 30, 4)
	if err != nil {
		t.Fatal(err)
	}
	// A replacement's fresh consumer ID must share the old generation's domain.
	bindings := append([]state.ProjectEnvironmentQueueDefinition(nil), f.spec.Settings.QueueBindings.Bindings...)
	bindings[0].MaxConcurrency = 1
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "stage", f.spec.Revision, bindings); err != nil {
		t.Fatal(err)
	}
	newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, newer.ID); err != nil {
		t.Fatal(err)
	}
	f.dep = newer
	second := enqueueStageQueue(t, store, f)
	if _, err := store.ClaimInvocationWithCap(ctx, second.ID, "", 30, 4); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("rollout bypassed logical queue concurrency: %v", err)
	}
	if err := store.CompleteInvocation(ctx, first.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, second.ID, "", 30, 4); err != nil {
		t.Fatalf("completion failed to free shared domain: %v", err)
	}
	if err := store.CancelInvocation(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, cur, err = store.GetAccountAsyncQuota(ctx, f.account.ID)
	if err != nil || cur != 0 {
		t.Fatalf("terminal transitions leaked quota: %d %v", cur, err)
	}
}

func TestMemEnvironmentQueueMessagesCleanUpAfterRetirement(t *testing.T) {
	testEnvironmentQueueMessageCleanup(t, state.NewMemStore())
}
func testEnvironmentQueueMessageCleanup(t *testing.T, store environmentQueueInvocationTestStore) queueConsumerFixture {
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	inv := enqueueStageQueue(t, store, f)
	if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 4); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, f.dep.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); !errors.Is(err, state.ErrEnvironmentInvocationWorkBusy) {
		t.Fatalf("busy queue removed: %v", err)
	}
	if err := store.CompleteInvocation(ctx, inv.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatalf("idle retired queue blocked cleanup: %v", err)
	}
	if _, err := store.InvocationByID(ctx, inv.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cleanup retained message: %v", err)
	}
	if _, err := store.InvocationEnvironmentQueueAdmission(ctx, inv.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cleanup retained ownership proof: %v", err)
	}
	if _, err := store.QueueBindingByID(ctx, f.account.ID, f.app.ID, f.prod.ID); err != nil {
		t.Fatalf("cleanup removed production queue: %v", err)
	}
	return f
}

func environmentQueueJSONEqual(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func TestMemEnvironmentQueueAdmissionSupportsEveryDefinitionClassAndMode(t *testing.T) {
	testEnvironmentQueueAdmissionClasses(t, state.NewMemStore())
}
func testEnvironmentQueueAdmissionClasses(t *testing.T, store environmentQueueInvocationTestStore) {
	base := seedQueueConsumers(t, store)
	for _, kind := range []struct {
		class state.WorkloadClass
		mode  string
	}{
		{state.WorkloadClassWorker, "pull"}, {state.WorkloadClassWorker, "push"}, {state.WorkloadClassJob, "pull"}, {state.WorkloadClassJob, "push"}, {state.WorkloadClassHTTP, "push"},
	} {
		t.Run(string(kind.class)+"_"+kind.mode, func(t *testing.T) {
			ctx := t.Context()
			f := base
			appType := state.AppTypeApp
			if kind.class == state.WorkloadClassHTTP {
				appType = state.AppTypeFunction
			}
			var err error
			f.app, err = store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "consumer-" + string(kind.class) + "-" + kind.mode, WorkloadName: "consumer-" + string(kind.class) + "-" + kind.mode, Type: appType, WorkloadClass: kind.class, RAMMB: 256, MaxConcurrency: 4, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
			if err != nil {
				t.Fatal(err)
			}
			f.spec, err = state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "stage", 0, []state.ProjectEnvironmentQueueDefinition{{Name: "orders", QueueName: "orders", Mode: kind.mode, WorkloadClass: kind.class, Enabled: true, MaxConcurrency: 1}})
			if err != nil {
				t.Fatal(err)
			}
			f.dep, err = store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, f.dep.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
				t.Fatal(err)
			}
			inv := enqueueStageQueue(t, store, f)
			if _, version, err := state.ResolveInvocationVersion(ctx, store, inv); err != nil || version.DeploymentID != f.dep.ID {
				t.Fatalf("definition class/mode lost pinned delivery: %+v %v", version, err)
			}
			if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 4); err != nil {
				t.Fatalf("definition class/mode admission claim: %v", err)
			}
			if err := store.CompleteInvocation(ctx, inv.ID, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMemEnvironmentQueuePartitionsShareOnlyAccountQuota(t *testing.T) {
	testEnvironmentQueuePartitions(t, state.NewMemStore())
}
func testEnvironmentQueuePartitions(t *testing.T, store environmentQueueInvocationTestStore) {
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		inv := enqueueStageQueue(t, store, f)
		if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 3); err != nil {
			t.Fatal(err)
		}
	}
	prod, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, prod.ID, "", 30, 3); err != nil {
		t.Fatalf("stage full queue throttled production consumer: %v", err)
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "other", 0, f.spec.Settings.QueueBindings.Bindings); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "other", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	f.dep = other
	sibling := enqueueStageQueue(t, store, f)
	if _, err := store.ClaimInvocationWithCap(ctx, sibling.ID, "", 30, 3); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("sibling bypassed global account quota: %v", err)
	}
	if err := store.CompleteInvocation(ctx, prod.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, sibling.ID, "", 30, 3); err != nil {
		t.Fatalf("stage full queue throttled sibling queue: %v", err)
	}
}
