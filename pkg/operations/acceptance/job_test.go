// adr: 602
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type jobOperationFixture struct {
	store     state.Store
	ops       state.OperationStore
	runtime   state.JobOperationStore
	account   state.Account
	tenant    state.PlatformTenant
	other     state.PlatformTenant
	def       state.OperationDefinition
	job       state.Job
	admission state.OperationAdmission
}

func jobOperationSetup(t *testing.T, store state.Store) jobOperationFixture {
	t.Helper()
	ctx, account, _, base, tenant, other := operationFixture(t, store.(operationFixtureStore))
	job, err := store.JobCreate(ctx, account.ID, "export-job", "batch", "registry.example/export:v1", []string{"node", "export.mjs"}, 128, 60, 1, 3, json.RawMessage(`{"VERSION":"original"}`))
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.(state.JobImageMaterializationStore).JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", "")
	if err != nil {
		t.Fatal(err)
	}
	spec := operationSpec()
	spec.Name, spec.Path, spec.Job = "job-export", "/job-exports", job.Name
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: account.ID, AppID: base.AppID, TargetURL: "https://completion.example.test/events", Enabled: true, SecretSealed: []byte("sealed"), EventFilter: []string{string(state.AppWebhookEventOperationFinished)}})
	if err != nil {
		t.Fatal(err)
	}
	spec.CompletionWebhookID = hook.ID
	base.ID = ""
	base.Spec = spec
	base.Revision = ""
	ops := store.(state.OperationStore)
	def, err := ops.PutOperationDefinition(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	return jobOperationFixture{store: store, ops: ops, runtime: store.(state.JobOperationStore), account: account, tenant: tenant, other: other, def: def, job: job, admission: state.OperationAdmission{AccountID: account.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "customer-export", Input: []byte(`{"count":1}`)}}
}
func (f jobOperationFixture) start(t *testing.T) state.Operation {
	t.Helper()
	op, fresh, err := f.ops.AdmitOperation(context.Background(), f.admission)
	if err != nil || !fresh {
		t.Fatalf("admit=%+v fresh=%v %v", op, fresh, err)
	}
	if op.CurrentInvocationID != "" || op.WorkflowRunID != "" || op.JobRunID == "" {
		t.Fatal("mixed execution family")
	}
	return op
}
func (f jobOperationFixture) claim(t *testing.T, op state.Operation, expiry time.Time) (state.JobOperationAuthority, string) {
	t.Helper()
	ctx := context.Background()
	instanceID, lease := uuid.NewString(), uuid.NewString()
	node, err := f.store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateAndClaimJobInstance(ctx, instanceID, f.job.ID, op.JobRunID, 0, string(state.StateColdBooting), 128, node.ID, "", lease, expiry, "test-node"); err != nil {
		t.Fatal(err)
	}
	env, err := f.runtime.OperationJobDispatchEnv(ctx, op.JobRunID, instanceID, lease)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(env["GREGALE_CUSTOMER_OPERATION_INPUT"], " ", "") != `{"count":1}` || env["GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY"] == lease {
		t.Fatal("input or capability invalid")
	}
	return state.JobOperationAuthority{RunID: op.JobRunID, InstanceID: instanceID, Generation: op.Generation, Attempt: 1, Capability: env["GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY"]}, lease
}
func TestOperationJobAtomicAdmissionAndResult(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := jobOperationSetup(t, store)
		ctx := context.Background()
		var wg sync.WaitGroup
		ops := make(chan state.Operation, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				op, _, err := f.ops.AdmitOperation(ctx, f.admission)
				if err != nil {
					t.Error(err)
					return
				}
				ops <- op
			}()
		}
		wg.Wait()
		close(ops)
		var op state.Operation
		for got := range ops {
			if op.ID == "" {
				op = got
			}
			if got.ID != op.ID || got.JobRunID != op.JobRunID {
				t.Fatal("duplicate operation/run")
			}
		}
		run, err := store.JobRunGetByID(ctx, op.JobRunID)
		if err != nil || run.Tasks != 1 || *run.RetryMax != 0 {
			t.Fatalf("run=%+v %v", run, err)
		}
		changed := f.admission
		changed.Input = []byte(`{"count":2}`)
		if _, _, err := f.ops.AdmitOperation(ctx, changed); !errors.Is(err, state.ErrOperationInputConflict) {
			t.Fatal(err)
		}
		if _, err := f.ops.OperationByID(ctx, f.account.ID, f.other.ID, op.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-customer read")
		}
		image := "registry.example/export:v2"
		if _, err := store.JobUpdate(ctx, f.job.ID, []string{"replacement"}, &image, nil, nil, nil, nil, []byte(`{"VERSION":"replacement"}`), nil); err != nil {
			t.Fatal(err)
		}
		candidates, err := store.JobTaskClaimBatch(ctx, 10)
		if err != nil || len(candidates) != 1 || candidates[0].RunID != op.JobRunID {
			t.Fatalf("frozen candidate=%+v %v", candidates, err)
		}
		repeated, fresh, err := f.ops.AdmitOperation(ctx, f.admission)
		if err != nil || fresh || repeated.ID != op.ID || repeated.JobRunID != op.JobRunID {
			t.Fatal("lost response changed accepted job", err)
		}
		historyBefore, err := f.ops.OperationExecutions(ctx, f.account.ID, op.ID, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		a, lease := f.claim(t, op, time.Now().Add(time.Minute))
		got, err := f.runtime.ReportOperationJob(ctx, op.ID, a, "progress", api.OperationJobReportRequest{ReportID: "working", Progress: &api.OperationReportRequest{Stage: "generating", Completed: 1, Total: 1}})
		if err != nil || got.State != api.OperationRunning {
			t.Fatal(err)
		}
		report := api.OperationJobReportRequest{ReportID: "result", Result: []byte(`{"file":"obj://export.csv"}`)}
		got, err = f.runtime.ReportOperationJob(ctx, op.ID, a, "result", report)
		if err != nil || len(got.Result) > 0 || got.State != api.OperationRunning {
			t.Fatalf("result prematurely published: %+v %v", got, err)
		}
		if _, err := f.runtime.ReportOperationJob(ctx, op.ID, a, "result", report); err != nil {
			t.Fatal("lost receipt replay", err)
		}
		report.Result = []byte(`{"file":"changed.csv"}`)
		if _, err := f.runtime.ReportOperationJob(ctx, op.ID, a, "result", report); !errors.Is(err, state.ErrOperationInputConflict) {
			t.Fatal("result overwritten", err)
		}
		forged := a
		forged.Capability = strings.Repeat("0", 64)
		if _, err := f.runtime.OperationJobControl(ctx, op.ID, forged); !errors.Is(err, state.ErrOperationStaleAttempt) {
			t.Fatal("forged capability", err)
		}
		if err := store.JobTaskCompleteClaimedWithLogs(ctx, op.JobRunID, 0, a.InstanceID, lease, "succeeded", 0, "", "", "", false, time.Now()); err != nil {
			t.Fatal(err)
		}
		got, err = f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
		if err != nil || got.State != api.OperationSucceeded || strings.ReplaceAll(string(got.Result), " ", "") != `{"file":"obj://export.csv"}` {
			t.Fatalf("outcome=%+v %v", got, err)
		}
		if got.CompletionDelivery.State != "pending" || got.CompletionDelivery.DeliveryID == "" {
			t.Fatal("completion delivery missing")
		}
		deliveries, err := store.ClaimDueAppWebhookDeliveries(ctx, 1, time.Now().Add(time.Second))
		if err != nil || len(deliveries) != 1 {
			t.Fatal("outbox", err)
		}
		delivery := deliveries[0]
		if err := store.MarkAppWebhookDeliveryDead(ctx, delivery.ID, delivery.Attempt, delivery.NextAttemptAt, "receiver unavailable"); err != nil {
			t.Fatal(err)
		}
		business, _ := f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
		if business.State != api.OperationSucceeded || business.CompletionDelivery.State != "dead" {
			t.Fatal("notification changed business outcome")
		}
		if _, err := f.runtime.OperationJobControl(ctx, op.ID, a); !errors.Is(err, state.ErrOperationStaleAttempt) {
			t.Fatal("closed authority", err)
		}
		history, err := f.ops.OperationExecutions(ctx, f.account.ID, op.ID, 0, 10)
		if err != nil || len(history.Executions) != 1 || history.Executions[0].JobRunID != op.JobRunID || !history.Executions[0].CreatedAt.Equal(historyBefore.Executions[0].CreatedAt) {
			t.Fatalf("history=%+v %v", history, err)
		}
	})
}
func TestOperationJobUncertainRecoveryAndCancellation(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := jobOperationSetup(t, store)
		ctx := context.Background()
		op := f.start(t)
		expires := time.Now().Add(2 * time.Second)
		a, lease := f.claim(t, op, expires)
		image := "registry.example/export:v2"
		if _, err := store.JobUpdate(ctx, f.job.ID, []string{"replacement"}, &image, nil, nil, nil, nil, []byte(`{"VERSION":"replacement"}`), nil); err != nil {
			t.Fatal(err)
		}
		// Leave room for the claim and capability reads under the race
		// detector, then wait for the actual captured lease to expire.
		if remaining := time.Until(expires); remaining > 0 {
			time.Sleep(remaining + time.Millisecond)
		}
		if err := store.JobTaskCompleteClaimedWithLogs(ctx, op.JobRunID, 0, a.InstanceID, lease, "succeeded", 0, "", "", "", false, time.Now()); !errors.Is(err, state.ErrOperationStaleAttempt) {
			t.Fatal("expired completion", err)
		}
		retry, err := store.JobTaskReapClaimed(ctx, op.JobRunID, 0, lease, time.Now(), 10, time.Now())
		if err != nil || retry {
			t.Fatal("automatically repeated uncertain task", err)
		}
		got, _ := f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
		if got.State != api.OperationRequiresReconciliation {
			t.Fatalf("uncertain=%+v", got)
		}
		if err := store.JobTaskRetry(ctx, op.JobRunID, 0, time.Now()); !errors.Is(err, state.ErrConflict) {
			t.Fatal("retry bypass", err)
		}
		if _, _, err := store.JobRunReplayFailed(ctx, op.JobRunID, f.account.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("replay bypass", err)
		}
		inspector := store.(state.OperationRecoveryInspectionStore)
		preview, err := inspector.PreviewOperationRecovery(ctx, f.account.ID, op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "safe_to_retry"})
		if err != nil || !preview.Eligible || preview.Inspection.ExecutionKind != "job" {
			t.Fatalf("preview=%+v %v", preview, err)
		}
		req := api.OperationRecoveryRequest{RecoveryID: "checked-provider", ExpectedGeneration: 1, ExpectedInspectionRevision: preview.Inspection.InspectionRevision, Resolution: "safe_to_retry", Evidence: "provider verified no effect"}
		recovered, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, req)
		if err != nil || recovered.ID != op.ID || recovered.Generation != 2 || recovered.JobRunID == op.JobRunID {
			t.Fatalf("recovery=%+v %v", recovered, err)
		}
		replay, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, req)
		if err != nil || replay.JobRunID != recovered.JobRunID {
			t.Fatal("recovery duplicated run", err)
		}
		run, err := store.JobRunGetByID(ctx, recovered.JobRunID)
		if err != nil || run.Command[0] != "node" || run.ImageRefSnapshot != f.job.ImageRef || string(run.EffectiveEnvSnapshot) != string(op.JobSnapshot.EffectiveEnvSnapshot) {
			t.Fatalf("recovery lost snapshot=%+v %v", run, err)
		}
		if _, err := f.runtime.ReportOperationJob(ctx, op.ID, a, "result", api.OperationJobReportRequest{ReportID: "late", Result: []byte(`{"file":"late.csv"}`)}); !errors.Is(err, state.ErrOperationStaleAttempt) {
			t.Fatal("stale report", err)
		}
		if _, err := f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, op.ID, 2); err != nil {
			t.Fatal(err)
		}
		got, _ = f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
		if got.State != api.OperationCancelled {
			t.Fatal("queued cancellation", got.State)
		}
		retained, err := store.(state.OperationJobImageRetentionStore).OperationJobImageRetained(ctx, f.job.ImageStorageKey)
		if err != nil || !retained {
			t.Fatal("lost image pin", err)
		}
	})
}

func TestOperationJobRequiresResultAndCooperatesWithCancellation(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := jobOperationSetup(t, store)
		ctx := context.Background()
		op := f.start(t)
		a, lease := f.claim(t, op, time.Now().Add(time.Minute))
		if _, err := f.runtime.ReportOperationJob(ctx, op.ID, a, "result", api.OperationJobReportRequest{ReportID: "bad", Result: []byte(`{"file":42}`)}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid result accepted", err)
		}
		if _, err := f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, op.ID, 1); err != nil {
			t.Fatal(err)
		}
		control, err := f.runtime.OperationJobControl(ctx, op.ID, a)
		if err != nil || !control.CancellationRequested {
			t.Fatal("missing cancellation intent", err)
		}
		if _, err := f.runtime.ReportOperationJob(ctx, op.ID, a, "result", api.OperationJobReportRequest{ReportID: "result", Result: []byte(`{"file":"new.csv"}`)}); !errors.Is(err, state.ErrConflict) {
			t.Fatal("work continued after cancellation", err)
		}
		if err := store.JobTaskCompleteClaimedWithLogs(ctx, op.JobRunID, 0, a.InstanceID, lease, "succeeded", 0, "", "", "", false, time.Now()); err != nil {
			t.Fatal(err)
		}
		got, _ := f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
		if got.State != api.OperationRequiresReconciliation || got.FailureCode != "job_result_missing" {
			t.Fatal("exit zero without result became success", got.State)
		}
		preview, err := store.(state.OperationRecoveryInspectionStore).PreviewOperationRecovery(ctx, f.account.ID, op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "succeeded", Result: []byte(`{"file":"confirmed.csv"}`)})
		if err != nil || !preview.Eligible {
			t.Fatal("confirmed outcome blocked", err)
		}
		req := api.OperationRecoveryRequest{RecoveryID: "provider-receipt", ExpectedGeneration: 1, Resolution: "succeeded", Evidence: "provider confirmed export", Result: []byte(`{"file":"confirmed.csv"}`), ExpectedInspectionRevision: preview.Inspection.InspectionRevision}
		resolved, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, req)
		if err != nil || resolved.State != api.OperationSucceeded || resolved.JobRunID != op.JobRunID {
			t.Fatal("manual resolution lost execution", err)
		}
	})
}

func TestOperationJobAdmissionBoundsAndSuspendedOwner(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := jobOperationSetup(t, store)
		ctx := context.Background()
		wide := f.def
		wide.ID = ""
		wide.Revision = ""
		wide.Spec.Name = "wide-export"
		wide.Spec.Path = "/wide-exports"
		wide.Spec.InputSchema = []byte(`{"type":"object"}`)
		wide, err := f.ops.PutOperationDefinition(ctx, wide)
		if err != nil {
			t.Fatal(err)
		}
		request := f.admission
		request.DefinitionID = wide.ID
		request.Input = []byte(`{"padding":"` + strings.Repeat("x", api.OperationJobInputMaxBytes) + `"}`)
		if _, _, err := f.ops.AdmitOperation(ctx, request); !errors.Is(err, state.ErrOperationQuota) {
			t.Fatal("oversize input admitted", err)
		}
		if tasks, err := store.JobTaskClaimBatch(ctx, 10); err != nil || len(tasks) != 0 {
			t.Fatal("rejected admission left work", err)
		}
		op := f.start(t)
		if _, err := store.(state.PlatformTenantStore).SetPlatformTenantStatus(ctx, f.account.ID, f.tenant.ID, state.PlatformTenantSuspended); err != nil {
			t.Fatal(err)
		}
		node, _ := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
		if _, err := store.CreateAndClaimJobInstance(ctx, uuid.NewString(), f.job.ID, op.JobRunID, 0, string(state.StateColdBooting), 128, node.ID, "", uuid.NewString(), time.Now().Add(time.Minute), "node"); err == nil {
			t.Fatal("suspended customer dispatched")
		}
		current, _ := f.ops.OperationByID(ctx, f.account.ID, "", op.ID)
		if current.State != api.OperationAccepted {
			t.Fatal("denied claim changed operation")
		}
		if _, err := f.ops.CancelOperation(ctx, f.account.ID, "", op.ID, 1); err != nil {
			t.Fatal(err)
		}
		run, err := store.JobRunGetByID(ctx, op.JobRunID)
		if err != nil || run.AggregateStatus != "cancelled" {
			t.Fatal("queued cancellation lost native projection", err)
		}
	})
}

func TestOperationJobOwnerPurgeClosesQueuedWorkAndWaitsForClaim(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "claimed"}[claimed], func(t *testing.T) {
			operationWorkflowStores(t, func(t *testing.T, store state.Store) {
				f := jobOperationSetup(t, store)
				ctx := t.Context()
				op := f.start(t)
				if claimed {
					f.claim(t, op, time.Now().Add(time.Minute))
				}
				if _, err := store.ScheduleAppDeletion(ctx, f.def.AppID, time.Now().Add(-time.Second)); err != nil {
					t.Fatal(err)
				}
				if err := store.ClaimAppDeletion(ctx, f.def.AppID); err != nil {
					t.Fatal(err)
				}
				if claimed {
					if err := store.DeleteAppPermanently(ctx, f.def.AppID); !errors.Is(err, state.ErrConflict) {
						t.Fatal("purged leased operation", err)
					}
					if _, err := f.ops.OperationByID(ctx, f.account.ID, "", op.ID); err != nil {
						t.Fatal("blocked purge removed operation", err)
					}
					if err := store.JobTaskCancel(ctx, op.JobRunID, 0); err != nil {
						t.Fatal(err)
					}
				}
				if err := store.DeleteAppPermanently(ctx, f.def.AppID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.ops.OperationByID(ctx, f.account.ID, "", op.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("purged operation retained", err)
				}
				run, err := store.JobRunGetByID(ctx, op.JobRunID)
				if err != nil || run.AggregateStatus != "cancelled" {
					t.Fatal("purge left runnable native work", run, err)
				}
				if tasks, err := store.JobTaskClaimBatch(ctx, 10); err != nil || len(tasks) != 0 {
					t.Fatal("purged operation became ordinary work", tasks, err)
				}
			})
		})
	}
}
