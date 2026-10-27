//go:build metal

// adr: 20261027090010 — Milestone A on one host: a signed GitHub pull_request delivery
// reaches githubd, becomes a gregale.github event, starts the account's
// triage automation, and its agent step runs Claude Code in a microVM whose
// model calls go through vmmd's relay and outboundd to Anthropic.
//
// The agent base comes one of two ways. FAAS_E2E_AGENT_RUN_BASE is an ext4
// built for the run, admitted with a fixture scan sidecar. FAAS_E2E_AGENT_BASE_LAYER
// is the agent-base root filesystem as one gzipped tar layer: the harness's
// imaged pulls it from a local registry and stages both agent drives the way
// an agent-pool node does, with its own guest-init and a real Grype scan,
// and schedd boots runs from the keys imaged staged them at.
package agentchain_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	triageInstallation = 4242
	triageRepository   = "gregale/faas"
	// keylessPrompt and keylessRun stand in for Claude Code when no
	// Anthropic key is set: the harness sends one Messages request through
	// the relay, which outboundd authorizes against the run and forwards to
	// Anthropic with the integration's placeholder credential, and writes
	// the triage itself with the status Anthropic answered.
	keylessPrompt = "Triage pull request #{{inputs.number}} in {{inputs.repository}}: {{inputs.title}}"
	keylessRun    = `const fs=require("fs");(async()=>{let status=0,body="";` +
		`try{const r=await fetch(process.env.ANTHROPIC_BASE_URL+"/v1/messages",{method:"POST",headers:{"content-type":"application/json",` +
		`"anthropic-version":"2023-06-01","x-api-key":process.env.ANTHROPIC_API_KEY||"relay"},body:JSON.stringify({model:"claude-haiku-4-5",` +
		`max_tokens:8,messages:[{role:"user",content:"ping"}]})});status=r.status;body=(await r.text()).slice(0,300)}catch(e){body=String(e)}` +
		`const prompt=fs.readFileSync("/input/prompt.txt","utf8");fs.mkdirSync("/work/output",{recursive:true});` +
		`fs.writeFileSync("/work/output/result.json",JSON.stringify({summary:prompt.split("\n")[0].slice(0,200),risk:"low",` +
		`labels:["keyless"],relay_status:status,relay_body:body}))})()`
	triagePrompt = "You are triaging pull request #{{inputs.number}} in {{inputs.repository}}, titled \"{{inputs.title}}\". " +
		"Do not run commands or read files. Write a JSON object with the keys \"summary\" (one sentence), \"risk\" " +
		"(\"low\", \"medium\" or \"high\") and \"labels\" (an array of at most three short strings) to the file " +
		"output/result.json in the current directory, and nothing else."
)

// agentLayer builds the platform agent layer: drive1 holding only the agent
// marker, with room for the run's writes.
func agentLayer(t *testing.T, dir string) string {
	t.Helper()
	skel := filepath.Join(dir, "layer-skel")
	marker := filepath.Join(skel, "upper", "etc", "faas", "agent.json")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(`{"kind":"agent","version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(dir, "agent-layer.ext4")
	for _, args := range [][]string{{"truncate", "-s", "512M", layer},
		{"mkfs.ext4", "-q", "-O", "^has_journal", "-d", skel, "-L", "agent-layer", "-F", layer}} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", args[0], err, out)
		}
	}
	return layer
}

// serveAgentBaseToImaged serves the agent-base layer from a local registry
// and returns the environment that has the harness's imaged stage it. Storage
// moves to the test's scratch directory: the staged base is about 1 GB, and
// it must not land in, or outlive the test in, the host's shared /srv/fc.
func serveAgentBaseToImaged(t *testing.T, layerPath string) []string {
	t.Helper()
	layer, err := os.ReadFile(layerPath)
	if err != nil {
		t.Fatalf("read agent base layer: %v", err)
	}
	storageRoot := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_STORAGE_ROOT", storageRoot)
	t.Setenv("FAAS_E2E_IMAGED_READY_TIMEOUT", "40m")
	// vmmd boots runs from the kernel at its storage key, which on this host
	// lives under /srv/fc; the moved root needs its own copy.
	kernel, err := os.ReadFile(os.Getenv("FAAS_TEST_KERNEL"))
	if err != nil {
		t.Fatalf("read kernel: %v", err)
	}
	kernelPath := filepath.Join(storageRoot, sched.KernelKey(os.Getenv("FAAS_TEST_FC_VERSION")))
	if err := os.MkdirAll(filepath.Dir(kernelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kernelPath, kernel, 0o644); err != nil {
		t.Fatalf("stage kernel: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	builder, _ := e2etest.RootfsLayerImage(registry.Host()+"/builder-base", inertBuilderBaseLayer(t))
	e2etest.OverrideBuilderBase(t, registry.AddImage("builder-base", builder))
	image, _ := e2etest.RootfsLayerImage(registry.Host()+"/agent-base", layer)
	ref := registry.AddImage("agent-base", image)
	t.Logf("agent base served as %s (%d MB layer)", ref, len(layer)>>20)
	return []string{"FAAS_STORAGE_ROOT=" + storageRoot, "FAAS_AGENT_BASE_REF=" + ref}
}

// inertBuilderBaseLayer is a builder base imaged validates and stages at
// startup but this test never boots: inert files at the builder's required
// paths. It is built here because the e2etest fixture compiles a server from
// source, which a prebuilt test binary on a native host cannot.
func inertBuilderBaseLayer(t *testing.T) []byte {
	t.Helper()
	var layer bytes.Buffer
	zw := gzip.NewWriter(&layer)
	tw := tar.NewWriter(zw)
	body := []byte("#!/bin/sh\nexit 1\n")
	for _, name := range []string{"usr/local/bin/faas-guest-init", "usr/local/bin/railpack", "usr/local/bin/buildctl", "usr/local/bin/runc", "bin/bash"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return layer.Bytes()
}

// checkStagedAgentDrives confirms imaged staged both drives where schedd's
// defaults look for them, and reports the Grype verdict vmmd's gate reads.
func checkStagedAgentDrives(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	backend, err := storage.BackendFromEnvContext(ctx)
	if err != nil {
		t.Fatalf("storage backend: %v", err)
	}
	baseKey := sched.BaseKeyForArch(sched.AgentRunBaseRuntime, "amd64")
	for _, key := range []string{baseKey, sched.AgentRunLayerKeyForArch("amd64")} {
		rc, err := backend.Get(ctx, key)
		if err != nil {
			t.Fatalf("imaged did not stage %s: %v", key, err)
		}
		_ = rc.Close()
	}
	rc, err := backend.Get(ctx, wire.ScanKeyForBaseKey(baseKey))
	if err != nil {
		t.Fatalf("imaged wrote no scan sidecar for %s: %v", baseKey, err)
	}
	defer func() { _ = rc.Close() }()
	var scan struct {
		Image        string         `json:"image"`
		Findings     map[string]int `json:"findings"`
		FixAvailable map[string]int `json:"fix_available_findings"`
	}
	if err := json.NewDecoder(rc).Decode(&scan); err != nil {
		t.Fatalf("decode scan sidecar: %v", err)
	}
	t.Logf("imaged staged the agent drives; Grype on %s: findings %v, fix available %v", scan.Image, scan.Findings, scan.FixAvailable)
	if scan.FixAvailable["CRITICAL"] >= 9999 {
		// The fail-closed placeholder: no scan completed, and vmmd will refuse
		// the base. The stored evidence names the failure.
		var evidence string
		if err := pool.QueryRow(ctx, `SELECT coalesce(string_agg((input_snapshot - 'report')::text, '; '), '')
			FROM base_image_scans WHERE storage_key = $1`, baseKey).Scan(&evidence); err != nil {
			evidence = err.Error()
		}
		t.Fatalf("imaged's scan of %s did not complete; vmmd would refuse the base. Stored scans: %s", baseKey, evidence)
	}
}

// admitAgentBase publishes the base's admission sidecar where vmmd's scan
// gate (issue #299) reads it, and removes it when the test ends. The base is
// built for this run, so the sidecar is a fixture, not Grype evidence.
func admitAgentBase(t *testing.T, ctx context.Context, base string) {
	t.Helper()
	backend, err := storage.BackendFromEnvContext(ctx)
	if err != nil {
		t.Fatalf("storage backend: %v", err)
	}
	scan, err := json.Marshal(map[string]any{"image": "agent-base-e2e", "findings": map[string]int{"CRITICAL": 0},
		"fix_available_findings": map[string]int{"CRITICAL": 0}})
	if err != nil {
		t.Fatal(err)
	}
	key := wire.ScanKeyForBaseKey(base)
	if err := backend.Put(ctx, key, bytes.NewReader(scan)); err != nil {
		t.Fatalf("publish agent base admission fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Delete(context.WithoutCancel(ctx), key); err != nil {
			t.Logf("remove agent base admission fixture %s: %v", key, err)
		}
	})
}

type apiClient struct {
	t     *testing.T
	base  string
	token string
}

func (c apiClient) do(method, path string, body, out any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		// Responses never echo a credential; the request body is not logged.
		c.t.Fatalf("%s %s = %d %s", method, path, res.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
}

// pullRequestDelivery is a pull_request delivery shaped as GitHub sends it.
func pullRequestDelivery(t *testing.T) []byte {
	t.Helper()
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	body, err := json.Marshal(map[string]any{
		"action": "opened", "number": 4321,
		"pull_request": map[string]any{
			"number": 4321, "title": "Retry the wake when the snapshot drive is still being written", "state": "open",
			"draft": false, "merged": false, "html_url": "https://github.com/" + triageRepository + "/pull/4321",
			"user":   map[string]any{"login": "octocat"},
			"labels": []any{},
			"head":   map[string]any{"ref": "fix/wake-retry", "sha": head, "repo": map[string]any{"full_name": triageRepository}},
			"base":   map[string]any{"ref": "main", "sha": base, "repo": map[string]any{"full_name": triageRepository}},
		},
		"repository":   map[string]any{"full_name": triageRepository, "default_branch": "main", "private": true},
		"installation": map[string]any{"id": triageInstallation},
		"sender":       map[string]any{"login": "octocat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestMetalAgentPRTriage(t *testing.T) {
	key, base := os.Getenv("FAAS_E2E_ANTHROPIC_API_KEY"), os.Getenv("FAAS_E2E_AGENT_RUN_BASE")
	baseLayer := os.Getenv("FAAS_E2E_AGENT_BASE_LAYER")
	if (base == "" && baseLayer == "") || os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("set FAAS_E2E_AGENT_RUN_BASE (an agent-base ext4 with this checkout's guest-init) or FAAS_E2E_AGENT_BASE_LAYER " +
			"(the agent-base rootfs as a gzipped tar layer, staged by imaged), and FAAS_TEST_KERNEL; " +
			"FAAS_E2E_ANTHROPIC_API_KEY runs Claude Code, and without it a keyless harness stands in")
	}
	keyless := key == ""
	if keyless {
		key = "sk-ant-e2e-placeholder"
	}
	// The daemons inherit this process's environment; the key reaches the
	// platform only through the credential API below.
	_ = os.Unsetenv("FAAS_E2E_ANTHROPIC_API_KEY")
	ctx := context.Background()
	which := e2etest.APID | e2etest.Schedd | e2etest.VMMD | e2etest.GatewaySynthStub | e2etest.Agentd | e2etest.Outboundd | e2etest.Githubd
	env := []string{"FAAS_AI_GATEWAY_ENABLED=1", "FAAS_WORKFLOWS_ENABLED=1", "FAAS_SCHEDD_AGENT_RUNS=1",
		"FAAS_SCHEDD_AGENT_STEPS=1", "FAAS_SCHEDD_AGENT_RUN_NODES=default-local", "FAAS_VMMD_AGENT_RUNS=1"}
	if baseLayer != "" {
		which |= e2etest.Imaged
		env = append(env, serveAgentBaseToImaged(t, baseLayer)...)
	} else {
		admitAgentBase(t, ctx, base)
		env = append(env, "FAAS_SCHEDD_AGENT_RUN_BASE_KEY="+base, "FAAS_SCHEDD_AGENT_RUN_LAYER_KEY="+agentLayer(t, t.TempDir()))
	}
	pool := pgtest.OpenMigrated(t)
	h := e2etest.Start(t, pool, which, env...)
	defer func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	}()
	if baseLayer != "" {
		checkStagedAgentDrives(t, ctx, pool)
		// imaged's part is done; it must not build the deployment written below.
		if err := h.KillImaged(); err != nil {
			t.Fatal(err)
		}
	}
	token := h.SeedAccount(ctx, api.PlanPro, "agents")
	store := state.NewPgStore(pool)
	acct, err := store.AccountByEmail(ctx, "e2e+pro+agents@test.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: acct.ID, InstallationID: triageInstallation,
		SealedToken: []byte("unused"), TokenExpiresAt: time.Now().Add(time.Hour), AuditGithubLogin: "e2e"}); err != nil {
		t.Fatal(err)
	}
	client := apiClient{t: t, base: h.APIDURL, token: token}

	// The account's own Anthropic key, behind the AI gateway's spend cap.
	var integration api.OutboundIntegrationOffer
	client.do(http.MethodPost, "/v1/outbound/integrations", api.CreateOutboundIntegrationRequest{Name: "anthropic",
		Origin: "https://api.anthropic.com", AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"},
		AIProvider: "anthropic"}, &integration)
	client.do(http.MethodPut, "/v1/outbound/integrations/"+integration.ID+"/credential", api.PutOutboundCredentialRequest{Authorization: key}, nil)
	client.do(http.MethodPut, "/v1/outbound/integrations/"+integration.ID+"/ai-prices", api.PutOutboundAIPricesRequest{Entries: []api.OutboundAIPriceEntry{
		{ModelPrefix: "claude-opus", InputMillicentsPerMTok: 1_500_000, OutputMillicentsPerMTok: 7_500_000},
		{ModelPrefix: "claude-", InputMillicentsPerMTok: 300_000, OutputMillicentsPerMTok: 1_500_000},
	}}, nil)
	harness, prompt := api.AgentHarness{Preset: "claude-code"}, triagePrompt
	if keyless {
		harness, prompt = api.AgentHarness{Preset: "claude-code", Run: []string{"node", "-e", keylessRun}}, keylessPrompt
	}
	client.do(http.MethodPost, "/v1/agents", api.CreateAgentRequest{Name: "pr-triage", AgentVersionSpec: api.AgentVersionSpec{
		Harness: harness, PromptTemplate: prompt, TimeoutSec: 900,
		Integrations: map[string]string{"anthropic": integration.ID}, AIBudgetMillicents: 200_000}}, nil)

	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "triage-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64),
		Status: state.DeployPending, Workflows: json.RawMessage(`[{"name":"triage","trigger":{"type":"event","source":"gregale.github",` +
			`"event_type":"pull_request.*","filter":{"data":{"pull_request":{"fork":false}}}},"steps":[{"name":"review","agent":"pr-triage",` +
			`"input":{"inputs":{"repository":"{{input.data.repository.full_name}}","number":"{{input.data.pull_request.number}}",` +
			`"title":"{{input.data.pull_request.title}}"}}}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}

	body := pullRequestDelivery(t)
	mac := hmac.New(sha256.New, []byte(h.AgentDaemons.GitHubWebhookSecret))
	mac.Write(body)
	req, err := http.NewRequest(http.MethodPost, h.AgentDaemons.GithubdURL+"/webhooks/github", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", uuid.NewString())
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("githubd webhook = %d", res.StatusCode)
	}
	t.Logf("pull_request delivered to githubd at %s", time.Now().UTC().Format(time.RFC3339))

	runID, output := waitForTriage(t, ctx, h, app.ID, 25*time.Minute)
	var step api.WorkflowAgentStepOutput
	if err := json.Unmarshal(output, &step); err != nil || step.State != "succeeded" {
		t.Fatalf("workflow %s step output %s: %v", runID, output, err)
	}
	var result struct {
		Summary     string   `json:"summary"`
		Risk        string   `json:"risk"`
		Labels      []string `json:"labels"`
		RelayStatus int      `json:"relay_status"`
		RelayBody   string   `json:"relay_body"`
	}
	if err := json.Unmarshal(step.Result, &result); err != nil || result.Summary == "" {
		t.Fatalf("agent run %s result %s: %v", step.RunID, step.Result, err)
	}
	// Anthropic refusing the placeholder key is the evidence the call left
	// the run VM, passed outboundd's run authorization and reached it.
	if keyless && result.RelayStatus != http.StatusUnauthorized {
		t.Fatalf("keyless relay call = %d %s, want 401 from Anthropic", result.RelayStatus, result.RelayBody)
	}
	var spent, calls int64
	if err := pool.QueryRow(ctx, `SELECT coalesce(sum(spent_millicents),0), coalesce(sum(calls),0) FROM agent_run_ai_spend WHERE run_id = $1`,
		step.RunID).Scan(&spent, &calls); err != nil {
		t.Logf("agent run spend unavailable: %v", err)
	}
	t.Logf("triage of PR #4321 by agent run %s (keyless=%v): risk=%s labels=%v relay_status=%d model_calls=%d spend=%d millicents\nsummary: %s",
		step.RunID, keyless, result.Risk, result.Labels, result.RelayStatus, calls, spent, result.Summary)
}

// waitForTriage waits for the automation's workflow run to end and returns
// its agent step's output.
func waitForTriage(t *testing.T, ctx context.Context, h *e2etest.Harness, appID string, limit time.Duration) (string, json.RawMessage) {
	t.Helper()
	deadline := time.Now().Add(limit)
	var last string
	for time.Now().Before(deadline) {
		// The output stays text: a cast to bytea reads its JSON escapes
		// (\") as bytea escapes and fails.
		var runID, status, output string
		err := h.Pool.QueryRow(ctx, `SELECT r.id::text, r.status, coalesce(s.output::text, '')
			FROM workflow_runs r LEFT JOIN workflow_steps s ON s.run_id = r.id AND s.step_name = 'review'
			WHERE r.app_id = $1 ORDER BY r.created_at DESC LIMIT 1`, appID).Scan(&runID, &status, &output)
		var agent string
		if err == nil {
			err = h.Pool.QueryRow(ctx, `SELECT state FROM agent_runs WHERE workflow_run_id = $1::uuid ORDER BY queued_at DESC LIMIT 1`, runID).Scan(&agent)
		}
		now := fmt.Sprintf("workflow=%s %s agent_run=%s", runID, status, agent)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			now = "poll failed: " + err.Error()
		}
		if now != last {
			t.Logf("%s: %s", time.Now().UTC().Format("15:04:05"), now)
			last = now
		}
		switch status {
		case "succeeded":
			return runID, json.RawMessage(output)
		case "failed", "cancelled", "timed_out":
			t.Fatalf("workflow run %s ended %s with output %s", runID, status, output)
		}
		if strings.HasSuffix(last, " awaiting_event agent_run=succeeded") && time.Since(succeededAt(t, ctx, h, runID)) > 2*time.Minute {
			dumpAgentStep(t, ctx, h)
			t.Fatalf("the agent run succeeded over 2 minutes ago but its workflow step still waits (last: %s)", last)
		}
		time.Sleep(5 * time.Second)
	}
	dumpAgentStep(t, ctx, h)
	t.Fatalf("no triage within %s (last: %s)", limit, last)
	return "", nil
}

// succeededAt is when the workflow run's agent run finished.
func succeededAt(t *testing.T, ctx context.Context, h *e2etest.Harness, runID string) time.Time {
	t.Helper()
	var at time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT finished_at FROM agent_runs WHERE workflow_run_id = $1::uuid AND finished_at IS NOT NULL
		ORDER BY queued_at DESC LIMIT 1`, runID).Scan(&at); err != nil {
		return time.Now()
	}
	return at
}

// dumpAgentStep logs what schedd's agent step delivery reads, the waits
// Postgres sees, and schedd's goroutine stacks, for a step that never resumed.
func dumpAgentStep(t *testing.T, ctx context.Context, h *e2etest.Harness) {
	t.Helper()
	for _, q := range []struct{ name, sql string }{
		{"agent runs", `SELECT id, state, trigger, coalesce(outcome_code, ''), finished_at, workflow_run_id, workflow_step, workflow_event FROM agent_runs ORDER BY queued_at DESC LIMIT 3`},
		{"agent outputs", `SELECT run_id, left(coalesce(result::text, '<null>'), 400) FROM agent_run_outputs LIMIT 3`},
		{"workflow runs", `SELECT id, status, resume_count, coalesce(last_error, '') FROM workflow_runs ORDER BY created_at DESC LIMIT 3`},
		{"workflow steps", `SELECT run_id, step_name, status, attempt FROM workflow_steps ORDER BY run_id LIMIT 10`},
		{"workflow events", `SELECT run_id, event_name, left(coalesce(payload::text, ''), 200), received_at FROM workflow_events LIMIT 10`},
		{"undelivered ends", `SELECT r.id FROM agent_runs r WHERE r.workflow_run_id IS NOT NULL AND r.finished_at IS NOT NULL
			AND EXISTS (SELECT 1 FROM workflow_runs w WHERE w.id=r.workflow_run_id AND w.status IN ('pending','running','awaiting_event'))
			AND NOT EXISTS (SELECT 1 FROM workflow_events e WHERE e.run_id=r.workflow_run_id AND e.event_name=r.workflow_event)`},
		{"active queries", `SELECT pid, state, coalesce(wait_event_type, ''), coalesce(wait_event, ''), now() - query_start, left(query, 160)
			FROM pg_stat_activity WHERE datname = current_database() AND state <> 'idle' AND pid <> pg_backend_pid()`},
	} {
		rows, err := h.Pool.Query(ctx, q.sql)
		if err != nil {
			t.Logf("dump %s: %v", q.name, err)
			continue
		}
		for rows.Next() {
			values, err := rows.Values()
			t.Logf("dump %s: %v %v", q.name, values, err)
		}
		rows.Close()
	}
	if err := h.DumpStacks("schedd"); err != nil {
		t.Logf("dump schedd stacks: %v", err)
	}
}
