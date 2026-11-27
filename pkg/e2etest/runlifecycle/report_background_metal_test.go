//go:build metal

package runlifecycle_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// The regional intake and completion receiver run in a trusted backend, with
// no traffic to the separately qualified Agent inbox function.
func TestSourceHostedBackgroundReportMetal(t *testing.T) {
	fixture := newHostedReportFixture(t, nil)
	exerciseHostedBackgroundReport(t, fixture.run, fixture.db, fixture.object, fixture.store, fixture.edge.Client(),
		fixture.backend, fixture.appID)
	fixture.run.assertSettled(t)
}

type hostedReportFixture struct {
	run     *runFixture
	db      *pgx.Conn
	object  *reportObjectFixture
	store   *state.PgStore
	edge    *httptest.Server
	backend hostedReportBackend
	appID   string
}

func newHostedReportFixture(t *testing.T, configureTask func(*api.PublishExecutionTaskRequest)) hostedReportFixture {
	t.Helper()
	ctx := t.Context()
	b := hostedReportBackend{node: requiredHostedEnv(t, "FAAS_TEST_REPORT_AGENT_NODE"),
		bundle: requiredHostedEnv(t, "FAAS_TEST_REPORT_BUNDLE"), dsn: requiredHostedEnv(t, "FAAS_TEST_REPORT_DB_DSN")}
	db, err := pgx.Connect(ctx, b.dsn)
	requireReport(t, err)
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	schema, err := os.ReadFile(filepath.Join(b.bundle, "report-db.sql"))
	requireReport(t, err)
	_, err = db.Exec(ctx, string(schema))
	requireReport(t, err)

	object := &reportObjectFixture{}
	storage := httptest.NewServer(http.HandlerFunc(object.serve))
	t.Cleanup(storage.Close)
	var edgeHandler atomic.Pointer[s3gateway.Handler]
	edge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler := edgeHandler.Load(); handler != nil {
			handler.ServeHTTP(w, r)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(edge.Close)
	config := hostedReportStorageConfig(storage.URL)
	config.PublicEndpoint = edge.URL
	_, apidEnv := hostedReportCredentials(t, config)
	t.Setenv("FAAS_RUN_TASK_QUALIFICATION", "1")
	f := startRunFixtureWithServices(t, false, apidEnv, func(string) []string {
		return []string{"FAAS_EGRESS_ALLOW_LOOPBACK=1"}
	}, 0)
	store := state.NewPgStore(f.h.Pool)
	edgeHandler.Store(hostedReportDownloadHandler(t, f, store, config))
	app, err := f.client.CreateApp(ctx, api.CreateAppRequest{Slug: hostedReportApp, Type: "function", Runtime: "node24"})
	requireReport(t, err)
	appRow, err := store.AppByID(ctx, app.ID)
	requireReport(t, err)
	seedReportDestination(t, f, store, appRow, config)
	endpoint, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: uuid.NewString(), AccountID: f.accountID, AppID: app.ID, CallbackURL: "https://example.test/report",
		ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect",
		CallbackAuthTokenSealed: []byte("fixture"), AuthTokenSealed: []byte("fixture"), Enabled: true,
	}, 10, 50)
	requireReport(t, err)
	publishReportFixtureTasks(t, f, b.node, endpoint.ID, configureTask)
	b.endpoint, b.base, b.token = endpoint.ID, f.h.APIDURL, reportBackendToken(t, store, f.accountID)
	return hostedReportFixture{run: f, db: db, object: object, store: store, edge: edge, backend: b, appID: app.ID}
}

type reportWebhookAttempt struct {
	deliveryID string
	event      api.ExecutionPipelineFinishedWebhookPayload
	headers    http.Header
	body       []byte
	pid        int
	status     int
	artifactID string
	at         time.Time
	err        error
}

func startBackgroundReportReceiver(t *testing.T, b hostedReportBackend) (*httptest.Server, <-chan reportWebhookAttempt, *atomic.Bool) {
	t.Helper()
	attempts := make(chan reportWebhookAttempt, 12)
	var count atomic.Int32
	var restart atomic.Bool
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/report-completion" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		first := count.Add(1) == 1
		if !first && !restart.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		attempt := invokeBackgroundReportReceiver(r, b, first)
		select {
		case attempts <- attempt:
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if attempt.err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if first && attempt.artifactID != "" {
			// The worker died after APID committed the copy and before the
			// receiver could persist the report or acknowledge the webhook.
			panic(http.ErrAbortHandler)
		}
		w.WriteHeader(attempt.status)
	}))
	t.Cleanup(receiver.Close)
	return receiver, attempts, &restart
}

func invokeBackgroundReportReceiver(r *http.Request, b hostedReportBackend, crash bool) (out reportWebhookAttempt) {
	out.headers = r.Header.Clone()
	out.deliveryID = r.Header.Get("X-Faas-Delivery-Id")
	out.body, out.err = io.ReadAll(io.LimitReader(r.Body, 16385))
	var event struct {
		ID   string
		Type string
		Data api.ExecutionPipelineFinishedWebhookPayload
	}
	if out.err != nil || len(out.body) > 16384 || json.Unmarshal(out.body, &event) != nil ||
		event.Type != api.ExecutionPipelineFinishedWebhookEvent || event.ID != out.deliveryID || !isHostedReportUUID(event.ID) {
		out.err = fmt.Errorf("invalid live pipeline completion envelope")
		return out
	}
	out.event = event.Data
	input, err := json.Marshal(struct {
		Headers http.Header `json:"headers"`
		Body    []byte      `json:"body"`
	}{out.headers, out.body})
	if err != nil {
		out.err = err
		return out
	}
	operation := "completion"
	if crash {
		operation = "completion-crash"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	raw, err := b.command(ctx, operation, "", "", input).Output()
	var results struct {
		PID        int
		Status     int
		ArtifactID string
	}
	out.at = time.Now().UTC()
	if json.Unmarshal(raw, &results) != nil || results.PID <= 0 {
		out.err = fmt.Errorf("Report Agent webhook worker returned no checkpoint: %v", err)
		return out
	}
	if crash {
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 86 || !isHostedReportUUID(results.ArtifactID) {
			out.err = fmt.Errorf("Report Agent did not exit after its committed storage receipt (HTTP %d)", results.Status)
			return out
		}
	} else if err != nil || results.Status != http.StatusNoContent {
		out.err = fmt.Errorf("Report Agent did not accept the completion (HTTP %d): %v", results.Status, err)
		return out
	}
	out.pid, out.status, out.artifactID = results.PID, results.Status, results.ArtifactID
	return out
}

func isHostedReportUUID(id string) bool {
	value, err := uuid.Parse(id)
	return err == nil && value.String() == id
}

func awaitReportWebhook(t *testing.T, attempts <-chan reportWebhookAttempt) reportWebhookAttempt {
	t.Helper()
	select {
	case attempt := <-attempts:
		requireReport(t, attempt.err)
		return attempt
	case <-time.After(160 * time.Second):
		t.Fatal("the live pipeline completion dispatcher did not reach the receiver")
		return reportWebhookAttempt{}
	}
}

func exerciseHostedBackgroundReport(t *testing.T, f *runFixture, db *pgx.Conn, object *reportObjectFixture,
	store *state.PgStore, downloadClient *http.Client, b hostedReportBackend, appID string) {
	t.Helper()
	ctx := t.Context()
	b.completionSecret = uuid.NewString() + uuid.NewString()
	receiver, attempts, restart := startBackgroundReportReceiver(t, b)
	hook := subscribeHostedReport(t, f, store, receiver.URL+"/report-completion", b.completionSecret)
	data, err := os.ReadFile("../../../examples/gregale-run/sales-report/rows.json")
	requireReport(t, err)
	var input struct{ Rows json.RawMessage }
	requireReport(t, json.Unmarshal(data, &input))
	owner, id := "background-report-customer", uuid.NewString()
	var report hostedRegionalStatus
	b.call(t, "submit-regional", id, owner, input.Rows, &report)
	if report.RequestID != id {
		t.Fatal("expected the submitted regional report")
	}
	t.Log("background report submitted; waiting for the signed dispatcher without polling the customer API")
	first := awaitReportWebhook(t, attempts)
	pipeline, linked := hostedRegionalWorkflow(t, f, db, b.endpoint, owner, id)
	completion := pipeline.Pipeline.Completion
	if !linked || completion == nil || completion.Outcome != "succeeded" || completion.CompletionID != first.event.EventID ||
		completion.WorkflowID != first.event.WorkflowID || completion.FinishedAt != first.event.FinishedAt || first.event.Outcome != "succeeded" {
		t.Fatal("signed completion did not match the completed regional pipeline")
	}
	finished, err := time.Parse(time.RFC3339Nano, completion.FinishedAt)
	requireReport(t, err)
	physical := make([]string, 0, 3)
	for _, label := range []string{"north", "south", "merge"} {
		step := hostedRegionalStep(t, pipeline, label)
		if step.Outcome != "succeeded" || len(step.Attempts) != 1 {
			t.Fatalf("expected one successful %s branch, got %s", label, step.Outcome)
		}
		physical = append(physical, step.Attempts[0].RunID)
	}
	expectHostedRegionalRuns(t, f, store, physical...)
	requireBackgroundStoredReport(t, db, pipeline.ID, "")
	receipt, err := api.NewClient(b.base, b.token).GetRunArtifactDelivery(ctx, hostedReportApp, "reports", first.artifactID)
	requireReport(t, err)
	if receipt.Status != "completed" || receipt.Object == nil {
		t.Fatal("receiver exited without a committed private artifact")
	}
	t.Log("background receiver exited after a committed copy and before acknowledging the webhook")
	restart.Store(true)
	retry := awaitReportWebhook(t, attempts)
	if retry.pid == first.pid || retry.deliveryID != first.deliveryID || retry.event != first.event || retry.status != http.StatusNoContent {
		t.Fatal("real dispatcher retry did not use the same completion and a fresh Node process")
	}
	requireBackgroundStoredReport(t, db, pipeline.ID, first.artifactID)
	ledgerAttempt := waitForBackgroundWebhookLedger(t, f, store, hook.ID, first.deliveryID)
	t.Log("background receiver recovered the same artifact on the real dispatcher's retry")

	// Replay the original byte-exact signed request after the outbox succeeds.
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, receiver.URL+"/report-completion", bytes.NewReader(first.body))
	requireReport(t, err)
	request.Header = first.headers.Clone()
	response, err := receiver.Client().Do(request)
	requireReport(t, err)
	requireReport(t, response.Body.Close())
	replay := awaitReportWebhook(t, attempts)
	if response.StatusCode != http.StatusNoContent || replay.deliveryID != first.deliveryID || replay.event != first.event {
		t.Fatal("completed delivery did not acknowledge its replay")
	}
	requireBackgroundStoredReport(t, db, pipeline.ID, first.artifactID)
	expectHostedRegionalRuns(t, f, store, physical...)
	var workflows int
	requireReport(t, f.h.Pool.QueryRow(ctx, "SELECT count(*) FROM agent_execution_workflows WHERE account_id=$1", f.accountID).Scan(&workflows))
	if workflows != 1 {
		t.Fatalf("expected exactly one workflow after webhook replay, got %d", workflows)
	}
	var download api.ObjectSignedRequest
	b.call(t, "download", id, owner, nil, &download)
	if download.Method != http.MethodGet || download.URL == "" {
		t.Fatal("expected a private signed download")
	}
	body := fetchHostedReport(t, downloadClient, download)
	if !bytes.Contains(body, []byte("4 orders · 8 units · 6996 USD cents")) {
		t.Fatal("unexpected contents on signed download")
	}
	object.mu.Lock()
	writes, reads := object.writes, object.reads
	object.mu.Unlock()
	if writes != 1 || reads != 1 {
		t.Fatalf("expected one provider write and one signed read, got %d and %d", writes, reads)
	}
	readyMS := retry.at.Sub(finished).Milliseconds()
	requireReport(t, writeBackgroundReportEvidence(t, map[string]any{
		"schema": "gregale.report-agent-background/v1", "app_id": appID, "pipeline_id": pipeline.ID,
		"request_id": id, "webhook_delivery_id": first.deliveryID, "artifact_delivery_id": first.artifactID, "dispatcher_attempts": ledgerAttempt,
		"customer_poll_count": 0, "receiver_crashed_after_copy": true, "fresh_receiver_recovered": true, "replay_verified": true,
		"physical_runs": len(physical), "workflow_count": workflows, "provider_writes": writes, "provider_reads": reads,
		"completion_to_copy_ms": first.at.Sub(finished).Milliseconds(), "completion_to_download_ready_ms": readyMS,
		"report_bytes": len(body), "report_sha256": fmt.Sprintf("%x", sha256.Sum256(body)), "revenue_cents": 6996,
	}))
	t.Logf("background report ready in %d ms with one provider write, no customer polling and zero extra Runs", readyMS)
}

func subscribeHostedReport(t *testing.T, f *runFixture, store *state.PgStore, target, secret string) state.AppWebhook {
	t.Helper()
	ctx := t.Context()
	// Seed the private loopback receiver using the established fixture path.
	sealed, err := secretbox.SealBytes(f.webhookRecipient, api.AppWebhookSecretNamespace, []byte(secret), api.AppWebhookSecretMaxBytes)
	requireReport(t, err)
	account, err := store.AccountByID(ctx, f.accountID)
	requireReport(t, err)
	limits, _ := api.LimitsFor(account.Plan)
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: f.accountID, TargetURL: target, SecretSealed: sealed, Enabled: true,
		EventFilter: []string{api.ExecutionPipelineFinishedWebhookEvent}, RetryPolicy: state.AppWebhookRetryAggressive,
		DeliveryFormat: state.AppWebhookDeliveryFormatCloudEvents,
	}, limits)
	requireReport(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.client.DeleteAccountReleaseWebhook(cleanupCtx, hook.ID); err != nil {
			t.Error("retire the private acceptance subscription:", err)
		}
	})
	return hook
}

func requireBackgroundStoredReport(t *testing.T, db *pgx.Conn, pipelineID, expectedDelivery string) {
	t.Helper()
	var delivery string
	var background, intent bool
	requireReport(t, db.QueryRow(t.Context(), `SELECT COALESCE(report #>> '{delivery,id}', ''), background_delivery, delivery_intent IS NOT NULL
		FROM report_regional_pipelines WHERE pipeline_id=$1`, pipelineID).Scan(&delivery, &background, &intent))
	if !background || !intent || delivery != expectedDelivery {
		t.Fatal("unexpected durable report state around webhook receiver restart")
	}
}

func waitForBackgroundWebhookLedger(t *testing.T, f *runFixture, store *state.PgStore, hookID, deliveryID string) int {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		rows, _, err := store.ListAccountReleaseWebhookDeliveries(t.Context(), f.accountID, hookID, 10, "")
		requireReport(t, err)
		if len(rows) != 1 || rows[0].ID != deliveryID {
			t.Fatal("the durable outbox did not retain exactly one delivery identity")
		}
		if rows[0].Status == state.AppWebhookDeliverySucceeded && rows[0].Attempt >= 2 && rows[0].LastResponseCode == http.StatusNoContent {
			return rows[0].Attempt
		}
	}
	t.Fatal("the real dispatcher did not acknowledge its durable retry")
	return 0
}

func writeBackgroundReportEvidence(t *testing.T, measurements map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(measurements)
	if err != nil {
		return err
	}
	return os.WriteFile(requiredHostedEnv(t, "FAAS_TEST_REPORT_MEASUREMENTS"), append(raw, '\n'), 0600)
}
