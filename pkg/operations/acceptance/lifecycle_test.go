package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationLifecycleStore interface {
	operationFixtureStore
	ClaimInvocation(context.Context, string, string, int) (state.Invocation, error)
	ClaimInvocationWithCap(context.Context, string, string, int, int) (state.Invocation, error)
	CompleteInvocation(context.Context, string, json.RawMessage) error
	CompleteKeyedInvocation(context.Context, string, int, json.RawMessage) error
	FailInvocation(context.Context, string, string, time.Duration, int, ...state.FailOption) error
	RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
	CancelInvocation(context.Context, string) error
	ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
	CreateInstance(context.Context, string, string, string, int, string, string) (state.Instance, error)
	CreateAppWebhook(context.Context, state.AppWebhook) (state.AppWebhook, error)
	UpdateAppWebhook(context.Context, string, state.UpdateAppWebhookParams) (state.AppWebhook, error)
	ClaimDueAppWebhookDeliveries(context.Context, int, time.Time) ([]state.AppWebhookDelivery, error)
	MarkAppWebhookDeliveryFailed(context.Context, string, int, int, time.Time, string, time.Time, ...state.AppWebhookAttemptMetadata) error
	MarkAppWebhookDeliverySucceeded(context.Context, string, int, int, time.Time, time.Time, ...state.AppWebhookAttemptMetadata) error
}

func testOperationLifecycle(t *testing.T, s operationLifecycleStore) {
	t.Helper()
	ctx, acct, app, definition, alice, _ := operationFixture(t, s)
	nodeID := uuid.NewString()
	if node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
		nodeID = node.ID
	}
	instance, err := s.CreateInstance(ctx, app.ID, definition.DeploymentID, "stopped", 128, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	define := func(spec api.OperationDefinitionSpec) state.OperationDefinition {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:operations", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: spec}})
		if err != nil {
			t.Fatal(err)
		}
		return def
	}
	admit := func(def state.OperationDefinition, key string) state.Operation {
		t.Helper()
		op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: key, Input: []byte(`{"count":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	claim := func(op state.Operation) (state.Invocation, state.OperationExecutionAuthority) {
		t.Helper()
		inv, err := s.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
		if err != nil {
			t.Fatal(err)
		}
		var headers map[string]string
		if err := json.Unmarshal(inv.Headers, &headers); err != nil {
			t.Fatal(err)
		}
		if !state.InvocationHasOperation(inv) || headers[api.OperationIDHeader] != op.ID || headers[api.OperationCapabilityHeader] == "" {
			t.Fatalf("missing ordinary HTTP operation context: %+v", inv)
		}
		stored, err := s.InvocationByID(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		var persisted map[string]string
		_ = json.Unmarshal(stored.Headers, &persisted)
		if persisted[api.OperationCapabilityHeader] != "" {
			t.Fatal("raw execution capability persisted in invocation")
		}
		wire := inv
		wire.OperationID, wire.AccountID = "", ""
		wire.Path = "/forged-wire-target"
		dispatched, err := state.AdmitPlatformTenantInvocation(ctx, s, app.ID, wire)
		if err != nil || dispatched.OperationID != op.ID || dispatched.Path != inv.Path || string(dispatched.Payload) != string(inv.Payload) {
			t.Fatalf("operation context lost through synthetic transport: %+v %v", dispatched, err)
		}
		var forwarded map[string]string
		_ = json.Unmarshal(dispatched.Headers, &forwarded)
		if forwarded[api.OperationCapabilityHeader] != headers[api.OperationCapabilityHeader] {
			t.Fatal("ephemeral claim proof was dropped during durable request reload")
		}
		proofless := wire
		proofless.Headers = stored.Headers
		if _, err := state.AdmitPlatformTenantInvocation(ctx, s, app.ID, proofless); err == nil {
			t.Fatal("synthetic delivery accepted missing operation claim proof")
		}
		return inv, state.OperationExecutionAuthority{AccountID: acct.ID, AppID: app.ID, InstanceID: instance.ID, InvocationID: inv.ID, Attempt: inv.Attempts, Capability: headers[api.OperationCapabilityHeader]}
	}
	read := func(op state.Operation) state.Operation {
		t.Helper()
		got, err := s.OperationByID(ctx, acct.ID, alice.ID, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	op := admit(definition, "progress")
	inv, authority := claim(op)
	report := api.OperationReportRequest{ReportID: "chunk-1", Stage: "generating", Completed: 1, Total: 10}
	progress, err := s.ReportOperationProgress(ctx, op.ID, authority, report)
	if err != nil || progress.State != api.OperationRunning || progress.Progress.Completed != 1 {
		t.Fatalf("progress: %+v %v", progress, err)
	}
	replayed, err := s.ReportOperationProgress(ctx, op.ID, authority, report)
	if err != nil || replayed.LatestSequence != progress.LatestSequence {
		t.Fatalf("report duplicate appended another event: %+v %v", replayed, err)
	}
	conflict := report
	conflict.Completed = 2
	if _, err := s.ReportOperationProgress(ctx, op.ID, authority, conflict); !errors.Is(err, state.ErrOperationInputConflict) {
		t.Fatalf("conflicting report accepted: %v", err)
	}
	forged := authority
	forged.Capability = string(make([]byte, 64))
	if _, err := s.ReportOperationProgress(ctx, op.ID, forged, report); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("forged capability accepted: %v", err)
	}
	forged = authority
	forged.InstanceID = uuid.NewString()
	if _, err := s.ReportOperationProgress(ctx, op.ID, forged, report); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("other workload instance accepted: %v", err)
	}
	if err := s.CompleteInvocation(ctx, inv.ID, []byte(`{"file":"exports/customer.csv"}`)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unfenced completion accepted: %v", err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts+1, []byte(`{"file":"exports/customer.csv"}`)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale completion accepted: %v", err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, []byte(`{"file":"exports/customer.csv"}`)); err != nil {
		t.Fatal(err)
	}
	completed := read(op)
	canonicalResult, _ := operations.CanonicalJSON(completed.Result)
	if completed.State != api.OperationSucceeded || string(canonicalResult) != `{"file":"exports/customer.csv"}` {
		t.Fatalf("business result: %+v", completed)
	}
	if _, err := s.ReportOperationProgress(ctx, op.ID, authority, report); !errors.Is(err, state.ErrOperationStaleAttempt) {
		t.Fatalf("report after completion accepted: %v", err)
	}
	page, err := s.OperationEvents(ctx, acct.ID, alice.ID, op.ID, 0, 100)
	if err != nil || len(page.Events) != 4 {
		t.Fatalf("durable events: %+v %v", page, err)
	}
	for i, event := range page.Events {
		if event.Sequence != int64(i+1) {
			t.Fatalf("non-contiguous cursor: %+v", page)
		}
	}

	bad := admit(definition, "invalid-output")
	badInv, _ := claim(bad)
	if err := s.CompleteKeyedInvocation(ctx, badInv.ID, badInv.Attempts, []byte(`{"unexpected":true}`)); err != nil {
		t.Fatal(err)
	}
	if got := read(bad); got.State != api.OperationRequiresReconciliation || got.FailureCode != "invalid_output" {
		t.Fatalf("invalid output encouraged repeat work: %+v", got)
	}

	unknown := admit(definition, "lost-lease")
	unknownInv, _ := claim(unknown)
	if n, err := s.RequeueExpiredInvocations(ctx, time.Now().Add(2*time.Minute), 10); err != nil || n != 1 {
		t.Fatalf("expired dispatch: %d %v", n, err)
	}
	if got := read(unknown); got.State != api.OperationRequiresReconciliation {
		t.Fatalf("lost dispatch blindly repeated: %+v", got)
	}
	if _, err := s.ClaimInvocation(ctx, unknownInv.ID, instance.ID, 60); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("uncertain effect reclaimed: %v", err)
	}

	pre := admit(definition, "pre-dispatch")
	preInv, oldAuthority := claim(pre)
	if err := s.FailInvocation(ctx, preInv.ID, "wake unavailable", time.Millisecond, 10, state.WithClaimAttempt(preInv.Attempts), state.WithDispatchNotStarted()); err != nil {
		t.Fatal(err)
	}
	retry, newAuthority := claim(pre)
	if retry.Attempts != 2 || oldAuthority.Capability == newAuthority.Capability {
		t.Fatal("new lease did not rotate reporting authority")
	}
	if _, err := s.ReportOperationProgress(ctx, pre.ID, oldAuthority, report); !errors.Is(err, state.ErrOperationStaleAttempt) {
		t.Fatalf("old execution report accepted: %v", err)
	}
	if err := s.FailInvocation(ctx, retry.ID, "connection lost after dispatch", time.Millisecond, 10, state.WithClaimAttempt(retry.Attempts)); err != nil {
		t.Fatal(err)
	}
	if got := read(pre); got.State != api.OperationRequiresReconciliation {
		t.Fatalf("unknown invoke failure retried: %+v", got)
	}

	cancel := admit(definition, "cancel-running")
	cancelInv, _ := claim(cancel)
	if err := s.CancelInvocation(ctx, cancelInv.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(cancel); !got.CancellationRequested || got.State != api.OperationRunning {
		t.Fatalf("cancellation claimed to undo external effects: %+v", got)
	}
	if err := s.CompleteKeyedInvocation(ctx, cancelInv.ID, cancelInv.Attempts, []byte(`{"file":"still-completed.csv"}`)); err != nil {
		t.Fatal(err)
	}
	if got := read(cancel); got.State != api.OperationSucceeded || !got.CancellationRequested {
		t.Fatalf("confirmed completion lost: %+v", got)
	}
	pending := admit(definition, "cancel-pending")
	if err := s.CancelInvocation(ctx, pending.CurrentInvocationID); err != nil {
		t.Fatal(err)
	}
	if got := read(pending); got.State != api.OperationCancelled {
		t.Fatalf("pending cancel: %+v", got)
	}

	safeSpec := operationSpec()
	safeSpec.Name = "safe-export"
	safeSpec.Recovery = api.OperationRecoverySafeRetry
	safeDef := define(safeSpec)
	safe := admit(safeDef, "safe-retry")
	safeInv, _ := claim(safe)
	if err := s.FailInvocation(ctx, safeInv.ID, "uncertain but developer declares replay safe", time.Millisecond, 10, state.WithClaimAttempt(safeInv.Attempts)); err != nil {
		t.Fatal(err)
	}
	if got := read(safe); got.State != api.OperationAccepted {
		t.Fatalf("explicit safe retry blocked: %+v", got)
	}
	safeInv, _ = claim(safe)
	if err := s.CompleteKeyedInvocation(ctx, safeInv.ID, safeInv.Attempts, []byte(`{"file":"safe.csv"}`)); err != nil {
		t.Fatal(err)
	}

	hook, err := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: acct.ID, AppID: app.ID, TargetURL: "https://receiver.example.test/completed", SecretSealed: []byte("test-sealed"), EventFilter: []string{string(state.AppWebhookEventOperationFinished)}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	deliverySpec := operationSpec()
	deliverySpec.Name = "delivered-export"
	deliverySpec.CompletionWebhookID = hook.ID
	deliveryDef := define(deliverySpec)
	delivered := admit(deliveryDef, "delivered")
	deliveredInv, _ := claim(delivered)
	if err := s.CompleteKeyedInvocation(ctx, deliveredInv.ID, deliveredInv.Attempts, []byte(`{"file":"ready.csv"}`)); err != nil {
		t.Fatal(err)
	}
	got := read(delivered)
	if got.State != api.OperationSucceeded || got.CompletionDelivery.State != "pending" || got.CompletionDelivery.DeliveryID == "" {
		t.Fatalf("completion outbox missing: %+v", got)
	}
	deliveries, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now().Add(time.Second))
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("completion claim: %+v %v", deliveries, err)
	}
	d := deliveries[0]
	if d.ID != got.CompletionDelivery.DeliveryID || d.Event != state.AppWebhookEventOperationFinished {
		t.Fatalf("delivery association: %+v", d)
	}
	if err := s.MarkAppWebhookDeliveryFailed(ctx, d.ID, 503, d.Attempt, d.NextAttemptAt, "receiver unavailable", time.Now()); err != nil {
		t.Fatal(err)
	}
	got = read(delivered)
	if got.State != api.OperationSucceeded || got.CompletionDelivery.Attempts != 1 || got.CompletionDelivery.LastError != "receiver unavailable" {
		t.Fatalf("notification failure changed business work: %+v", got)
	}
	deliveries, err = s.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now().Add(time.Hour))
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("completion retry: %+v %v", deliveries, err)
	}
	d = deliveries[0]
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, d.ID, 200, d.Attempt, d.NextAttemptAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := read(delivered); got.State != api.OperationSucceeded || got.CompletionDelivery.State != "succeeded" {
		t.Fatalf("completion delivered: %+v", got)
	}

	disabled := admit(deliveryDef, "disabled-destination")
	disabledInv, _ := claim(disabled)
	enabled := false
	if _, err := s.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteKeyedInvocation(ctx, disabledInv.ID, disabledInv.Attempts, []byte(`{"file":"already-generated.csv"}`)); err != nil {
		t.Fatal(err)
	}
	if got := read(disabled); got.State != api.OperationSucceeded || got.CompletionDelivery.State != "configuration_failed" {
		t.Fatalf("disabled completion erased success: %+v", got)
	}
}

func TestMemOperationLifecycle(t *testing.T) { testOperationLifecycle(t, state.NewMemStore()) }
func TestPgOperationLifecycle(t *testing.T)  { s, _ := pgStore(t); testOperationLifecycle(t, s) }
