//go:build metal

package runlifecycle_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// TestSourceHostedReportAgentMetal runs the actual example in fresh Node
// processes against the hosted API, private Postgres and real Run microVMs.
// The driver forwards persisted inbox records and supplies fixture workload
// identity and an isolated S3 wire endpoint.
func TestSourceHostedReportAgentMetal(t *testing.T) {
	node := os.Getenv("FAAS_TEST_REPORT_AGENT_NODE")
	if !filepath.IsAbs(node) {
		t.Fatal("set FAAS_TEST_REPORT_AGENT_NODE to the pinned Node 24 executable")
	}
	out, err := exec.CommandContext(t.Context(), node, "--version").Output()
	if err != nil || !strings.HasPrefix(string(out), "v24.") {
		t.Fatal("the Report Agent qualification requires Node 24")
	}
	signer := reportSigner(t)
	provider := &reportObjectFixture{}
	storage := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(storage.Close)
	objectConfig := objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "report"},
		Accounting: &api.ObjectStoragePolicy{MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1_000,
			MaxMonthlyCostMillicents: 100_000, MaxMonthlyRequests: 10_000, MaxMonthlyEgressBytes: 1 << 30,
			MaxMonthlyAuthorizations: 1_000, MaxReportAgeSeconds: 3_600},
		Backends: []objectstorage.BackendConfig{{ID: "report", Driver: "s3", Region: "us-east-1", Namespace: "report",
			Endpoint: storage.URL, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true,
			AccessKeyEnv: "FAAS_E2E_REPORT_S3_KEY", SecretKeyEnv: "FAAS_E2E_REPORT_S3_SECRET"}}}
	writeConfig := func(name string, value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Setenv("FAAS_RUN_TASK_QUALIFICATION", "1")
	f := startRunFixture(t, false, []string{
		"FAAS_EXECUTION_PIPELINES_ENABLED=1",
		"FAAS_WORKLOAD_JWKS_PATH=" + writeConfig("workload.jwks", signer.JWKS()),
		"FAAS_OBJECT_STORAGE_CONFIG=" + writeConfig("objects.json", objectConfig),
		"FAAS_E2E_REPORT_S3_KEY=report-fixture",
		"FAAS_E2E_REPORT_S3_SECRET=report-fixture",
	})
	s := state.NewPgStore(f.h.Pool)
	ctx := t.Context()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ownApp, ownInstance := reportWorkload(t, s, f.accountID, "report-agent")
	otherApp, otherInstance := reportWorkload(t, s, f.accountID, "report-other")
	reportToken := func(app state.App, instance state.Instance) workloadidentity.Token {
		t.Helper()
		token, err := signer.Mint(time.Now(), f.accountID, app.ID, instance.ID, workloadidentity.AgentsAudience)
		check(err)
		return token
	}
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audience") != workloadidentity.AgentsAudience {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		token, err := signer.Mint(time.Now(), f.accountID, ownApp.ID, ownInstance.ID, workloadidentity.AgentsAudience)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(token)
	}))
	t.Cleanup(metadata.Close)
	seedReportDestination(t, f, s, ownApp, objectConfig)
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: uuid.NewString(), AccountID: f.accountID, AppID: ownApp.ID, CallbackURL: "https://example.test/report",
		ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect",
		CallbackAuthTokenSealed: []byte("fixture"), AuthTokenSealed: []byte("fixture"), Enabled: true,
	}, 10, 50)
	check(err)

	// node-postgres accepts libpq's options parameter; pgx's search_path URL
	// parameter would otherwise be ignored by the worker.
	dsn, err := url.Parse(os.Getenv("DATABASE_URL"))
	check(err)
	schema := pgtest.SchemaOf(f.h.Pool)
	if schema == "" || (dsn.Scheme != "postgres" && dsn.Scheme != "postgresql") {
		t.Fatal("Report Agent requires the acceptance PostgreSQL URL and isolated schema")
	}
	q := dsn.Query()
	q.Set("options", "-c search_path="+schema)
	dsn.RawQuery = q.Encode()
	worker := func(mode string, record *state.ManagedRealtimeChannelMessage, exit int) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, node, "../../../examples/gregale-run/test/hosted-worker.mjs", mode, endpoint.ID)
		cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn.String(), "GREGALE_API_URL="+f.h.APIDURL,
			"FAAS_WORKLOAD_IDENTITY_ENDPOINT="+metadata.URL+"/identity")
		if record != nil {
			raw, err := json.Marshal(map[string]any{"endpoint_id": record.EndpointID, "metadata": record.Metadata, "data_base64": record.Data})
			check(err)
			cmd.Stdin = bytes.NewReader(raw)
		}
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		b, err := cmd.Output()
		var status *exec.ExitError
		if (exit == 0 && err != nil) || (exit != 0 && (!errors.As(err, &status) || status.ExitCode() != exit)) {
			t.Fatalf("Report Agent %s: %v\n%s", mode, err, diagnostic.String())
		}
		return b
	}
	var fixtures struct {
		Tasks []api.PublishExecutionTaskRequest
		Input json.RawMessage
	}
	check(json.Unmarshal(worker("fixtures", nil, 0), &fixtures))
	if len(fixtures.Tasks) != 2 || len(fixtures.Input) == 0 {
		t.Fatal("missing sample tasks or order fixture")
	}
	for _, def := range fixtures.Tasks {
		// Include time queued behind the other branch on the two-vCPU host.
		def.Definition.Limits.TimeoutMS = 20_000
		_, err := f.client.PublishExecutionTask(ctx, def)
		check(err)
	}

	const thread = "qualification-report"
	user, err := s.AppendManagedRealtimeChannelMetadata(ctx, endpoint.ID, api.AgentInboxChannel, fixtures.Input, false, "user-1",
		map[string]string{"kind": "user", "thread_id": thread, "message_id": "user-1"})
	check(err)
	worker("drop-dispatch", &user, 71)
	check(f.h.RestartAPID())
	worker("process", &user, 0)
	t.Log("worker restart replayed the original persisted dispatch after acknowledgement loss")

	var completion state.ManagedRealtimeChannelMessage
	deadline := time.Now().Add(140 * time.Second)
	for time.Now().Before(deadline) {
		history, err := s.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, api.AgentInboxChannel, user.Sequence, 5)
		check(err)
		if len(history.Messages) != 0 {
			if len(history.Messages) != 1 || history.Messages[0].Metadata["kind"] != "tool_result" || history.Messages[0].Metadata["thread_id"] != thread {
				t.Fatal("expected one matching terminal Agent inbox record")
			}
			completion = history.Messages[0]
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if completion.Sequence == 0 {
		t.Fatal("hosted pipeline did not complete")
	}
	var envelope struct {
		Text string
	}
	check(json.Unmarshal(completion.Data, &envelope))
	var result api.ExecutionPipelineCompletion
	check(json.Unmarshal([]byte(envelope.Text), &result))
	if result.Outcome != "succeeded" || result.CompletionID == "" {
		if job, err := s.ExecutionWorkflowJobByKey(ctx, f.accountID, result.WorkflowID, nil); err == nil {
			t.Logf("pipeline job after settlement: %s (%s)", job.Status, job.LastError)
		}
		if stored, err := s.ExecutionPipelineByID(ctx, result.CompletionID); err == nil && stored.Completion != nil {
			for _, step := range stored.Completion.Steps {
				t.Logf("pipeline step %s: %s (%s)", step.Label, step.Outcome, step.ResultValidation)
				for _, attempt := range step.Attempts {
					if row, err := f.client.GetExecution(ctx, attempt.RunID); err == nil {
						t.Logf("pipeline attempt %d: %s (%s: %s)", attempt.Attempt, attempt.Status, attempt.FailureCode, fmt.Sprint(row.Failure))
					}
				}
			}
		}
		t.Fatalf("Report Agent pipeline outcome %s", result.Outcome)
	}

	t.Log("Report Agent pipeline succeeded and published its durable tool result")
	ref := strings.TrimSpace(string(worker("drop-delivery", &completion, 71)))
	const receiptPrefix = "/v1/apps/report-agent/run-artifact-deliveries/reports/"
	if !strings.HasPrefix(ref, receiptPrefix) {
		t.Fatal("delivery did not return an app-private receipt")
	}
	check(f.h.RestartAPID())
	worker("process", &completion, 0)
	first := worker("transcript", &user, 0)
	worker("process", &completion, 0)
	if replay := worker("transcript", &user, 0); !bytes.Equal(first, replay) {
		t.Fatal("replaying the completion changed the persisted transcript")
	}
	var transcript []struct {
		Role, CallID, Text string
		Dispatched, Final  bool
		Delivery           *api.RunArtifactDelivery
	}
	// Go does not infer the name of underscored JSON fields.
	var call struct {
		CallID string `json:"call_id"`
	}
	var rows []json.RawMessage
	check(json.Unmarshal(first, &rows))
	check(json.Unmarshal(first, &transcript))
	if len(rows) != 4 {
		t.Fatalf("expected four transcript records, found %d", len(rows))
	}
	check(json.Unmarshal(rows[1], &call))
	delivery := transcript[2].Delivery
	if transcript[0].Role != "user" || transcript[1].Role != "assistant" || call.CallID != "assistant:user-1" || !transcript[1].Dispatched ||
		transcript[2].Role != "tool" || delivery == nil || delivery.ReceiptURL != ref || string(delivery.Status) != "completed" ||
		transcript[3].Role != "assistant" || !transcript[3].Final || transcript[3].Text != "4 orders, 6996 USD cents. Report: "+ref {
		t.Fatal("persisted transcript did not retain the original private delivery")
	}
	receiptHTTP := func(app state.App, instance state.Instance) int {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.h.APIDURL+ref, nil)
		check(err)
		req.Header.Set("Authorization", "Bearer "+reportToken(app, instance).AccessToken)
		res, err := http.DefaultClient.Do(req)
		check(err)
		defer res.Body.Close()
		return res.StatusCode
	}
	if allowed, denied := receiptHTTP(ownApp, ownInstance), receiptHTTP(otherApp, otherInstance); allowed != 200 || denied != 404 {
		t.Fatalf("private receipt authorization: owner=%d other_app=%d", allowed, denied)
	}
	provider.mu.Lock()
	stored, writes := bytes.Clone(provider.body), provider.writes
	provider.mu.Unlock()
	if writes != 1 || !bytes.Contains(stored, []byte("6996")) {
		t.Fatalf("expected one stored report containing the verified revenue, provider writes=%d", writes)
	}

	runs, err := s.ListExecutions(ctx, f.accountID, 20, 0)
	check(err)
	if len(runs) != 3 {
		t.Fatalf("expected exactly two sales Runs and one merge Run, found %d", len(runs))
	}
	for _, row := range runs {
		f.runs[row.ID] = true
		f.wait(t, row.ID, api.ExecutionStatusSucceeded)
	}
	history, err := s.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, api.AgentInboxChannel, user.Sequence, 5)
	check(err)
	if len(history.Messages) != 1 || history.Messages[0].Sequence != completion.Sequence {
		t.Fatal("restart or replay duplicated terminal delivery")
	}
	f.assertSettled(t)
	t.Log("hosted Report Agent witness: runs=3 inbox_completions=1 transcript_rows=4 provider_writes=1 apid_restarts=2 receipt_replay=true cross_app_denied=true")
}

func reportSigner(t *testing.T) *workloadidentity.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "report-agent-acceptance", workloadidentity.DefaultTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func reportWorkload(t *testing.T, s *state.PgStore, account, slug string) (state.App, state.Instance) {
	t.Helper()
	ctx := t.Context()
	app, err := s.CreateApp(ctx, state.App{AccountID: account, Slug: slug, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return app, inst
}

func seedReportDestination(t *testing.T, f *runFixture, s *state.PgStore, app state.App, config objectstorage.Config) {
	t.Helper()
	ctx := t.Context()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "report-fixture" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	check(err)
	if !registry.Accounting.Valid() {
		t.Fatal("Report Agent destination requires a valid object accounting policy")
	}
	backend, err := registry.Default(config.DefaultRegion)
	check(err)
	b, err := s.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: f.accountID, AppID: app.ID, Name: "assets",
		Scope: "default", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "report-fixture", Region: config.DefaultRegion}, 10)
	check(err)
	_, err = s.ClaimObjectBucket(ctx, f.accountID, app.ID, b.ID, "create", "provisioning")
	check(err)
	check(s.FinishObjectBucket(ctx, b.ID, "create", "ready"))
	check(s.ClaimObjectInventory(ctx, b.ID, "baseline"))
	check(s.FinishObjectInventory(ctx, b.ID, "baseline", 0, 0))
	now := time.Now()
	check(s.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: f.accountID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		Source: "fixture", PeriodStart: state.ObjectStoragePeriod(now), ObservedAt: now}))
	_, err = s.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: f.accountID, AppID: app.ID, BucketID: b.ID,
		Name: "reports", KeyPrefix: "reports", MaxBytes: 32768, AllowedContentTypes: []string{"text/html"}, Enabled: true})
	check(err)
	_, err = s.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: "s3_enabled", Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage("true"), ApplyMode: state.RuntimeConfigApplyHot, Reason: "Report Agent internal qualification"})
	check(err)
	check(f.h.RestartAPID())
}

type reportObjectFixture struct {
	mu      sync.Mutex
	body    []byte
	headers http.Header
	path    string
	writes  int
	reads   int
	heads   int
}

func (o *reportObjectFixture) serve(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r.Method == http.MethodPut {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
		if err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		o.body, o.headers, o.path = body, r.Header.Clone(), r.URL.Path
		o.writes++
	}
	if o.headers == nil || r.URL.Path != o.path {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		o.reads++
	}
	if r.Method == http.MethodHead {
		o.heads++
	}
	for name, values := range o.headers {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") || name == "Content-Type" {
			w.Header()[name] = append([]string(nil), values...)
		}
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%x\"", sha256.Sum256(o.body)))
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(o.body)))
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write(o.body)
	}
}
