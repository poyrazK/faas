// adr: 531
package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type invocationWorkEnvironmentTestStore interface {
	invocationEnvironmentTestStore
	state.ProjectEnvironmentWorkPolicyStore
	state.DeploymentWorkloadSpecReader
	state.InvocationWorkEnvironmentAdmissionReader
	state.EnvironmentWorkCancellationStore
	state.WorkCancellationStore
	RollbackProjectEnvironmentClone(context.Context, string, string, string) error
	RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
}

type invocationWorkEnvironmentFixture struct {
	invocationEnvironmentFixture
	other          state.Deployment
	latest, serial workpolicy.Policy
}

func seedInvocationWorkEnvironment(t *testing.T, store invocationWorkEnvironmentTestStore) invocationWorkEnvironmentFixture {
	t.Helper()
	f := invocationWorkEnvironmentFixture{invocationEnvironmentFixture: seedInvocationEnvironment(t, store),
		latest: workpolicy.Policy{Name: "latest", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest},
		serial: workpolicy.Policy{Name: "serial", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll, MaxRunningPerFairnessKey: 1, ExpiresAfter: time.Hour}}
	for _, environment := range []string{"staging", "empty-stage"} {
		for _, policy := range []workpolicy.Policy{f.latest, f.serial} {
			if _, _, err := state.UpsertEnvironmentWorkPolicy(t.Context(), store, f.app, environment, nil, policy); err != nil {
				t.Fatal(err)
			}
		}
		dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Scope: environment, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
			t.Fatal(err)
		}
		set, err := store.PublishProjectReleaseSet(t.Context(), f.account.ID, f.project.ID, environment, 1800, []state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: dep.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if environment == "staging" {
			f.stage, f.stageRelease = dep, set
		} else {
			f.other = dep
		}
	}
	return f
}

func (f invocationWorkEnvironmentFixture) request(t *testing.T, store invocationWorkEnvironmentTestStore, environment string) state.Invocation {
	t.Helper()
	request := state.Invocation{ID: uuid.NewString(), AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/work", Payload: json.RawMessage(`{}`), DueAt: time.Now().Add(-time.Second)}
	prepared, _, err := state.ResolveInvocationVersionForEnvironment(t.Context(), store, request, environment)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestMemInvocationWorkEnvironmentIsolation(t *testing.T) {
	testInvocationWorkEnvironmentIsolation(t, state.NewMemStore())
}

func testInvocationWorkEnvironmentIsolation(t *testing.T, store invocationWorkEnvironmentTestStore) invocationWorkEnvironmentFixture {
	t.Helper()
	ctx := t.Context()
	f := seedInvocationWorkEnvironment(t, store)
	enqueue := func(environment string, policy workpolicy.Policy, key string, fairness ...string) state.Invocation {
		t.Helper()
		row, err := store.EnqueueKeyedInvocation(ctx, f.request(t, store, environment), policy, key, fairness...)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	assertState := func(row state.Invocation, expected state.InvocationState) {
		t.Helper()
		actual, err := store.InvocationByID(ctx, row.ID)
		if err != nil || actual.State != expected {
			t.Fatalf("%s state = %s, %v; want %s", row.ID, actual.State, err, expected)
		}
	}
	prod := enqueue("production", f.latest, "s:one")
	stage := enqueue("staging", f.latest, "s:one")
	other := enqueue("empty-stage", f.latest, "s:one")
	legacyDigest, _ := workpolicy.DigestKey("s:one")
	if !bytes.Equal(prod.WorkKeyDigest, legacyDigest[:]) || bytes.Equal(prod.WorkKeyDigest, stage.WorkKeyDigest) || bytes.Equal(stage.WorkKeyDigest, other.WorkKeyDigest) {
		t.Fatal("production or stage key namespaces overlap")
	}
	newStage := enqueue("staging", f.latest, "s:one")
	assertState(stage, state.InvocationSuperseded)
	assertState(prod, state.InvocationPending)
	assertState(other, state.InvocationPending)
	if newStage.WorkSequence != 2 || prod.WorkSequence != 1 || other.WorkSequence != 1 {
		t.Fatalf("shared sequence: stage=%d production=%d sibling=%d", newStage.WorkSequence, prod.WorkSequence, other.WorkSequence)
	}
	owner, err := store.InvocationWorkEnvironmentAdmission(ctx, newStage.ID)
	spec, specErr := store.ProjectEnvironmentWorkloadSpecForDeployment(ctx, f.account.ID, f.project.ID, f.stage.ID)
	if err != nil || specErr != nil || owner.EnvironmentID != spec.EnvironmentID || owner.WorkloadSpecID != spec.ID || owner.SettingsHash != spec.Hash || owner.PolicyRevision != 1 {
		t.Fatalf("admission not tied to pinned policy: %+v, %v, %v", owner, err, specErr)
	}
	owner.KeyDigest[0] ^= 1
	if fresh, _ := store.InvocationWorkEnvironmentAdmission(ctx, newStage.ID); !bytes.Equal(fresh.KeyDigest, newStage.WorkKeyDigest) {
		t.Fatal("caller changed durable admission")
	}
	if _, err := store.InvocationWorkEnvironmentAdmission(ctx, prod.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("legacy production gained stage ownership: %v", err)
	}
	for _, policy := range []workpolicy.Policy{
		{Name: "latest", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll},
		{Name: "latest", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest, Debounce: time.Second},
	} {
		request := f.request(t, store, "staging")
		if _, err := store.EnqueueKeyedInvocation(ctx, request, policy, "s:one"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unowned policy admitted: %v", err)
		}
		if _, err := store.InvocationByID(ctx, request.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("rejected policy wrote an invocation: %v", err)
		}
		assertState(newStage, state.InvocationPending)
		assertState(prod, state.InvocationPending)
	}
	if replay, err := store.EnqueueKeyedInvocation(ctx, newStage, f.latest, "s:one"); err != nil || replay.ID != newStage.ID || replay.WorkSequence != 2 {
		t.Fatalf("stage enqueue replay = %+v, %v", replay, err)
	}
	direct := f.request(t, store, "staging")
	direct.Headers, _ = json.Marshal(map[string]string{api.RevisionHeader: f.stage.ID})
	direct, err = store.EnqueueKeyedInvocation(ctx, direct, f.serial, "s:direct", "s:direct-customer")
	if err != nil {
		t.Fatal(err)
	}
	if _, selected, err := state.ResolveInvocationVersion(ctx, store, direct); err != nil || selected.Scope != "staging" || selected.DeploymentID != f.stage.ID || selected.ReleaseID != "" {
		t.Fatalf("direct stage revision = %+v, %v", selected, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, direct.ID, "", 30, 100); err != nil {
		t.Fatalf("direct stage claim: %v", err)
	}
	borrowed, err := store.InvocationByID(ctx, direct.ID)
	if err != nil {
		t.Fatal(err)
	}
	borrowed.Headers[0], borrowed.WorkKeyDigest[0], borrowed.WorkFairnessDigest[0] = '!', 0xff, 0xff
	*borrowed.WorkExpiresAt = borrowed.CreatedAt
	retained, err := store.InvocationByID(ctx, direct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, selected, err := state.ResolveInvocationVersion(ctx, store, retained); err != nil || selected.DeploymentID != f.stage.ID {
		t.Fatalf("caller changed persisted work envelope: %+v, %v", selected, err)
	}
	// Fairness and lane claims are independent across environments. Account
	// capacity remains shared and is deliberately generous in this contract.
	prodRunning := enqueue("production", f.serial, "s:first", "s:customer")
	stageRunning := enqueue("staging", f.serial, "s:first", "s:customer")
	otherRunning := enqueue("empty-stage", f.serial, "s:first", "s:customer")
	for _, row := range []state.Invocation{prodRunning, stageRunning, otherRunning} {
		if _, err := store.ClaimInvocationWithCap(ctx, row.ID, "", 30, 100); err != nil {
			t.Fatalf("cross-environment claim blocked: %s: %v", row.ID, err)
		}
	}
	if bytes.Equal(prodRunning.WorkFairnessDigest, stageRunning.WorkFairnessDigest) || bytes.Equal(stageRunning.WorkFairnessDigest, otherRunning.WorkFairnessDigest) {
		t.Fatal("fairness namespaces overlap")
	}
	blocked := enqueue("staging", f.serial, "s:second", "s:customer")
	if _, err := store.ClaimInvocationWithCap(ctx, blocked.ID, "", 30, 100); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stage fairness not enforced: %v", err)
	}
	independent := enqueue("staging", f.serial, "s:third", "s:other-customer")
	if _, err := store.ClaimInvocationWithCap(ctx, independent.ID, "", 30, 100); err != nil {
		t.Fatal(err)
	}
	// Desired policy edits/deletions do not change retained deployed work.
	changed := f.serial
	changed.Debounce = time.Minute
	if _, _, err := state.UpsertEnvironmentWorkPolicy(ctx, store, f.app, "staging", nil, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DeleteEnvironmentWorkPolicy(ctx, store, f.app, "staging", f.serial.Name, nil); err != nil {
		t.Fatal(err)
	}
	for _, row := range []state.Invocation{stageRunning, blocked} {
		if _, selected, err := state.ResolveInvocationVersion(ctx, store, row); err != nil || selected.DeploymentID != f.stage.ID || selected.Scope != "staging" {
			t.Fatalf("desired edit changed retained work: %+v, %v", selected, err)
		}
	}
	// The operational cancel can drain that old policy without inheriting a
	// production policy. Its receipt cannot cancel newer work on replay.
	receiptID := uuid.NewString()
	receipt, err := store.CancelPendingEnvironmentKeyedInvocations(ctx, f.account.ID, f.app.ID, "staging", f.serial.Name, "s:second", receiptID)
	if err != nil || receipt.CancelledCount != 1 {
		t.Fatalf("scoped cancellation = %+v, %v", receipt, err)
	}
	receipt.KeyDigest[0] ^= 1
	readReceipt, err := store.WorkCancellationByID(ctx, receiptID)
	if err != nil || !bytes.Equal(readReceipt.KeyDigest, blocked.WorkKeyDigest) {
		t.Fatalf("caller changed cancellation receipt: %+v, %v", readReceipt, err)
	}
	readReceipt.KeyDigest[0] ^= 1
	assertState(blocked, state.InvocationCancelled)
	assertState(stageRunning, state.InvocationDispatching)
	assertState(prodRunning, state.InvocationDispatching)
	later := enqueue("staging", f.serial, "s:second", "s:customer")
	if replay, err := store.CancelPendingEnvironmentKeyedInvocations(ctx, f.account.ID, f.app.ID, "staging", f.serial.Name, "s:second", receiptID); err != nil || replay.CancelledCount != 1 {
		t.Fatalf("scoped cancel replay = %+v, %v", replay, err)
	}
	assertState(later, state.InvocationPending)
	if _, err := store.CancelPendingEnvironmentKeyedInvocations(ctx, f.account.ID, f.app.ID, "empty-stage", f.serial.Name, "s:second", receiptID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("receipt crossed stage: %v", err)
	}
	if _, err := store.CancelPendingKeyedInvocations(ctx, f.app.ID, f.serial.Name, "s:second", receiptID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stage receipt crossed production: %v", err)
	}
	if _, err := store.CancelPendingEnvironmentKeyedInvocations(ctx, uuid.NewString(), f.app.ID, "staging", f.latest.Name, "s:one", uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account cancelled stage: %v", err)
	}
	for _, scope := range []string{"", "default", "production"} {
		if _, err := store.CancelPendingEnvironmentKeyedInvocations(ctx, f.account.ID, f.app.ID, scope, f.latest.Name, "s:one", uuid.NewString()); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("stage cancellation accepted %q: %v", scope, err)
		}
	}
	stageReceipt, err := store.CancelPendingEnvironmentKeyedInvocations(ctx, f.account.ID, f.app.ID, "staging", f.latest.Name, "s:one", uuid.NewString())
	if err != nil || stageReceipt.CancelledCount != 1 {
		t.Fatalf("latest stage cancel = %+v, %v", stageReceipt, err)
	}
	assertState(newStage, state.InvocationCancelled)
	assertState(prod, state.InvocationPending)
	assertState(other, state.InvocationPending)
	if _, err := store.CancelPendingEnvironmentKeyedInvocations(ctx, f.account.ID, f.app.ID, "empty-stage", f.latest.Name, "s:one", uuid.NewString(), uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale environment identity cancelled recreated scope: %v", err)
	}
	assertState(other, state.InvocationPending)
	for _, change := range []func(*state.Invocation){
		func(inv *state.Invocation) { inv.Source = state.InvocationQueue },
		func(inv *state.Invocation) { inv.Source = state.InvocationCron },
		func(inv *state.Invocation) { inv.Source = state.InvocationInboundWebhook },
		func(inv *state.Invocation) { inv.Source = state.InvocationReplay },
		func(inv *state.Invocation) { inv.Source = state.InvocationSource("esm") },
		func(inv *state.Invocation) { inv.QueueName = "production-queue" },
		func(inv *state.Invocation) { id := uuid.NewString(); inv.CronID = &id },
		func(inv *state.Invocation) { inv.OnSuccessDestinationID = uuid.NewString() },
		func(inv *state.Invocation) { inv.OnFailureDestinationID = uuid.NewString() },
	} {
		request := f.request(t, store, "staging")
		change(&request)
		if _, err := store.EnqueueKeyedInvocation(ctx, request, f.latest, "s:one"); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
			t.Fatalf("stage work used a shared producer or destination: %v", err)
		}
	}
	if receipt, err := store.CancelPendingKeyedInvocations(ctx, f.app.ID, f.latest.Name, "s:one", uuid.NewString()); err != nil || receipt.CancelledCount != 1 {
		t.Fatalf("production cancellation changed stage lanes: %+v, %v", receipt, err)
	}
	assertState(other, state.InvocationPending)
	// Delivery cannot silently turn an owned row into production if pins or
	// keyed metadata are missing or altered.
	for _, change := range []func(*state.Invocation){
		func(inv *state.Invocation) { inv.Headers = json.RawMessage(`{}`) },
		func(inv *state.Invocation) { inv.WorkPolicyRevision++ },
		func(inv *state.Invocation) { inv.WorkFairnessLimit++ },
		func(inv *state.Invocation) { inv.WorkKeyDigest = make([]byte, 32) },
		func(inv *state.Invocation) { inv.WorkExpiresAt = nil },
		func(inv *state.Invocation) { inv.DueAt = inv.CreatedAt.Add(-time.Second) },
		func(inv *state.Invocation) {
			inv.WorkPolicyName, inv.WorkKeyDigest, inv.WorkFairnessDigest = "", nil, nil
		},
	} {
		altered := later
		change(&altered)
		if _, _, err := state.ResolveInvocationVersion(ctx, store, altered); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
			t.Fatalf("altered work delivered: %v", err)
		}
	}
	return f
}

func TestMemInvocationWorkEnvironmentCleanup(t *testing.T) {
	testInvocationWorkEnvironmentCleanup(t, state.NewMemStore())
}

func testInvocationWorkEnvironmentCleanup(t *testing.T, store invocationWorkEnvironmentTestStore) {
	t.Helper()
	f := seedInvocationWorkEnvironment(t, store)
	ctx := t.Context()
	for name, remove := range map[string]func(context.Context, string, string, string) error{
		"delete": store.DeleteProjectEnvironment, "rollback": store.RollbackProjectEnvironmentClone,
	} {
		t.Run(name, func(t *testing.T) {
			env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "drain-only"})
			if err != nil {
				t.Fatal(err)
			}
			operationID := uuid.NewString()
			if _, err := store.CancelPendingEnvironmentKeyedInvocations(ctx, f.account.ID, f.app.ID, env.Slug, "old-policy", "s:one", operationID); err != nil {
				t.Fatal(err)
			}
			if err := remove(ctx, f.account.ID, f.project.ID, env.Slug); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, env.Slug); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("idle work stage remains: %v", err)
			}
			// A deleted stage receipt must still fence operation reuse in production.
			if _, err := store.CancelPendingKeyedInvocations(ctx, f.app.ID, "old-policy", "s:one", operationID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("receipt identity was erased: %v", err)
			}
		})
	}
}
