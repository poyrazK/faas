//go:build metal

package runlifecycle_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const (
	hostedReportApp      = "report-agent"
	hostedReportGroup    = "slices"
	hostedReportTimeoutS = 120
	hostedReportLeaseS   = 240
	hostedReportDispatch = "/v1/agent-tools/invoke"
	hostedReportDelivery = "/v1/apps/report-agent/run-artifact-deliveries/reports"
)

// TestDeployedReportAgentMetal builds and deploys the actual pilot source as
// a function. Only the real scheduler/gateway can invoke the inbox handler.
func TestDeployedReportAgentMetal(t *testing.T) {
	ctx := t.Context()
	node := requiredHostedEnv(t, "FAAS_TEST_REPORT_AGENT_NODE")
	bundle := requiredHostedEnv(t, "FAAS_TEST_REPORT_BUNDLE")
	dsn := requiredHostedEnv(t, "FAAS_TEST_REPORT_DB_DSN")
	guestDSN := requiredHostedEnv(t, "FAAS_TEST_REPORT_GUEST_DB_DSN")
	guestIP := requiredHostedEnv(t, "FAAS_TEST_REPORT_GUEST_IP")
	pgConnections := setupHostedPostgresProxy(t, guestIP+":443", requiredHostedEnv(t, "FAAS_TEST_REPORT_DB_SOCKET"))
	appDB, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("connect isolated Report Agent database")
	}
	t.Cleanup(func() { _ = appDB.Close(context.Background()) })
	schema, err := os.ReadFile(filepath.Join(bundle, "report-db.sql"))
	requireReport(t, err)
	_, err = appDB.Exec(ctx, string(schema))
	requireReport(t, err)

	provider := &reportObjectFixture{}
	storage := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(storage.Close)
	config := hostedReportStorageConfig(storage.URL)
	var downloadHandler atomic.Pointer[s3gateway.Handler]
	downloadEdge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler := downloadHandler.Load(); handler != nil {
			handler.ServeHTTP(w, r)
		} else {
			http.Error(w, "fixture not ready", http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(downloadEdge.Close)
	config.PublicEndpoint = downloadEdge.URL
	identity, apidEnv := hostedReportCredentials(t, config)
	t.Setenv("FAAS_RUN_TASK_QUALIFICATION", "1")
	if os.Getenv("FAAS_RUN_NATIVE_PRIVATE_MOUNT_NS") != "1" {
		t.Fatal("hosted Report Agent requires the runner's private mount namespace")
	}
	// Source builds use the guestdisk export path, which is unavailable
	// under native process recovery. Keep the legacy owner and the deployed
	// function's writable drive on the runner's private scratch disk.
	jailDir := filepath.Join(requiredHostedEnv(t, "FAAS_TEST_RUN_FIXTURE_ROOT"), "hosted-jail")
	if !filepath.IsAbs(jailDir) {
		t.Fatal("hosted Report Agent jail path must be absolute")
	}
	prepareNativeRunJail(t, jailDir)
	f := startRunFixtureWithServices(t, false, apidEnv, func(root string) []string {
		return hostedReportRuntime(t, root, identity)
	}, e2etest.All|e2etest.Realtimed)
	store := state.NewPgStore(f.h.Pool)
	downloadHandler.Store(hostedReportDownloadHandler(t, f, store, config))
	relay := startHostedReportRelay(t, guestIP+":80", f.h.APIDURL)
	public := false
	app, err := f.client.CreateApp(ctx, api.CreateAppRequest{Slug: hostedReportApp, Type: "function",
		Runtime: "node24", RequestTimeoutS: hostedReportTimeoutS, RequireAuthn: &public})
	requireReport(t, err)
	retired := false
	triggerID := ""
	retire := func() {
		if !retired {
			retired = true
			retireHostedReport(t, f, app.ID, triggerID)
		}
	}
	t.Cleanup(retire)
	// The private relay uses an IP literal. Admit only that destination for
	// this app while the node continues to require DNS-resolved egress.
	relayCIDRs := []string{guestIP + "/32"}
	if _, err := f.client.UpdateApp(ctx, hostedReportApp, api.UpdateAppRequest{EgressAllowlist: &relayCIDRs}); err != nil {
		t.Fatalf("configure the pilot relay's egress policy: %v", err)
	}
	row, err := store.AppByID(ctx, app.ID)
	requireReport(t, err)
	if timeout := state.AsyncInvocationTimeout(ctx, store, row); timeout != hostedReportTimeoutS*time.Second {
		t.Fatalf("unexpected hosted Report Agent request timeout %s", timeout)
	}
	seedReportDestination(t, f, store, row, config)
	if os.Getenv("FAAS_TEST_REPORT_RECOVERY") == "1" {
		t.Run("recovery", func(t *testing.T) {
			starter := configureHostedReportStarter(t, f, store, downloadEdge, node, dsn, guestDSN, guestIP)
			setup := starter.run(t, "")
			triggerID = hostedReportTriggerID(t, f)
			backend := hostedReportBackend{node: node, bundle: bundle, dsn: dsn, endpoint: setup.EndpointID,
				base: f.h.APIDURL, token: starter.token, prefix: setup.TaskPrefix}
			exerciseHostedRegionalRecovery(t, f, appDB, provider, store, downloadEdge.Client(), backend, setup)
		})
		retire()
		f.assertSettled(t)
		return
	}
	if os.Getenv("FAAS_TEST_REPORT_SETUP") == "1" {
		t.Run("setup", func(t *testing.T) {
			exerciseHostedReportSetup(t, f, store, appDB, provider, downloadEdge, node, dsn, guestDSN, guestIP, &triggerID)
		})
		retire()
		f.assertSettled(t)
		return
	}
	endpoint, trigger := configureHostedInbox(t, f, app)
	triggerID = trigger.ID
	// The disposable database credential follows the platform's ephemeral
	// attachment policy and must not be captured in a VM snapshot.
	if err := f.client.SetSecretWithScopeAndClass(ctx, hostedReportApp, "DATABASE_URL", guestDSN, "", api.SecretClassEphemeral); err != nil {
		t.Fatal("set the pilot's ephemeral database credential")
	}
	for key, value := range map[string]string{
		"GREGALE_API_URL": "http://" + guestIP, "REPORT_APP": hostedReportApp,
		"REPORT_ENDPOINT_ID": endpoint.ID, "REPORT_UPLOAD_ROUTE": "reports",
	} {
		if err := f.client.SetSecret(ctx, hostedReportApp, key, value); err != nil {
			t.Fatalf("set pilot secret %s: %v", key, err)
		}
	}
	rows := publishHostedReportTasks(t, f, node, endpoint.ID)
	archive, err := os.ReadFile(requiredHostedEnv(t, "FAAS_TEST_REPORT_ARCHIVE"))
	requireReport(t, err)
	started := time.Now()
	dep, err := f.client.DeployMultipart(ctx, hostedReportApp, bytes.NewReader(archive), "report-agent.tar.gz",
		"node24", "handler.handler", false, api.DeployAnnotations{})
	requireReport(t, err)
	// First deployment stages the immutable and canonical Node 24 bases.
	// Their scans can outlast the general deployment stall window on this
	// two-vCPU host without updating the source build's progress counters.
	live, build, err := e2etest.WaitForSourceDeployment(ctx, t, f.h.Pool, dep.ID, 12*time.Minute, 20*time.Minute)
	requireReport(t, err)
	if build.Status != state.BuildSucceeded || live.Status != state.DeployLive {
		t.Fatal("pilot source did not reach a live deployment through the native builder")
	}
	deployElapsed := time.Since(started)
	t.Logf("hosted pilot deployed, source build in %s", deployElapsed.Round(time.Millisecond))

	backend := hostedReportBackend{node: node, bundle: bundle, dsn: dsn, endpoint: endpoint.ID,
		base: f.h.APIDURL, token: reportBackendToken(t, store, f.accountID)}
	var samples []hostedReportSample
	for _, input := range []string{"json", "csv"} {
		t.Run(input, func(t *testing.T) {
			beforeConnections := pgConnections.Load()
			t.Cleanup(func() {
				if t.Failed() {
					logHostedReportFailure(t, f, relay, appDB, live.ID, endpoint.ID, pgConnections.Load()-beforeConnections)
				}
			})
			beforeSubmit := time.Since(started).Milliseconds()
			sample := exerciseHostedReport(t, f, appDB, provider, store, relay, downloadEdge.Client(), backend, rows, app.ID, trigger.ID, input)
			sample.BeforeSubmitMS = beforeSubmit
			samples = append(samples, sample)
		})
		if t.Failed() {
			return
		}
	}
	retire()
	f.assertSettled(t)
	report := map[string]any{
		"schema": "gregale.report-agent-deployed/v1", "app_runtime": "node24", "deployment_ms": deployElapsed.Milliseconds(),
		"app_request_timeout_s": hostedReportTimeoutS, "consumer_group_lease_s": hostedReportLeaseS,
		"app_snapshot_policy":       "ephemeral_database_secret",
		"deploy_to_first_report_ms": samples[0].BeforeSubmitMS + samples[0].TotalMS,
		"journeys":                  samples, "compute_overage_usd_per_gib_hour": float64(api.OverageMillicentsPerGBHour) / 100_000,
		"cost_basis": "Run limit MiB times ledger wall time and hosted app reservation MiB times observed residency at the compute overage rate before plan allowance. Object bytes and operations come from the isolated S3-compatible fixture.",
	}
	encoded, err := json.Marshal(report)
	requireReport(t, err)
	requireReport(t, os.WriteFile(requiredHostedEnv(t, "FAAS_TEST_REPORT_MEASUREMENTS"), append(encoded, '\n'), 0600))
}

func logHostedReportFailure(t *testing.T, f *runFixture, relay *hostedReportRelay, appDB *pgx.Conn, deployment, endpoint string, connections int64) {
	t.Helper()
	relay.mu.Lock()
	for failure, count := range relay.failures {
		t.Logf("hosted failure: Agent API %s HTTP %d count=%d", failure.route, failure.status, count)
	}
	relay.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var transcript int
	if err := appDB.QueryRow(ctx, "SELECT count(*) FROM agent_messages").Scan(&transcript); err == nil {
		t.Logf("hosted failure: postgres_connections=%d transcript_rows=%d", connections, transcript)
	}
	var status, failure string
	var attempts int
	err := f.h.Pool.QueryRow(ctx, `SELECT m.status, m.attempts, m.failure_code
FROM managed_realtime_consumer_group_messages AS m
JOIN managed_realtime_consumer_groups AS g ON g.id=m.group_id
WHERE g.endpoint_id=$1 AND g.channel=$2 AND g.name=$3 ORDER BY m.sequence DESC LIMIT 1`,
		endpoint, api.AgentInboxChannel, hostedReportGroup).Scan(&status, &attempts, &failure)
	if err == nil {
		t.Logf("hosted failure: broker_state=%s attempts=%d failure=%s", status, attempts, failure)
	}
	stream, err := f.client.StreamAppLogs(ctx, hostedReportApp, deployment, false, api.LogFilter{})
	if err != nil {
		t.Log("hosted failure: runtime log stream unavailable")
		return
	}
	defer stream.Close()
	decoder := api.NewDecoder(io.LimitReader(stream, 64<<10))
	defer decoder.Close()
	runtimeErrors := 0
	signals := []string{"ECONNREFUSED", "ETIMEDOUT", "ENETUNREACH", "EHOSTUNREACH", "connection timeout",
		"permission denied", "workload identity: HTTP", "FAAS_WORKLOAD_IDENTITY_ENDPOINT is not set",
		"Configure REPORT_APP", "DATABASE_URL is not set", "Cannot find module", "Cannot find package",
		"postgres_lifecycle_unavailable", "Invalid Agent order", "Agent demo expects", "fetch failed",
		"HTTP 400", "HTTP 401", "HTTP 403", "HTTP 404", "HTTP 409", "HTTP 500", "HTTP 503"}
	seen := make(map[string]bool)
	for event := range decoder.Events() {
		var log api.LogEvent
		if event.Event != "log" || json.Unmarshal([]byte(event.Data), &log) != nil || log.Stream != "stderr" {
			continue
		}
		runtimeErrors++
		for _, signal := range signals {
			if strings.Contains(log.Line, signal) && !seen[signal] {
				t.Logf("hosted failure: runtime marker %s", signal)
				seen[signal] = true
			}
		}
	}
	t.Logf("hosted failure: runtime_stderr_lines=%d", runtimeErrors)
}

func requireReport(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func requiredHostedEnv(t *testing.T, name string) string {
	t.Helper()
	if value := os.Getenv(name); value != "" {
		return value
	}
	t.Fatalf("required hosted Report Agent fixture %s is unset", name)
	return ""
}

func hostedReportStorageConfig(endpoint string) objectstorage.Config {
	return objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "report"},
		Accounting: &api.ObjectStoragePolicy{MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1_000,
			MaxMonthlyCostMillicents: 100_000, MaxMonthlyRequests: 10_000, MaxMonthlyEgressBytes: 1 << 30,
			MaxMonthlyAuthorizations: 1_000, MaxReportAgeSeconds: 3_600},
		Backends: []objectstorage.BackendConfig{{ID: "report", Driver: "s3", Region: "us-east-1", Namespace: "report",
			Endpoint: endpoint, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true,
			AccessKeyEnv: "FAAS_E2E_REPORT_S3_KEY", SecretKeyEnv: "FAAS_E2E_REPORT_S3_SECRET"}}}
}

func hostedReportCredentials(t *testing.T, config objectstorage.Config) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	requireReport(t, err)
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "hosted-report-qualification", workloadidentity.DefaultTokenTTL)
	requireReport(t, err)
	keyPath := filepath.Join(dir, "key.pem")
	requireReport(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	write := func(name string, document any) string {
		raw, err := json.Marshal(document)
		requireReport(t, err)
		path := filepath.Join(dir, name)
		requireReport(t, os.WriteFile(path, raw, 0600))
		return path
	}
	return keyPath, []string{"FAAS_EXECUTION_PIPELINES_ENABLED=1", "FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1",
		"FAAS_REALTIME_PREVIEW_ACCOUNTS=*",
		"FAAS_OBJECT_STORAGE_CONFIG=" + write("objects.json", config),
		"FAAS_WORKLOAD_JWKS_PATH=" + write("identity.json", signer.JWKS()),
		"FAAS_E2E_REPORT_S3_KEY=report-fixture", "FAAS_E2E_REPORT_S3_SECRET=report-fixture"}
}

func hostedReportDownloadHandler(t *testing.T, f *runFixture, store *state.PgStore, config objectstorage.Config) *s3gateway.Handler {
	t.Helper()
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "report-fixture" },
		map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	requireReport(t, err)
	var identities []*age.X25519Identity
	for _, name := range []string{"host.age", "fleet.age"} {
		raw, err := os.ReadFile(filepath.Join(f.root, name))
		requireReport(t, err)
		identity, err := age.ParseX25519Identity(strings.TrimSpace(string(raw)))
		requireReport(t, err)
		identities = append(identities, identity)
	}
	h, err := s3gateway.New(s3gateway.Config{Registry: registry, Store: store, RequestMetrics: store, SpoolDir: t.TempDir(),
		MinSpoolFreeBytes: 1, OpenSecret: func(blob []byte) (string, error) {
			namespace, plain, err := secretbox.OpenBytesMulti(identities, blob)
			if err != nil || namespace != s3gateway.CredentialSecretNamespace {
				return "", errors.New("invalid fixture credential")
			}
			return string(plain), nil
		}})
	requireReport(t, err)
	return h
}

func hostedReportRuntime(t *testing.T, root, identity string) []string {
	t.Helper()
	// Staged bases belong to this harness. imaged may patch and publish its
	// exact guest init into these copies without mutating installed images.
	base := requiredHostedEnv(t, "FAAS_TEST_REPORT_BASE_DIR")
	// The local snapshot publisher inherits permissions from this root.
	requireReport(t, os.MkdirAll(filepath.Join(root, "snap"), 0o770))
	for _, name := range []string{"base", "scans"} {
		if out, err := exec.CommandContext(t.Context(), "cp", "-r", "--reflink=auto", "--sparse=always",
			filepath.Join(base, name)+"/.", filepath.Join(root, name)).CombinedOutput(); err != nil {
			t.Fatalf("stage private hosted base assets: %v %s", err, out)
		}
	}
	builder := filepath.Join(root, "base/runner-builder-amd64.ext4")
	t.Setenv("FAAS_BUILDER_BASE_PATH", builder)
	return []string{"FAAS_WORKLOAD_IDENTITY_KEY_PATH=" + identity,
		"FAAS_E2E_GATEWAY_NODE_NAME=default-local",
		"FAAS_WORKLOAD_IDENTITY_KEY_ID=hosted-report-qualification",
		"FAAS_FUNCTION_RUNNER_NODE24=" + requiredHostedEnv(t, "FAAS_TEST_REPORT_NODE24_RUNNER"),
		"FAAS_BUILDER_BASE_PATH=" + builder,
		"FAAS_BUILDER_BASE_REF=" + requiredHostedEnv(t, "FAAS_TEST_REPORT_BUILDER_REF"),
		"FAAS_DEPLOY_BASE_REF_NODE24=" + requiredHostedEnv(t, "FAAS_TEST_REPORT_NODE24_REF"),
		"FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT=" + requiredHostedEnv(t, "FAAS_TEST_REPORT_DEBIAN_PARENT_REF"),
		"FAAS_DEPLOY_BASE_REF_MINIMAL=" + requiredHostedEnv(t, "FAAS_TEST_REPORT_MINIMAL_REF")}
}

func configureHostedInbox(t *testing.T, f *runFixture, app api.AppResponse) (api.ManagedRealtimeEndpointResponse, api.Trigger) {
	t.Helper()
	ctx := t.Context()
	endpoint, err := f.client.CreateManagedRealtimeEndpoint(ctx, hostedReportApp, api.CreateManagedRealtimeEndpointRequest{
		Slug: "agent", CallbackURL: "https://" + hostedReportApp + ".apps.test.example",
		ConnectPath: "/realtime/connect", MessagePath: "/realtime/message", DisconnectPath: "/realtime/disconnect",
		CallbackAuthToken: uuid.NewString(), AuthMode: "none",
	})
	requireReport(t, err)
	// Leave the broker lease longer than the handler and its wake allowance.
	ordering, deduplication, deadLetter, lease := "thread_id", "message_id", "block", hostedReportLeaseS
	_, err = f.client.CreateRealtimeConsumerGroup(ctx, hostedReportApp, endpoint.ID, api.AgentInboxChannel,
		api.RealtimeConsumerGroupCreateRequest{Name: hostedReportGroup, AfterSequence: "0", LeaseSeconds: &lease,
			OrderingMetadataKey: &ordering, DeduplicationMetadataKey: &deduplication, DeadLetterPolicy: &deadLetter})
	requireReport(t, err)
	config, err := json.Marshal(api.RealtimeTriggerConfig{EndpointID: endpoint.ID, Channel: api.AgentInboxChannel,
		Group: hostedReportGroup, MaxConcurrency: 4})
	requireReport(t, err)
	trigger, err := f.client.PostTriggers(ctx, api.CreateTriggerRequest{
		AppID: app.ID, Kind: api.TriggerKindRealtime, Slug: "agent-inbox", Config: config})
	requireReport(t, err)
	return endpoint, trigger
}

func publishHostedReportTasks(t *testing.T, f *runFixture, node, endpoint string) json.RawMessage {
	t.Helper()
	return publishReportFixtureTasks(t, f, node, endpoint, nil)
}

func publishReportFixtureTasks(t *testing.T, f *runFixture, node, endpoint string, configure func(*api.PublishExecutionTaskRequest)) json.RawMessage {
	t.Helper()
	raw, err := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", `
import { buildExamples, jsonFile } from '../../../examples/gregale-run/scripts/manifests.mjs';
const { tasks } = await buildExamples({ endpointID: process.argv[1] });
console.log(JSON.stringify({ tasks: [tasks.sales, tasks.merge], input: await jsonFile('sales-report/rows.json') }));`, endpoint).Output()
	requireReport(t, err)
	var fixtures struct {
		Tasks []api.PublishExecutionTaskRequest
		Input json.RawMessage
	}
	requireReport(t, json.Unmarshal(raw, &fixtures))
	if len(fixtures.Tasks) != 2 || len(fixtures.Input) == 0 {
		t.Fatal("missing sales/merge or order fixtures")
	}
	for _, definition := range fixtures.Tasks {
		definition.Definition.Limits.TimeoutMS = 20_000
		if configure != nil {
			configure(&definition)
		}
		_, err := f.client.PublishExecutionTask(t.Context(), definition)
		requireReport(t, err)
	}
	return fixtures.Input
}

func reportBackendToken(t *testing.T, store *state.PgStore, account string) string {
	t.Helper()
	token, digest, err := api.GenerateAPIKey()
	requireReport(t, err)
	_, err = store.CreateAPIKey(t.Context(), account, digest, "hosted-report-backend", api.ScopesAdminOnly)
	requireReport(t, err)
	return token
}

func retireHostedReport(t *testing.T, f *runFixture, appID, triggerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if triggerID != "" {
		if err := f.client.PostTriggersIdPause(ctx, triggerID); err != nil {
			t.Errorf("pause pilot trigger: %v", err)
		}
	}
	store := state.NewPgStore(f.h.Pool)
	requested := false
	for ctx.Err() == nil {
		rows, err := store.ListInstancesForApp(ctx, appID)
		if err == nil {
			resident := false
			for _, row := range rows {
				resident = resident || state.State(row.State).CountsForRAM()
			}
			if !resident {
				return
			}
			if !requested {
				if err := f.client.Park(ctx, hostedReportApp); err != nil {
					t.Errorf("park pilot: %v", err)
					return
				}
				requested = true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("pilot still has resident instances after retirement")
}

type hostedReportBackend struct {
	node, bundle, dsn, endpoint, base, token, prefix string
	completionSecret                                 string
}

func (b hostedReportBackend) command(ctx context.Context, operation, requestID, customer string, contents []byte) *exec.Cmd {
	cmd := exec.CommandContext(ctx, b.node, "../../../examples/gregale-run/test/hosted-intake-driver.mjs",
		b.bundle, requestID, customer, operation)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+b.dsn, "GREGALE_API_URL="+b.base,
		"GREGALE_TEST_TOKEN="+b.token, "REPORT_ENDPOINT_ID="+b.endpoint)
	if b.prefix != "" {
		cmd.Env = append(cmd.Env, "REPORT_TASK_PREFIX="+b.prefix)
	}
	if b.completionSecret != "" {
		cmd.Env = append(cmd.Env, "REPORT_COMPLETION_WEBHOOK_SECRET="+b.completionSecret)
	}
	if contents != nil {
		cmd.Stdin = bytes.NewReader(contents)
	}
	return cmd
}

func (b hostedReportBackend) call(t *testing.T, operation, requestID, customer string, contents []byte, result any) {
	t.Helper()
	raw, err := b.command(t.Context(), operation, requestID, customer, contents).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("Report backend %s: %v\n%s", operation, err, exit.Stderr)
		}
		t.Fatalf("Report backend %s: %v", operation, err)
	}
	requireReport(t, json.Unmarshal(raw, result))
}

type hostedReportSample struct {
	Input          string  `json:"input"`
	SubmitMS       int64   `json:"submit_ms"`
	PublishToUser  int64   `json:"publish_to_agent_ms"`
	SettlementMS   int64   `json:"publish_to_final_ms"`
	DownloadAuthMS int64   `json:"download_authorization_ms"`
	TotalMS        int64   `json:"submit_to_verified_report_ms"`
	RunWallMS      int64   `json:"run_wall_ms_sum"`
	RunCPUMS       int64   `json:"run_cpu_ms_sum"`
	RunCount       int     `json:"physical_runs"`
	ReportBytes    int     `json:"retained_report_bytes"`
	ReportSHA256   string  `json:"retained_report_sha256"`
	ObjectWrites   int     `json:"provider_writes"`
	ObjectReads    int     `json:"provider_reads"`
	ObjectHeads    int     `json:"provider_head_requests"`
	DownloadBytes  int     `json:"verified_download_bytes"`
	RunComputeUSD  float64 `json:"run_compute_estimate_usd"`
	AgentMBSeconds int64   `json:"agent_resident_reserved_mib_seconds"`
	AgentUSD       float64 `json:"agent_compute_estimate_usd"`
	APIDRestarts   int     `json:"apid_restarts"`
	BeforeSubmitMS int64   `json:"-"`
}

func exerciseHostedReport(t *testing.T, f *runFixture, appDB *pgx.Conn, object *reportObjectFixture, store *state.PgStore,
	relay *hostedReportRelay, downloadClient *http.Client, backend hostedReportBackend, rows json.RawMessage, appID, trigger, input string) hostedReportSample {
	t.Helper()
	ctx := t.Context()
	seen, err := store.ListExecutions(ctx, f.accountID, 30, 0)
	requireReport(t, err)
	dispatch, delivery := relay.arm(hostedReportDispatch), relay.arm(hostedReportDelivery)
	thread := uuid.NewString()
	requestID := uuid.NewString()
	var contents []byte
	const owner = "qualification-customer"
	wantOrders, wantCents, wantRuns := 4, 6996, 3
	object.mu.Lock()
	beforeWrites, beforeReads, beforeHeads := object.writes, object.reads, object.heads
	object.mu.Unlock()
	start := time.Now()
	var publisher func()
	if input == "csv" {
		wantOrders, wantCents, wantRuns = 1024, 251904, 1
		var buffer strings.Builder
		buffer.WriteString("order_id,region,quantity,unit_price_cents\n")
		for i := 0; i < wantOrders; i++ {
			fmt.Fprintf(&buffer, "qualification-%d,north,2,123\n", i)
		}
		contents = []byte(buffer.String())
		publisher = func() {
			var result struct{ Status string }
			backend.call(t, "submit", requestID, owner, contents, &result)
			if result.Status != "queued" && result.Status != "running" && result.Status != "succeeded" {
				t.Fatalf("CSV submission returned %s", result.Status)
			}
			requireReport(t, appDB.QueryRow(ctx, "SELECT thread_id::text FROM report_requests WHERE request_id=$1", requestID).Scan(&thread))
		}
	} else {
		publisher = func() {
			_, err := f.client.AppendManagedRealtimeRetainedMessage(ctx, hostedReportApp, backend.endpoint, api.AgentInboxChannel,
				api.ManagedRealtimeRetainedMessageRequest{DataBase64: base64.StdEncoding.EncodeToString(rows), IdempotencyKey: thread,
					Metadata: map[string]string{"thread_id": thread, "message_id": thread, "kind": "user"}})
			requireReport(t, err)
		}
	}
	publisher()
	submitted := time.Now()
	confirmHostedReplay(t, f, dispatch, func() string {
		return hostedReportPipelineID(t, f, backend.endpoint, thread)
	})
	firstReceipt := confirmHostedReplay(t, f, delivery, nil)
	final, received, finished := waitHostedTranscript(t, appDB, thread)
	if final.ID != firstReceipt || final.ReceiptURL != hostedReportDelivery+"/"+final.ID || final.Status != "completed" {
		t.Fatal("the resumed agent did not retain the first durable delivery")
	}
	if input == "csv" {
		var result struct {
			Status  string
			Summary struct {
				Orders  int
				Revenue int `json:"revenue_cents"`
			}
		}
		backend.call(t, "status", requestID, owner, nil, &result)
		if result.Status != "succeeded" || result.Summary.Orders != wantOrders || result.Summary.Revenue != wantCents {
			t.Fatal("CSV backend did not return the verified report summary")
		}
	}
	downloadStart := time.Now()
	var download api.ObjectSignedRequest
	if input == "csv" {
		backend.call(t, "download", requestID, owner, nil, &download)
	} else {
		download, err = f.client.SignRunArtifactDelivery(ctx, hostedReportApp, "reports", final.ID)
		requireReport(t, err)
	}
	if download.Method != http.MethodGet || download.URL == "" {
		t.Fatal("owner did not receive a private download capability")
	}
	downloadMS := time.Since(downloadStart).Milliseconds()
	downloaded := fetchHostedReport(t, downloadClient, download)
	verifiedAt := time.Now()
	totalMS := verifiedAt.Sub(start).Milliseconds()
	publisher()
	published := checkHostedSettlements(t, f, store, backend.endpoint, trigger, thread)
	if input == "csv" {
		var other *api.ObjectSignedRequest
		backend.call(t, "download", requestID, "another-customer", nil, &other)
		if other != nil {
			t.Fatal("another customer received a download capability")
		}
	}
	object.mu.Lock()
	body, writes := bytes.Clone(object.body), object.writes-beforeWrites
	reads, heads := object.reads-beforeReads, object.heads-beforeHeads
	object.mu.Unlock()
	if writes != 1 || final.SizeBytes != int64(len(body)) || !bytes.Equal(body, downloaded) ||
		!bytes.Contains(body, []byte(strconv.Itoa(wantCents))) || !bytes.Contains(body, []byte(strconv.Itoa(wantOrders)+" orders")) {
		t.Fatal("the object store did not retain exactly one correct report")
	}
	sample := hostedReportSample{Input: input, SubmitMS: submitted.Sub(start).Milliseconds(),
		PublishToUser: received.Sub(published).Milliseconds(), SettlementMS: finished.Sub(published).Milliseconds(),
		DownloadAuthMS: downloadMS, TotalMS: totalMS, ReportBytes: len(body), ReportSHA256: fmt.Sprintf("%x", sha256.Sum256(body)),
		ObjectWrites: writes, ObjectReads: reads, ObjectHeads: heads, DownloadBytes: len(downloaded), APIDRestarts: 2}
	recordHostedRunCosts(t, f, store, seen, wantRuns, &sample)
	sample.AgentMBSeconds = hostedAppResidency(t, store, appID, start, verifiedAt)
	sample.AgentUSD = float64(sample.AgentMBSeconds) * hostedComputeRate()
	t.Logf("%s report orders=%d cents=%d run_count=%d object_writes=%d total_ms=%d", input,
		wantOrders, wantCents, sample.RunCount, sample.ObjectWrites, sample.TotalMS)
	encoded, err := json.Marshal(sample)
	requireReport(t, err)
	t.Logf("hosted journey measurements %s", encoded)
	return sample
}

func hostedReportPipelineID(t *testing.T, f *runFixture, endpoint, thread string) string {
	t.Helper()
	var count int
	var id string
	err := f.h.Pool.QueryRow(t.Context(), `SELECT count(*), coalesce(min(p.job_id::text), '')
FROM execution_workflow_pipelines AS p
JOIN agent_execution_workflows AS j ON j.id=p.job_id
WHERE j.account_id=$1 AND p.policy #>> '{callback,endpoint_id}'=$2 AND p.policy #>> '{callback,thread_id}'=$3`,
		f.accountID, endpoint, thread).Scan(&count, &id)
	requireReport(t, err)
	if count != 1 || id == "" {
		t.Fatalf("expected one durable Agent pipeline, got %d", count)
	}
	return id
}

func fetchHostedReport(t *testing.T, client *http.Client, signed api.ObjectSignedRequest) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, signed.URL, nil)
	if err != nil {
		t.Fatal("invalid fixture download capability")
	}
	for key, value := range signed.Headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("fixture report download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Report download returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 32769))
	requireReport(t, err)
	return body
}

func hostedAppResidency(t *testing.T, store *state.PgStore, appID string, start, verifiedAt time.Time) int64 {
	t.Helper()
	instances, err := store.ListInstancesForApp(t.Context(), appID)
	requireReport(t, err)
	seconds, err := store.InstanceBillingSeconds(t.Context(), start, verifiedAt)
	requireReport(t, err)
	var total int64
	for _, instance := range instances {
		total += int64(api.BillableRAMMB(instance.RAMMB)) * seconds[instance.ID]
	}
	if total == 0 {
		t.Fatal("missing hosted Agent residency measurement")
	}
	return total
}

func hostedComputeRate() float64 {
	return float64(api.OverageMillicentsPerGBHour) / (100_000 * 1024 * 3600)
}

func recordHostedRunCosts(t *testing.T, f *runFixture, store *state.PgStore, before []state.Execution, want int, out *hostedReportSample) {
	t.Helper()
	current, err := store.ListExecutions(t.Context(), f.accountID, 30, 0)
	requireReport(t, err)
	old := make(map[string]bool, len(before))
	for _, row := range before {
		old[row.ID] = true
	}
	for _, row := range current {
		if old[row.ID] {
			continue
		}
		f.runs[row.ID] = true
		f.wait(t, row.ID, api.ExecutionStatusSucceeded)
		var wall, cpu int64
		requireReport(t, f.h.Pool.QueryRow(t.Context(), "SELECT wall_time_ms,cpu_time_ms FROM execution_usage_ledger WHERE execution_id=$1", row.ID).Scan(&wall, &cpu))
		out.RunCount++
		out.RunWallMS += wall
		out.RunCPUMS += cpu
		out.RunComputeUSD += float64(row.Limits.MemoryMB) * float64(wall) / 1000 * hostedComputeRate()
	}
	if out.RunCount != want || out.RunWallMS <= 0 {
		t.Fatalf("expected %d settled Runs with nonzero wall time, got %d", want, out.RunCount)
	}
}

func waitHostedTranscript(t *testing.T, db *pgx.Conn, thread string) (api.RunArtifactDelivery, time.Time, time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	for ctx.Err() == nil {
		var delivery []byte
		var received, finished time.Time
		err := db.QueryRow(ctx, `SELECT tool.content->'report'->'delivery', request.created_at, final.created_at
FROM agent_messages AS request JOIN agent_messages AS tool ON tool.thread_id=request.thread_id AND tool.role='tool'
JOIN agent_messages AS final ON final.thread_id=tool.thread_id AND final.message_id='assistant:'||tool.message_id
WHERE request.thread_id=$1 AND request.role='user' AND final.content->'final'='true'::jsonb`, thread).
			Scan(&delivery, &received, &finished)
		if err == pgx.ErrNoRows {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		requireReport(t, err)
		var receipt api.RunArtifactDelivery
		requireReport(t, json.Unmarshal(delivery, &receipt))
		var count int
		requireReport(t, db.QueryRow(ctx, "SELECT count(*) FROM agent_messages WHERE thread_id=$1", thread).Scan(&count))
		if count != 4 {
			t.Fatalf("replayed inbox changed transcript count: %d", count)
		}
		return receipt, received, finished
	}
	t.Fatal("deployed agent did not persist its final report")
	return api.RunArtifactDelivery{}, time.Time{}, time.Time{}
}

func checkHostedSettlements(t *testing.T, f *runFixture, store *state.PgStore, endpoint, trigger, thread string) time.Time {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		history, err := store.ReadManagedRealtimeChannelHistory(ctx, endpoint, api.AgentInboxChannel, 0, 16)
		requireReport(t, err)
		var expected []string
		var published time.Time
		for _, row := range history.Messages {
			if row.Metadata["thread_id"] == thread {
				expected = append(expected, strconv.FormatInt(row.Sequence, 10))
				if row.Metadata["kind"] == "user" {
					published = row.CreatedAt
				}
			}
		}
		if len(expected) != 2 {
			t.Fatalf("expected one user record and one tool result, found %d", len(expected))
		}
		if !published.IsZero() && hostedTriggerSettled(ctx, f, trigger, expected) {
			return published
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the real realtime trigger did not settle both redelivered records")
	return time.Time{}
}

func hostedTriggerSettled(ctx context.Context, f *runFixture, trigger string, sequences []string) bool {
	count := 0
	err := f.h.Pool.QueryRow(ctx, `SELECT count(*) FROM trigger_records
WHERE trigger_id=$1 AND split_part(item_identifier, ':', 1)=ANY($2) AND state='succeeded'`, trigger, sequences).Scan(&count)
	return err == nil && count == 2
}
