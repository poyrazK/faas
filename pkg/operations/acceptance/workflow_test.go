package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type workflowOperationFixture struct {
	store      state.Store
	ops        state.OperationStore
	account    state.Account
	app        state.App
	definition state.OperationDefinition
	tenant     state.PlatformTenant
	other      state.PlatformTenant
	admission  state.OperationAdmission
}

func operationWorkflowStores(t *testing.T, test func(*testing.T, state.Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { test(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { store, _ := pgStore(t); test(t, store) })
}

func workflowOperationSetup(t *testing.T, store state.Store) workflowOperationFixture {
	return workflowOperationSetupWithTimeout(t, store, 0)
}

func workflowOperationSetupWithTimeout(t *testing.T, store state.Store, timeout time.Duration) workflowOperationFixture {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "workflow-operations@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "workflow-operations", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "export-chain", Steps: []api.WorkflowStepSpec{
		{Name: "collect", Path: "/collect"},
		{Name: "transform", Path: "/transform", DependsOn: []string{"collect"}, Input: json.RawMessage(`{"rows":"{{steps.collect.output.rows}}"}`)},
		{Name: "finish", Path: "/finish", DependsOn: []string{"transform"}, Input: json.RawMessage(`{"rows":"{{steps.transform.output.rows}}"}`)},
	}}
	for i := range spec.Steps {
		spec.Steps[i].Timeout = timeout
	}
	raw, err := json.Marshal([]api.WorkflowSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:workflow-operations", Workflows: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	ops := store.(state.OperationStore)
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: account.ID, AppID: app.ID, TargetURL: "https://completion.example.test/events", Enabled: true, SecretSealed: []byte("sealed"), EventFilter: []string{string(state.AppWebhookEventOperationFinished)}})
	if err != nil {
		t.Fatal(err)
	}
	def, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{
			Name: "export", Workflow: spec.Name, Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
			InputSchema:    []byte(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}},"additionalProperties":false}`),
			OutputSchema:   []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}},"additionalProperties":false}`),
			ProgressStages: []string{"collect", "transform", "finish"}, CompletionWebhookID: hook.ID,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tenants := store.(state.PlatformTenantStore)
	tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := tenants.CreatePlatformTenant(ctx, account.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: account.ID, AppID: app.ID, Name: "workflow-alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	return workflowOperationFixture{store: store, ops: ops, account: account, app: app, definition: def, tenant: tenant, other: other,
		admission: state.OperationAdmission{AccountID: account.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "export-1", Input: []byte(`{"count":1}`)}}
}

func (f workflowOperationFixture) start(t *testing.T) state.Operation {
	t.Helper()
	op, fresh, err := f.ops.AdmitOperation(context.Background(), f.admission)
	if err != nil || !fresh || op.CurrentInvocationID != "" || op.WorkflowRunID == "" {
		t.Fatalf("workflow admission=%+v fresh=%v err=%v", op, fresh, err)
	}
	return op
}

func (f workflowOperationFixture) read(ctx context.Context, t *testing.T, id string) state.Operation {
	t.Helper()
	op, err := f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

type operationWorkflowExecutor struct {
	execute func(context.Context, sched.WorkflowStepIdentity, string, map[string]string, []byte) (int, []byte, error)
}

func (e operationWorkflowExecutor) ExecuteStep(context.Context, string, string, string, map[string]string, []byte, time.Duration) (int, []byte, error) {
	return 0, nil, errors.New("tenant workflow used legacy transport")
}
func (e operationWorkflowExecutor) ExecuteWorkflowStep(ctx context.Context, _ string, identity sched.WorkflowStepIdentity, path, _ string, headers map[string]string, input []byte, _ time.Duration, _ string, _ int64) (int, []byte, error) {
	return e.execute(ctx, identity, path, headers, input)
}

func TestOperationWorkflowResumePreservesConfirmedSteps(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := workflowOperationSetup(t, store)
		op := f.start(t)
		ctx := context.Background()
		run, err := store.GetWorkflowRun(ctx, op.WorkflowRunID)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := string(run.DefinitionSnapshot)
		if run.PlatformTenantID != f.tenant.ID || workflowCanonical(t, run.Input) != `{"count":1}` {
			t.Fatalf("run lost scoped input: %+v", run)
		}
		var calls, keys, inputs []string
		failed := false
		executor := operationWorkflowExecutor{execute: func(ctx context.Context, identity sched.WorkflowStepIdentity, path string, headers map[string]string, input []byte) (int, []byte, error) {
			if identity.RunID != run.ID || identity.PlatformTenantID != f.tenant.ID || headers[api.RevisionHeader] != f.definition.DeploymentID {
				t.Fatalf("workflow lost identity or pin: %+v %v", identity, headers)
			}
			encoded, _ := json.Marshal(headers)
			inv := state.Invocation{ID: "workflow-test", AppID: f.app.ID, PlatformTenantID: f.tenant.ID, Source: state.InvocationSource("workflow"), Path: path, Method: "POST", Headers: encoded, Payload: input}
			_, version, err := state.ResolveInvocationVersion(ctx, store, inv)
			if err != nil || version.DeploymentID != f.definition.DeploymentID {
				t.Fatalf("private workflow dispatch=%+v %v", version, err)
			}
			calls = append(calls, path)
			keys = append(keys, headers["Idempotency-Key"])
			inputs = append(inputs, string(input))
			progress := f.read(ctx, t, op.ID)
			if progress.State != api.OperationRunning || progress.Progress == nil {
				t.Fatalf("missing running projection: %+v", progress)
			}
			if path == "/transform" && !failed {
				failed = true
				return 503, []byte(`{"error":"unavailable"}`), nil
			}
			if path == "/finish" {
				return 200, []byte(`{"file":"export.csv"}`), nil
			}
			return 200, []byte(`{"rows":[1,2,3]}`), nil
		}}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		first := f.read(ctx, t, op.ID)
		if first.State != api.OperationRequiresReconciliation || first.Progress.Completed != 1 || first.CompletionDelivery.State != "awaiting_outcome" || len(calls) != 2 {
			t.Fatalf("failed action projection=%+v calls=%v", first, calls)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil || len(calls) != 2 {
			t.Fatalf("uncertain action replayed: %v %v", calls, err)
		}
		if _, err := f.ops.OperationByID(ctx, f.account.ID, f.other.ID, op.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-tenant operation read", err)
		}
		if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.WorkflowResumeOptions{RunID: run.ID, AppID: f.app.ID, AccountID: f.account.ID}); !errors.Is(err, state.ErrWorkflowResumeUnsafe) {
			t.Fatal("direct resume bypassed recovery", err)
		}
		if _, _, err := store.(state.WorkflowRetryStore).RetryWorkflowStep(ctx, run.ID, "transform", f.account.Plan.WorkflowMaxConcurrentRuns()); !errors.Is(err, state.ErrWorkflowRetryNotAllowed) {
			t.Fatal("direct step retry bypassed recovery", err)
		}
		next, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, next.ID); err != nil {
			t.Fatal(err)
		}
		recovery := api.OperationRecoveryRequest{RecoveryID: "verified-resume", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "provider ledger confirms transform did not create an external result"}
		if _, err := f.ops.RecoverOperation(ctx, f.account.ID, f.other.ID, op.ID, recovery); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-tenant recovery", err)
		}
		if err := store.UpdateAccountPlan(ctx, f.account.ID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		if _, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, recovery); !errors.Is(err, state.ErrOperationQuota) {
			t.Fatal("workflow recovery bypassed current Operations plan", err)
		}
		if err := store.UpdateAccountPlan(ctx, f.account.ID, f.account.Plan); err != nil {
			t.Fatal(err)
		}
		// A rejected resume must retain the generation and leave no receipt, so
		// the same recovery request can succeed once native quota is available.
		queued := []string{}
		for range f.account.Plan.WorkflowMaxConcurrentRuns() {
			queuedRun := &state.WorkflowRun{AppID: f.app.ID, WorkflowName: "quota", DefinitionSnapshot: []byte(`{"name":"quota","steps":[{"name":"work","run":"work"}]}`)}
			if err := store.CreateWorkflowRun(ctx, queuedRun); err != nil {
				t.Fatal(err)
			}
			queued = append(queued, queuedRun.ID)
		}
		if _, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, recovery); !errors.Is(err, state.ErrOperationQuota) {
			t.Fatal("workflow recovery ignored quota", err)
		}
		if rejected := f.read(ctx, t, op.ID); rejected.Generation != 1 || rejected.RecoveryCount != 0 || rejected.State != api.OperationRequiresReconciliation {
			t.Fatalf("rejected recovery changed operation: %+v", rejected)
		}
		for _, id := range queued {
			if _, err := store.CancelWorkflowRun(ctx, id, "release quota"); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				recovered, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, recovery)
				if err != nil || recovered.WorkflowRunID != run.ID || recovered.Generation != 2 {
					t.Errorf("concurrent resume=%+v %v", recovered, err)
				}
			}()
		}
		wg.Wait()
		stale := state.WithWorkflowRunGeneration(ctx, run.ID, 0)
		if err := store.MarkWorkflowStepAttemptStatus(stale, run.ID, "transform", state.WorkflowStepStatusSucceeded, 1, nil, []byte(`{"rows":[9]}`), nil); !errors.Is(err, state.ErrWorkflowOutboundAttemptExpired) {
			t.Fatal("stale completion overwrote resumed work", err)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final := f.read(ctx, t, op.ID)
		if final.State != api.OperationSucceeded || final.ID != op.ID || final.WorkflowRunID != run.ID || workflowCanonical(t, final.Result) != `{"file":"export.csv"}` || final.Progress.Completed != 3 || final.CompletionDelivery.State != "pending" {
			t.Fatalf("resumed result=%+v", final)
		}
		if fmt.Sprint(calls) != "[/collect /transform /transform /finish]" || keys[1] != keys[2] || inputs[1] != inputs[2] || workflowCanonical(t, []byte(inputs[1])) != `{"rows":[1,2,3]}` {
			t.Fatalf("completed prefix, key or input changed: %v %v %v", calls, keys, inputs)
		}
		finished, _ := store.GetWorkflowRun(ctx, run.ID)
		if string(finished.DefinitionSnapshot) != snapshot || finished.ResumeCount != 1 {
			t.Fatalf("resume replaced workflow: %+v", finished)
		}
		replay, fresh, err := f.ops.AdmitOperation(ctx, f.admission)
		if err != nil || fresh || replay.ID != op.ID {
			t.Fatal("submission receipt changed", err)
		}
		history, err := f.ops.OperationExecutions(ctx, f.account.ID, op.ID, 0, 100)
		if err != nil || len(history.Executions) != 2 || history.Executions[0].WorkflowRunID != run.ID || history.Executions[0].State != state.WorkflowRunStatusDead || history.Executions[1].State != state.WorkflowRunStatusSucceeded {
			t.Fatalf("workflow execution history=%+v %v", history, err)
		}
		events, err := f.ops.OperationEvents(ctx, f.account.ID, f.tenant.ID, op.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		resumes, successes := 0, 0
		for _, event := range events.Events {
			if event.Type == "recovery_requested" {
				resumes++
			}
			if event.Type == "succeeded" {
				successes++
			}
		}
		if resumes != 1 || successes != 1 {
			t.Fatalf("duplicate lifecycle events: %+v", events.Events)
		}

		deliveries, err := store.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now().Add(time.Second))
		if err != nil || len(deliveries) != 1 {
			t.Fatalf("completion claim=%+v %v", deliveries, err)
		}
		delivery := deliveries[0]
		if err := store.MarkAppWebhookDeliveryDead(ctx, delivery.ID, delivery.Attempt, delivery.NextAttemptAt, "receiver unavailable"); err != nil {
			t.Fatal(err)
		}
		if failedDelivery := f.read(ctx, t, op.ID); failedDelivery.State != api.OperationSucceeded || failedDelivery.CompletionDelivery.State != "dead" {
			t.Fatalf("notification failure changed business state: %+v", failedDelivery)
		}
		if _, err := f.ops.(state.OperationDeliveryStore).RetryOperationCompletionDelivery(ctx, f.account.ID, op.ID, api.OperationDeliveryRetryRequest{RetryID: "delivery-only", DeliveryID: final.CompletionDelivery.DeliveryID, ExpectedReplayGeneration: func() *int { zero := 0; return &zero }()}); err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil || len(calls) != 4 || f.read(ctx, t, op.ID).State != api.OperationSucceeded {
			t.Fatal("delivery retry repeated business work", err)
		}
		if deleted, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || deleted != len(queued) {
			t.Fatalf("retained workflow pruned: %d %v", deleted, err)
		}
		if _, err := store.GetWorkflowRun(ctx, op.WorkflowRunID); err != nil {
			t.Fatal("retained operation lost its workflow", err)
		}
	})
}

func TestOperationWorkflowCancellationFencesRunningAction(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := workflowOperationSetup(t, store)
		op := f.start(t)
		ctx := context.Background()
		entered, stopped := make(chan struct{}), make(chan error, 1)
		executor := operationWorkflowExecutor{execute: func(ctx context.Context, _ sched.WorkflowStepIdentity, _ string, _ map[string]string, _ []byte) (int, []byte, error) {
			close(entered)
			<-ctx.Done()
			return 0, nil, ctx.Err()
		}}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		go func() { stopped <- orchestrator.DispatchTick(ctx) }()
		select {
		case <-entered:
		case <-time.After(4 * time.Second):
			t.Fatal("workflow action did not start")
		}
		if _, err := f.ops.CancelOperation(ctx, f.account.ID, f.other.ID, op.ID, 1); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-tenant cancellation", err)
		}
		cancelled, err := f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, op.ID, 1)
		if err != nil || cancelled.State != api.OperationRequiresReconciliation || !cancelled.CancellationRequested {
			t.Fatalf("dispatched cancellation=%+v %v", cancelled, err)
		}
		select {
		case <-stopped:
		case <-time.After(4 * time.Second):
			t.Fatal("cancelled workflow HTTP context stayed active")
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, op.WorkflowRunID, "collect", state.WorkflowStepStatusSucceeded, 1, nil, []byte(`{"rows":[1]}`), nil); !errors.Is(err, state.ErrWorkflowOutboundAttemptExpired) {
			t.Fatal("late cancellation completion accepted", err)
		}
		recovery := api.OperationRecoveryRequest{RecoveryID: "unsafe-cancel-resume", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "customer cancelled"}
		if _, err := f.ops.RecoverOperation(ctx, f.account.ID, "", op.ID, recovery); !errors.Is(err, state.ErrWorkflowResumeUnsafe) {
			t.Fatal("cancelled workflow resumed", err)
		}
		recovery.RecoveryID, recovery.Resolution = "confirmed-cancel", "cancelled"
		if resolved, err := f.ops.RecoverOperation(ctx, f.account.ID, "", op.ID, recovery); err != nil || resolved.State != api.OperationCancelled {
			t.Fatalf("confirmed cancellation=%+v %v", resolved, err)
		}
	})
}

func TestOperationWorkflowPendingCancellationAndAtomicQuota(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := workflowOperationSetup(t, store)
		ctx := context.Background()
		var queued []string
		for range f.account.Plan.WorkflowMaxConcurrentRuns() {
			run := &state.WorkflowRun{AppID: f.app.ID, WorkflowName: "other", DefinitionSnapshot: []byte(`{"name":"other","steps":[{"name":"other","path":"/other"}]}`)}
			if err := store.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			queued = append(queued, run.ID)
		}
		if _, _, err := f.ops.AdmitOperation(ctx, f.admission); !errors.Is(err, state.ErrOperationQuota) {
			t.Fatal("workflow quota ignored", err)
		}
		history, err := f.ops.ListPlatformTenantOperations(ctx, f.account.ID, f.tenant.ID, api.OperationListOptions{AppID: f.app.ID, Scope: f.definition.Scope})
		if err != nil || len(history.Operations) != 0 {
			t.Fatalf("rejected admission left operation: %+v %v", history, err)
		}
		if _, err := store.CancelWorkflowRun(ctx, queued[0], "free quota"); err != nil {
			t.Fatal(err)
		}
		op := f.start(t)
		cancelled, err := f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, op.ID, 1)
		if err != nil || cancelled.State != api.OperationCancelled || !cancelled.CancellationRequested {
			t.Fatalf("undispatched cancellation=%+v %v", cancelled, err)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, op.WorkflowRunID, "collect")
		if err != nil || len(attempts) != 0 {
			t.Fatalf("pending cancellation dispatched work: %+v %v", attempts, err)
		}
		replay, fresh, err := f.ops.AdmitOperation(ctx, f.admission)
		if err != nil || fresh || replay.ID != op.ID {
			t.Fatal("quota rejection or cancellation lost receipt", err)
		}
	})
}

func workflowCanonical(t *testing.T, raw []byte) string {
	t.Helper()
	canonical, err := operations.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

func TestOperationWorkflowConcurrentAdmission(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := workflowOperationSetup(t, store)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var freshCount atomic.Int64
		ids := make(chan string, 24)
		var wg sync.WaitGroup
		for range cap(ids) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				op, fresh, err := f.ops.AdmitOperation(ctx, f.admission)
				if err != nil {
					t.Error(err)
					return
				}
				if fresh {
					freshCount.Add(1)
				}
				ids <- op.ID
			}()
		}
		wg.Wait()
		close(ids)
		if freshCount.Load() != 1 || len(ids) != cap(ids) {
			t.Fatalf("duplicate submissions: fresh=%d receipts=%d", freshCount.Load(), len(ids))
		}
		id := ""
		for received := range ids {
			if id != "" && received != id {
				t.Fatal("duplicate submission created another operation")
			}
			id = received
		}
		runs, total, err := store.ListWorkflowRuns(ctx, f.app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || total != 1 || len(runs) != 1 || runs[0].ID != f.read(ctx, t, id).WorkflowRunID {
			t.Fatalf("duplicate submission left extra workflow runs: %+v %d %v", runs, total, err)
		}
	})
}

func TestOperationWorkflowRetentionAndOwnerDeletion(t *testing.T) {
	for _, action := range []string{"expiry", "owner deletion"} {
		t.Run(action, func(t *testing.T) {
			operationWorkflowStores(t, func(t *testing.T, store state.Store) {
				f := workflowOperationSetup(t, store)
				op := f.start(t)
				ctx := context.Background()
				if action == "expiry" {
					cancelled, err := f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, op.ID, 1)
					if err != nil {
						t.Fatal(err)
					}
					if count, err := store.(state.OperationRetentionStore).PruneOperationState(ctx, cancelled.ExpiresAt.Add(time.Second), api.OperationRetentionPageMax); err != nil || count != 1 {
						t.Fatalf("operation expiry=%d %v", count, err)
					}
					if count, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || count != 1 {
						t.Fatalf("released workflow retention=%d %v", count, err)
					}
				} else {
					if err := store.UpdateAccountStatus(ctx, f.account.ID, state.AccountDeletedPending); err != nil {
						t.Fatal(err)
					}
					if err := store.DeleteAccount(ctx, f.account.ID); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.ops.OperationByID(ctx, f.account.ID, "", op.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("customer operation survived cleanup", err)
				}
				if _, err := store.GetWorkflowRun(ctx, op.WorkflowRunID); !errors.Is(err, state.ErrWorkflowRunNotFound) {
					t.Fatal("workflow survived cleanup", err)
				}
			})
		})
	}
}

func TestOperationWorkflowInterruptedActionRequiresReconciliation(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := workflowOperationSetup(t, store)
		op := f.start(t)
		ctx := context.Background()
		run, err := store.ClaimNextDueWorkflowRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "collect", 1, []byte(`{"count":1}`)); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "collect", state.WorkflowStepStatusSucceeded, 1, nil, []byte(`{"rows":[1]}`), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "transform", 1, []byte(`{"rows":[1]}`)); err != nil {
			t.Fatal(err)
		}
		if err := store.RecoverWorkflowRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		if uncertain := f.read(ctx, t, op.ID); uncertain.State != api.OperationRequiresReconciliation || uncertain.Progress.Completed != 1 {
			t.Fatalf("interrupted action was not reconciled: %+v", uncertain)
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "transform", state.WorkflowStepStatusSucceeded, 1, nil, []byte(`{"rows":[9]}`), nil); !errors.Is(err, state.ErrWorkflowOutboundAttemptExpired) {
			t.Fatal("interrupted attempt accepted late output", err)
		}
		calls := 0
		executor := operationWorkflowExecutor{execute: func(context.Context, sched.WorkflowStepIdentity, string, map[string]string, []byte) (int, []byte, error) {
			calls++
			return 200, []byte(`{"file":"unexpected.csv"}`), nil
		}}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil || calls != 0 {
			t.Fatal("scheduler repeated unknown effect", calls, err)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "transform")
		if err != nil || len(attempts) != 1 || attempts[0].Status != state.WorkflowAttemptStatusFailed || attempts[0].FinishedAt == nil {
			t.Fatalf("uncertain attempt evidence lost: %+v %v", attempts, err)
		}
		resolved, err := f.ops.RecoverOperation(ctx, f.account.ID, "", op.ID, api.OperationRecoveryRequest{RecoveryID: "provider-confirmed", ExpectedGeneration: 1, Resolution: "succeeded", Evidence: "provider returned the previously generated export", Result: []byte(`{"file":"retained.csv"}`)})
		if err != nil || resolved.State != api.OperationSucceeded || resolved.WorkflowRunID != run.ID || calls != 0 {
			t.Fatalf("confirmed result regenerated workflow: %+v %v", resolved, err)
		}
	})
}

func TestOperationWorkflowInvalidOutputDoesNotRestartSuccessfulRun(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := workflowOperationSetup(t, store)
		op := f.start(t)
		ctx := context.Background()
		calls := 0
		executor := operationWorkflowExecutor{execute: func(context.Context, sched.WorkflowStepIdentity, string, map[string]string, []byte) (int, []byte, error) {
			calls++
			return 200, []byte(`{"rows":[1]}`), nil
		}}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		result := f.read(ctx, t, op.ID)
		if result.State != api.OperationRequiresReconciliation || result.FailureCode != "invalid_output" || calls != 3 || result.CompletionDelivery.State != "awaiting_outcome" {
			t.Fatalf("invalid output projection=%+v calls=%d", result, calls)
		}
		if _, err := f.ops.RecoverOperation(ctx, f.account.ID, "", op.ID, api.OperationRecoveryRequest{RecoveryID: "cannot-regenerate", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "output shape changed"}); !errors.Is(err, state.ErrWorkflowResumeConflict) {
			t.Fatal("successful workflow regenerated after output mismatch", err)
		}
	})
}
