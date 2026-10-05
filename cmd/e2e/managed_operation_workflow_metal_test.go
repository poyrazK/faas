//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

const managedWorkflowRecoveryWebhookSecret = "managed-workflow-native-webhook-secret"

type managedWorkflowWebhookReceipt struct {
	DeliveryID string
	Payload    api.OperationEffectPayload
	Err        error
}

// TestManagedOperationWorkflowMetal qualifies managed workflow recovery on a
// native Firecracker guest: a handler process exits after saving its operation
// response but before replying, then an in-place step retry replays that
// response under generation 2 and delivers its effect once. The guest fixture
// uses a local receipt to isolate guest lifecycle/recovery. Real PostgreSQL
// receipt commit and process-death replay are qualified by test-operation-sdk.
func TestManagedOperationWorkflowMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping native managed workflow qualification")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping native managed workflow qualification")
	}
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL required for native managed workflow qualification")
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("native managed workflow qualification cannot disable PostgreSQL tests")
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "managed workflow recovery fixture")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate webhook identity: %v", err)
	}
	identityPath := filepath.Join(t.TempDir(), "host.age")
	if err := secretbox.WriteHostKeyAtPath(identityPath, identity); err != nil {
		t.Fatalf("write webhook identity: %v", err)
	}
	webhookReceipts := make(chan managedWorkflowWebhookReceipt, 4)
	var webhookRequests atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookRequests.Add(1)
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			webhookReceipts <- managedWorkflowWebhookReceipt{Err: readErr}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		deliveryID := r.Header.Get("X-Faas-Delivery-Id")
		timestamp, verifyErr := strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if verifyErr == nil {
			verifyErr = webhookout.NewSigner([]byte(managedWorkflowRecoveryWebhookSecret)).Verify(
				timestamp, deliveryID, body,
				strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256="),
			)
		}
		var event struct {
			Type string                     `json:"type"`
			Data api.OperationEffectPayload `json:"data"`
		}
		if verifyErr == nil {
			verifyErr = json.Unmarshal(body, &event)
		}
		if verifyErr == nil && event.Type != state.OperationEffectEvent {
			verifyErr = fmt.Errorf("event type = %q, want %q", event.Type, state.OperationEffectEvent)
		}
		webhookReceipts <- managedWorkflowWebhookReceipt{DeliveryID: deliveryID, Payload: event.Data, Err: verifyErr}
		if verifyErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	h := e2etest.Start(t, pool, e2etest.All,
		"FAAS_WORKFLOWS_ENABLED=1",
		"FAAS_EGRESS_ALLOW_LOOPBACK=1",
		"FAAS_HOST_AGE_IDENTITY_PATH="+identityPath,
	)
	defer h.DumpLogs(t)
	key := h.SeedAccount(context.Background(), api.PlanHobby, "managed-workflow-native")
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "managedwf", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "managedwf")
	app, err := state.NewPgStore(pool).AppByID(context.Background(), appID)
	if err != nil {
		t.Fatalf("load app: %v", err)
	}
	sealedSecret, err := secretbox.SealBytes(identity.Recipient(), "APP_WEBHOOK", []byte(managedWorkflowRecoveryWebhookSecret), api.AppWebhookSecretMaxBytes)
	if err != nil {
		t.Fatalf("seal webhook secret: %v", err)
	}
	store := state.NewPgStore(pool)
	hook, err := store.CreateAppWebhook(context.Background(), state.AppWebhook{
		Scope: state.AppWebhookScopeApp, AccountID: app.AccountID, AppID: app.ID,
		TargetURL: receiver.URL, SecretSealed: sealedSecret,
		EventFilter: []string{state.OperationEffectEvent}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create operation.effect webhook: %v", err)
	}

	deployBody, deployStatus := postMultipartDeployment(t, h, key, "managedwf", ManagedWorkflowRecoveryFixture(t, hook.ID), false, fixedIdempotencyKey())
	if deployStatus != http.StatusAccepted {
		t.Fatalf("create source deployment: status=%d body=%s", deployStatus, deployBody)
	}
	deploymentID, _ := parseQueuedDeployment(t, deployBody)
	deployCtx, deployCancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
	defer deployCancel()
	if _, _, err := e2etest.WaitForSourceDeployment(deployCtx, t, pool, deploymentID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("source deployment %s did not reach live: %v", deploymentID, err)
	}

	runBody, runStatus := doReq(t, h, key, http.MethodPost,
		"/v1/apps/managedwf/workflows/fulfill_order/runs",
		map[string]any{"order_id": "ord-native-recovery"},
	)
	if runStatus != http.StatusCreated {
		t.Fatalf("create workflow run: status=%d body=%s", runStatus, runBody)
	}
	var accepted api.WorkflowRunResponse
	if err := json.Unmarshal(runBody, &accepted); err != nil {
		t.Fatalf("decode accepted workflow run: %v body=%s", err, runBody)
	}
	if accepted.ID == "" {
		t.Fatal("workflow run response omitted id")
	}

	failedRun := waitForManagedWorkflowTerminal(t, store, accepted.ID, 5*time.Minute)
	if failedRun.Status != state.WorkflowRunStatusFailed && failedRun.Status != state.WorkflowRunStatusDead {
		t.Fatalf("first managed operation unexpectedly completed: status=%s", failedRun.Status)
	}
	firstAttempts, err := store.GetWorkflowStepAttempts(context.Background(), accepted.ID, "commit_order")
	if err != nil {
		t.Fatalf("load first workflow attempt: %v", err)
	}
	if len(firstAttempts) != 1 || (firstAttempts[0].Status != state.WorkflowStepStatusFailed && firstAttempts[0].Status != state.WorkflowStepStatusDead) {
		t.Fatalf("first attempt = %+v, want one failed guest invocation", firstAttempts)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(context.Background(), app.ID, hook.ID, 10, "")
	if err != nil {
		t.Fatalf("list deliveries after process death: %v", err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("process-death attempt emitted %d effects before workflow acceptance", len(deliveries))
	}

	retryPath := fmt.Sprintf("/v1/workflows/runs/%s/steps/commit_order/retry", accepted.ID)
	retryBody, retryStatus := doReq(t, h, key, http.MethodPost, retryPath, nil)
	if retryStatus != http.StatusOK {
		t.Fatalf("retry managed workflow step: status=%d body=%s", retryStatus, retryBody)
	}
	completedRun := waitForManagedWorkflowStatus(t, store, accepted.ID, state.WorkflowRunStatusSucceeded, 5*time.Minute)
	if completedRun.ID != accepted.ID {
		t.Fatalf("retry changed run ID: got=%s want=%s", completedRun.ID, accepted.ID)
	}
	attempts, err := store.GetWorkflowStepAttempts(context.Background(), accepted.ID, "commit_order")
	if err != nil {
		t.Fatalf("load recovered attempts: %v", err)
	}
	if len(attempts) != 2 || attempts[0].Attempt != 1 || attempts[1].Attempt != 2 ||
		(attempts[0].Status != state.WorkflowStepStatusFailed && attempts[0].Status != state.WorkflowStepStatusDead) ||
		attempts[1].Status != state.WorkflowStepStatusSucceeded {
		t.Fatalf("recovered attempts = %+v, want failed generation 1 then successful generation 2", attempts)
	}
	steps, err := store.GetWorkflowSteps(context.Background(), accepted.ID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("recovered workflow steps = %+v, err=%v", steps, err)
	}
	if steps[0].Status != state.WorkflowStepStatusSucceeded || string(steps[0].Output) != `{"order_id":"ord-native-recovery","status":"fulfilled"}` {
		t.Fatalf("workflow step result = status %s output %s", steps[0].Status, steps[0].Output)
	}
	if len(attempts[0].Effects) != 0 || len(attempts[1].Effects) != 1 {
		t.Fatalf("effects by attempt = [%d, %d], want none before acceptance and one on generation 2", len(attempts[0].Effects), len(attempts[1].Effects))
	}
	expectedOperationID, err := api.ManagedWorkflowStepOperationID(accepted.ID, "commit_order")
	if err != nil {
		t.Fatalf("derive managed workflow operation ID: %v", err)
	}
	effect := attempts[1].Effects[0]
	if effect.Generation != 2 || effect.ID == "" || effect.ID != effect.DeliveryID || effect.WebhookID != hook.ID || effect.Type != "order.fulfilled" {
		t.Fatalf("recovered operation effect = %+v, want generation 2, stable operation ID, and configured receiver", effect)
	}

	var webhookReceipt managedWorkflowWebhookReceipt
	select {
	case webhookReceipt = <-webhookReceipts:
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for signed operation.effect webhook; delivery state: %+v", deliveries)
	}
	if webhookReceipt.Err != nil {
		t.Fatalf("verify signed operation.effect webhook: %v", webhookReceipt.Err)
	}
	if webhookReceipt.DeliveryID != effect.DeliveryID || webhookReceipt.Payload.OperationID != expectedOperationID ||
		webhookReceipt.Payload.Generation != 2 || webhookReceipt.Payload.Name != "customer-notification" ||
		webhookReceipt.Payload.Type != "order.fulfilled" ||
		string(webhookReceipt.Payload.Data) != `{"order_id":"ord-native-recovery","status":"fulfilled"}` {
		t.Fatalf("delivered webhook = %+v delivery=%s, want the committed generation-2 effect", webhookReceipt.Payload, webhookReceipt.DeliveryID)
	}
	if err := waitForManagedWorkflowDelivery(t, store, app.ID, hook.ID, effect.DeliveryID, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	deliveries, _, err = store.ListAppWebhookDeliveries(context.Background(), app.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].ID != effect.DeliveryID || deliveries[0].Status != state.AppWebhookDeliverySucceeded {
		t.Fatalf("operation.effect delivery ledger = %+v err=%v, want one succeeded delivery", deliveries, err)
	}
	if got := webhookRequests.Load(); got != 1 {
		t.Fatalf("receiver got %d webhook requests, want one stable delivery", got)
	}
	t.Logf("managed workflow recovery passed: run=%s operation=%s generations=1→2 effect_delivery=%s", accepted.ID, expectedOperationID, effect.DeliveryID)
}

func waitForManagedWorkflowTerminal(t *testing.T, store *state.PgStore, runID string, timeout time.Duration) *state.WorkflowRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, err := store.GetWorkflowRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("get workflow run %s: %v", runID, err)
		}
		if run.Status == state.WorkflowRunStatusSucceeded || run.Status == state.WorkflowRunStatusFailed || run.Status == state.WorkflowRunStatusDead {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("workflow run %s did not become terminal within %s (status=%s)", runID, timeout, run.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForManagedWorkflowStatus(t *testing.T, store *state.PgStore, runID, want string, timeout time.Duration) *state.WorkflowRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, err := store.GetWorkflowRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("get workflow run %s: %v", runID, err)
		}
		if run.Status == want {
			return run
		}
		if run.Status == state.WorkflowRunStatusFailed || run.Status == state.WorkflowRunStatusDead {
			t.Fatalf("workflow run %s ended %s, want %s: %v", runID, run.Status, want, run.LastError)
		}
		if time.Now().After(deadline) {
			t.Fatalf("workflow run %s did not reach %s within %s (status=%s)", runID, want, timeout, run.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForManagedWorkflowDelivery(t *testing.T, store *state.PgStore, appID, webhookID, deliveryID string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		deliveries, _, err := store.ListAppWebhookDeliveries(context.Background(), appID, webhookID, 10, "")
		if err != nil {
			return fmt.Errorf("list operation.effect deliveries: %w", err)
		}
		if len(deliveries) > 1 {
			return fmt.Errorf("got %d operation.effect delivery rows, want exactly one", len(deliveries))
		}
		if len(deliveries) == 1 && deliveries[0].ID == deliveryID && deliveries[0].Status == state.AppWebhookDeliverySucceeded {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("delivery %s did not succeed within %s: %+v", deliveryID, timeout, deliveries)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
