package acceptance_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

// The SDK acceptance gate builds the packaged Node SDK and requires these
// tests. Ordinary Go-only test runs do not require a Node toolchain.
func testCustomerOperationCommittedReceipt(t *testing.T, s operationLifecycleStore) {
	t.Helper()
	ctx, acct, app, original, alice, _ := operationFixture(t, s)
	business := pgtest.OpenDatabase(t) // a separate customer database, never a platform transaction
	businessURL, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil || (businessURL.Scheme != "postgres" && businessURL.Scheme != "postgresql") {
		t.Fatal("Customer Operations SDK acceptance requires a PostgreSQL URL")
	}
	// pgx ConnString retains the original DSN after OpenDatabase changes the
	// connection config. Explicitly address the private customer database.
	businessURL.Path, businessURL.RawPath = "/"+business.Config().ConnConfig.Database, ""
	schema, err := os.ReadFile("../../../pkg/operationinbox/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := business.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	customerSchema, err := os.ReadFile("../../../pkg/operationinbox/customer_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := business.Exec(ctx, string(customerSchema)); err != nil {
		t.Fatal(err)
	}
	if _, err := business.Exec(ctx, "CREATE SCHEMA business; CREATE TABLE business.counter(id integer PRIMARY KEY,total integer NOT NULL); INSERT INTO business.counter VALUES(1,0)"); err != nil {
		t.Fatal(err)
	}
	hook, err := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: acct.ID, AppID: app.ID, TargetURL: "https://receiver.example.test/completed", SecretSealed: []byte("test-sealed"), EventFilter: []string{string(state.AppWebhookEventOperationFinished)}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := operationSpec()
	spec.Name, spec.Path = "transaction-export", "/transaction-exports"
	spec.TransactionReceipt, spec.CompletionWebhookID = api.OperationTransactionPostgres, hook.ID
	spec.OutputSchema = []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"},"value":{"type":"number"},"label":{"type":"string"}},"additionalProperties":false}`)
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: original.Scope, DeploymentID: original.DeploymentID, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: "committed-receipt", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimInvocation(ctx, op.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(inv state.Invocation, mode string) ([]byte, error) {
		t.Helper()
		var headers map[string]string
		if err := json.Unmarshal(inv.Headers, &headers); err != nil {
			t.Fatal(err)
		}
		// MemStore uses undashed account/app UUID text. Guest scope headers use
		// the canonical UUID spelling accepted by every SDK and PostgreSQL.
		headers[api.TenantIDHeader], headers[api.AppIDHeader], headers[api.PlatformTenantIDHeader], headers[api.InvocationIDHeader] = uuid.MustParse(acct.ID).String(), uuid.MustParse(app.ID).String(), alice.ID, inv.ID
		raw, err := json.Marshal(map[string]any{"headers": headers, "method": inv.Method, "path": inv.Path, "body_base64": base64.StdEncoding.EncodeToString(inv.Payload)})
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "request.json")
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, "node", "../../../sdk/node/test/fixtures/operation-interop.mjs")
		command.Env = append(os.Environ(), "OPERATION_CROSS_DATABASE_URL="+businessURL.String(), "OPERATION_REQUEST_FILE="+file, "OPERATION_MODE="+mode)
		return command.CombinedOutput()
	}
	if output, err := invoke(first, "committed-crash"); err == nil {
		t.Fatalf("worker did not crash after COMMIT: %s", output)
	} else {
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("worker failed before expected committed crash: %s %v", output, err)
		}
	}
	counts := func() {
		t.Helper()
		var total, receipts int
		err := business.QueryRow(ctx, "SELECT (SELECT total FROM business.counter WHERE id=1),(SELECT count(*) FROM public.gregale_customer_operation_inbox)").Scan(&total, &receipts)
		if err != nil || total != 1 || receipts != 1 {
			t.Fatalf("business total=%d receipts=%d err=%v", total, receipts, err)
		}
	}
	counts()
	if err := s.FailInvocation(ctx, first.ID, "response lost after customer COMMIT", time.Second, 10, state.WithClaimAttempt(first.Attempts)); err != nil {
		t.Fatal(err)
	}
	lost, err := s.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || lost.State != api.OperationRequiresReconciliation || lost.CompletionDelivery.State != "awaiting_outcome" {
		t.Fatalf("receipt incorrectly certified platform completion: %+v %v", lost, err)
	}
	recovered, err := s.RecoverOperation(ctx, acct.ID, "", op.ID, api.OperationRecoveryRequest{RecoveryID: "approved-receipt-replay", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified captured handler uses only the PostgreSQL transaction adapter and retains its scoped receipt; external effects are excluded"})
	if err != nil || recovered.Generation != 2 {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	second, err := s.ClaimInvocation(ctx, recovered.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]string
	_ = json.Unmarshal(first.Headers, &before)
	_ = json.Unmarshal(second.Headers, &after)
	if before[api.OperationReceiptBindingHeader] == "" || after[api.OperationReceiptBindingHeader] != before[api.OperationReceiptBindingHeader] {
		t.Fatal("approved recovery changed immutable receipt binding")
	}
	output, err := invoke(second, "replay") // callback throws if executed
	if err != nil {
		t.Fatalf("replay: %s %v", output, err)
	}
	var result struct {
		Body     string `json:"body"`
		Replayed bool   `json:"replayed"`
	}
	if err := json.Unmarshal(output, &result); err != nil || !result.Replayed {
		t.Fatalf("receipt not replayed: %s %v", output, err)
	}
	if err := s.CompleteKeyedInvocation(ctx, first.ID, first.Attempts, []byte(result.Body)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old execution completed recovered work: %v", err)
	}
	if err := s.CompleteKeyedInvocation(ctx, second.ID, second.Attempts, []byte(result.Body)); err != nil {
		t.Fatal(err)
	}
	savedFingerprint, err := operations.InputFingerprint([]byte(result.Body))
	if err != nil {
		t.Fatal(err)
	}
	sameResult := func(raw json.RawMessage) bool {
		fingerprint, err := operations.InputFingerprint(raw)
		return err == nil && fingerprint == savedFingerprint
	}
	done, err := s.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || done.State != api.OperationSucceeded || !sameResult(done.Result) || done.CompletionDelivery.State != "pending" {
		t.Fatalf("business result/delivery: %+v %v", done, err)
	}
	if err := s.CompleteKeyedInvocation(ctx, second.ID, second.Attempts, []byte(result.Body)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("duplicate completion: %v", err)
	}
	deliveries, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now().Add(time.Second))
	if err != nil || len(deliveries) != 1 || deliveries[0].ID != done.CompletionDelivery.DeliveryID {
		t.Fatalf("logical completion delivery duplicated: %+v %v", deliveries, err)
	}
	delivery := deliveries[0]
	if err := s.MarkAppWebhookDeliveryFailed(ctx, delivery.ID, 503, delivery.Attempt, delivery.NextAttemptAt, "receiver unavailable", time.Now()); err != nil {
		t.Fatal(err)
	}
	deliveries, err = s.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now().Add(time.Hour))
	if err != nil || len(deliveries) != 1 || deliveries[0].ID != delivery.ID {
		t.Fatalf("delivery retry regenerated completion: %+v %v", deliveries, err)
	}
	done, err = s.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || done.State != api.OperationSucceeded || !sameResult(done.Result) {
		t.Fatalf("delivery failure changed business result: %+v %v", done, err)
	}
	counts()
}

func TestMemCustomerOperationCommittedReceipt(t *testing.T) {
	if os.Getenv("GREGALE_CUSTOMER_OPERATION_SDK") == "" {
		t.Skip("run make test-customer-operation-sdk")
	}
	testCustomerOperationCommittedReceipt(t, state.NewMemStore())
}

func TestPgCustomerOperationCommittedReceipt(t *testing.T) {
	if os.Getenv("GREGALE_CUSTOMER_OPERATION_SDK") == "" {
		t.Skip("run make test-customer-operation-sdk")
	}
	s, _ := pgStore(t)
	testCustomerOperationCommittedReceipt(t, s)
}
