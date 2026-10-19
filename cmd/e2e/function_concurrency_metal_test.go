//go:build metal

package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestFunctionConcurrencyMetal is the ADR-1342 acceptance on real guests. A
// binary function whose handler speaks the shared worker protocol, and real
// node22 and python312 (async and sync) functions, each deployed with
// function_concurrency=8, serve eight slow requests at once from one worker
// process, and a request its client abandons does not restart that worker.
// Each guest's writable root also has the plan's ephemeral disk (#2529). The
// gregale CLI then deploys a functions manifest: per-function settings, env
// and secrets reach each app before its build, the entry function's host
// routes /users/* to another function, queue bindings and event triggers
// reach the function, and prune_env removes an undeclared key.
func TestFunctionConcurrencyMetal(t *testing.T) {
	requireFunctionConcurrencyFixtures(t)
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal function concurrency test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping metal function concurrency test")
	}
	runner := os.Getenv("FAAS_E2E_FUNCTION_RUNNER_GO124")
	if runner == "" {
		t.Skip("FAAS_E2E_FUNCTION_RUNNER_GO124 unset; binary functions need the real go124 runner")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	env := append(secretSealingEnv(t), "FAAS_FUNCTION_RUNNER_GO124="+runner)
	nodeRunner := os.Getenv("FAAS_E2E_FUNCTION_RUNNER_NODE22")
	pythonRunner := os.Getenv("FAAS_E2E_FUNCTION_RUNNER_PYTHON312")
	if nodeRunner != "" {
		env = append(env, "FAAS_FUNCTION_RUNNER_NODE22="+nodeRunner)
	}
	if pythonRunner != "" {
		env = append(env, "FAAS_FUNCTION_RUNNER_PYTHON312="+pythonRunner)
	}
	h := e2etest.Start(t, pool, e2etest.All, env...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanPro)

	t.Run("shared worker", func(t *testing.T) {
		result := deployBinaryFunctionWith(t, h, pool, key, "concfn", sharedWorkerCHandlerSource(t),
			func(req *api.CreateAppRequest) { req.FunctionConcurrency = 8 })
		exerciseSharedFunction(t, h, key, result.slug, "/hang", "1")
	})

	t.Run("interpreter workers", func(t *testing.T) {
		for _, tc := range []struct {
			name, runtime, runner, file, source string
			abandon, wantCancels                string
		}{
			// Node aborts this request's signal and waits for cleanup.
			{"node", "node22", nodeRunner, "handler.js", nodeSharedHandler, "/hang", "1"},
			// Python cancels an async handler's task, which counts it.
			{"python async", "python312", pythonRunner, "handler.py", pythonAsyncSharedHandler, "/hang", "1"},
			// A sync handler's thread finishes; the cancel is acknowledged then.
			{"python sync", "python312", pythonRunner, "handler.py", pythonSyncSharedHandler, "/abandon", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if tc.runner == "" {
					t.Skipf("FAAS_E2E_FUNCTION_RUNNER_%s unset; %s functions need the real runner", strings.ToUpper(tc.runtime), tc.runtime)
				}
				slug := "conc-" + strings.ReplaceAll(tc.name, " ", "-")
				result := deployRuntimeFunction(t, h, pool, key, slug, tc.runtime, tc.file, tc.source, 8)
				exerciseSharedFunction(t, h, key, result.slug, tc.abandon, tc.wantCancels)
			})
		}
	})

	t.Run("functions manifest", func(t *testing.T) {
		cli := os.Getenv("FAAS_E2E_GREGALE_BIN")
		if cli == "" {
			t.Skip("FAAS_E2E_GREGALE_BIN unset; the manifest phase runs the gregale CLI")
		}
		project := filepath.Join(t.TempDir(), "shop")
		source := sharedWorkerCHandlerFiles()
		for _, root := range []string{"web", "users"} {
			if err := os.MkdirAll(filepath.Join(project, root), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range source {
				if err := os.WriteFile(filepath.Join(project, root, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		manifest := `functions:
  - name: web
    root: web
    runtime: binary
    entry: true
    env:
      WHO: web
  - name: users
    root: users
    runtime: binary
    concurrency: 4
    request_timeout_s: 120
    env:
      WHO: users
    prune_env: true
    secrets_from_env: [USERS_TOKEN]
    routes: ["/users/*"]
    queue_bindings:
      - name: jobs
        queue_name: user-jobs
    event_triggers:
      - source: shop.*
        type: user.created
`
		if err := os.WriteFile(filepath.Join(project, "gregale.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, "deploy", "--path", project, "--name", "shop", "--no-doctor")
		cmd.Dir = filepath.Dir(project)
		home := t.TempDir()
		cmd.Env = append(os.Environ(), "FAAS_API="+h.APIDURL, "FAAS_TOKEN="+key, "HOME="+home,
			"XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "USERS_TOKEN=s3cret-users-token", "NO_COLOR=1")
		started := time.Now()
		out, err := cmd.CombinedOutput()
		t.Logf("gregale deploy (%v):\n%s", time.Since(started).Round(time.Second), out)
		if err != nil {
			t.Fatalf("gregale deploy: %v", err)
		}
		if strings.Contains(string(out), "s3cret-users-token") {
			t.Fatal("the secret value reached the CLI output")
		}

		var users api.AppResponse
		mustGetJSON(t, h, key, "/v1/apps/shop-users", &users)
		if users.FunctionConcurrency != 4 || users.RequestTimeoutS != 120 {
			t.Fatalf("shop-users settings: function_concurrency=%d request_timeout_s=%d", users.FunctionConcurrency, users.RequestTimeoutS)
		}
		var secrets api.AppSecretListResponse
		mustGetJSON(t, h, key, "/v1/apps/shop-users/secrets", &secrets)
		if !containsSecret(secrets, "USERS_TOKEN") {
			t.Fatalf("shop-users secrets = %+v; want USERS_TOKEN", secrets.Secrets)
		}
		var web api.AppResponse
		mustGetJSON(t, h, key, "/v1/apps/shop-web", &web)
		entry, err := url.Parse(web.URL)
		if err != nil || entry.Hostname() == "" {
			t.Fatalf("entry app URL %q: %v", web.URL, err)
		}
		routed := sharedWorkerGet(t, h, key, entry.Hostname(), "/users/42", time.Minute)
		fallthrough_ := sharedWorkerGet(t, h, key, entry.Hostname(), "/about", time.Minute)
		t.Logf("ADR-1342 evidence: %s/users/42 -> who=%s slots=%s secret bytes=%s; %s/about -> who=%s slots=%s",
			entry.Hostname(), routed.who, routed.slots, routed.tok, entry.Hostname(), fallthrough_.who, fallthrough_.slots)
		if routed.who != "users" || routed.slots != "4" {
			t.Fatalf("/users/42 on the entry host reached who=%s slots=%s; want the users function built with concurrency 4", routed.who, routed.slots)
		}
		if routed.tok != fmt.Sprint(len("s3cret-users-token")) {
			t.Fatalf("users function saw a %s-byte USERS_TOKEN; want the sealed secret from the deployer's environment", routed.tok)
		}
		if fallthrough_.who != "web" {
			t.Fatalf("/about on the entry host reached who=%s; want the entry function", fallthrough_.who)
		}
		var bindings []api.QueueBindingResponse
		mustGetJSON(t, h, key, "/v1/apps/shop-users/queue-bindings", &bindings)
		var subscriptions api.EventSubscriptionListResponse
		mustGetJSON(t, h, key, "/v1/apps/shop-users/event-subscriptions", &subscriptions)
		t.Logf("ADR-1342 evidence: shop-users queue bindings %+v; event subscriptions %+v", bindings, subscriptions.Subscriptions)
		if len(bindings) != 1 || bindings[0].Name != "jobs" || bindings[0].QueueName != "user-jobs" || bindings[0].Mode != "push" || bindings[0].WorkloadClass != "http" {
			t.Fatalf("shop-users queue bindings = %+v; want one push/http binding jobs → user-jobs", bindings)
		}
		if len(subscriptions.Subscriptions) != 1 || subscriptions.Subscriptions[0].Source != "shop.*" || subscriptions.Subscriptions[0].Type != "user.created" {
			t.Fatalf("shop-users event subscriptions = %+v; want shop.* / user.created", subscriptions.Subscriptions)
		}

		// A key set by hand on a prune_env function is gone after the next
		// deploy, which otherwise converges without new rules or subscriptions.
		if body, status := doReq(t, h, key, http.MethodPut, "/v1/apps/shop-users/env/EXTRA", api.PutAppEnvRequest{Value: "1"}); status/100 != 2 {
			t.Fatalf("PUT env EXTRA: status=%d body=%s", status, body)
		}
		again := exec.CommandContext(ctx, cli, "deploy", "--path", project, "--name", "shop", "--no-doctor")
		again.Dir, again.Env = cmd.Dir, cmd.Env
		if out, err := again.CombinedOutput(); err != nil {
			t.Fatalf("second gregale deploy: %v\n%s", err, out)
		}
		var rules []api.EdgeRuleResponse
		mustGetJSON(t, h, key, "/v1/apps/shop-web/edge-rules", &rules)
		managed := 0
		for _, rule := range rules {
			if strings.HasPrefix(rule.Name, "functions:") {
				managed++
			}
		}
		if managed != 1 {
			t.Fatalf("entry app has %d managed route rules after a repeat deploy; want 1: %+v", managed, rules)
		}
		var envList api.AppEnvListResponse
		mustGetJSON(t, h, key, "/v1/apps/shop-users/env", &envList)
		mustGetJSON(t, h, key, "/v1/apps/shop-users/event-subscriptions", &subscriptions)
		envKeys := make([]string, 0, len(envList.Env))
		for _, row := range envList.Env {
			envKeys = append(envKeys, row.Key)
		}
		t.Logf("ADR-1342 evidence: after the repeat deploy shop-users env keys %v, %d event subscription(s)", envKeys, len(subscriptions.Subscriptions))
		if strings.Join(envKeys, ",") != "WHO" {
			t.Fatalf("shop-users env keys = %v; prune_env should leave only WHO", envKeys)
		}
		if len(subscriptions.Subscriptions) != 1 {
			t.Fatalf("repeat deploy left %d event subscriptions; want 1", len(subscriptions.Subscriptions))
		}
	})
}

// Qualification must exercise every interpreter and the CLI. Ordinary metal
// development runs may still select the fixtures available on their host.
func requireFunctionConcurrencyFixtures(t *testing.T) {
	t.Helper()
	if os.Getenv("FAAS_REQUIRE_FUNCTION_CONCURRENCY_METAL") != "1" {
		return
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("function qualification requires root on native x86_64 Linux")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatal("function qualification requires /dev/kvm; skips cannot qualify this suite")
	}
	if _, err := os.Stat("/etc/faas/builder-acceptance-host"); err != nil {
		t.Fatal("function qualification requires a designated isolated acceptance host")
	}
	if err := exec.Command("systemd-detect-virt", "--quiet").Run(); err == nil {
		t.Fatal("virtualized/container runs cannot qualify native function concurrency")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatal("could not establish native host class")
		}
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("qualification requires an enabled disposable PostgreSQL fixture")
	}
	for _, key := range []string{"FAAS_TEST_KERNEL", "FAAS_BUILDER_BASE_PATH", "FAAS_GUEST_INIT", "FAAS_E2E_FUNCTION_RUNNER_GO124", "FAAS_E2E_FUNCTION_RUNNER_NODE22", "FAAS_E2E_FUNCTION_RUNNER_PYTHON312", "FAAS_E2E_GREGALE_BIN"} {
		info, err := os.Stat(os.Getenv(key))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("qualification requires a readable %s artifact", key)
		}
	}
}

// exerciseSharedFunction checks a function deployed with
// function_concurrency=8 (ADR-1342): one worker answers eight concurrent
// 2 s requests, and after a client abandons abandonPath the same worker
// still serves, past the runner's 5 s cancel deadline. wantCancels, when
// set, is the cancel count the worker must report. The guest's writable
// root must also be the plan's ephemeral disk (#2529).
func exerciseSharedFunction(t *testing.T, h *e2etest.Harness, key, slug, abandonPath, wantCancels string) {
	t.Helper()
	host := slug + ".apps.test.example"
	first := sharedWorkerGet(t, h, key, host, "/fast", time.Minute)
	if first.slots != "8" {
		t.Fatalf("handler saw FAAS_FUNCTION_CONCURRENCY=%q; want imaged's 8", first.slots)
	}
	checkWritableRoot(t, slug, first)

	// One worker with eight slots answers eight 2 s requests together.
	// One request per worker, four workers, would take at least 4 s.
	started := time.Now()
	var wg sync.WaitGroup
	replies := make(chan sharedWorkerReply, 8)
	for range 8 {
		wg.Go(func() {
			start := time.Now()
			reply := sharedWorkerGet(t, h, key, host, "/slow", time.Minute)
			reply.elapsed = time.Since(start)
			replies <- reply
		})
	}
	wg.Wait()
	close(replies)
	elapsed := time.Since(started)
	pids := map[string]bool{}
	latencies := make([]time.Duration, 0, 8)
	peakRSS := 0
	for reply := range replies {
		pids[reply.pid] = true
		latencies = append(latencies, reply.elapsed)
		rss, _ := strconv.Atoi(reply.rssMB)
		peakRSS = max(peakRSS, rss)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("ADR-1342 evidence: %s: 8 concurrent 2 s requests answered in %v by %d worker process(es) %v (first request pid %s)",
		slug, elapsed.Round(10*time.Millisecond), len(pids), sortedKeys(pids), first.pid)
	t.Logf("concurrency sample: throughput=%.2f requests/s; eight-sample p95=%s; interpreter RSS high-water=%d MB (0 if unreported)", 8/elapsed.Seconds(), latencies[len(latencies)-1].Round(time.Millisecond), peakRSS)
	if len(pids) != 1 || !pids[first.pid] {
		t.Fatalf("requests ran in workers %v; want the one shared worker %s", sortedKeys(pids), first.pid)
	}
	if elapsed > 3500*time.Millisecond {
		t.Fatalf("8 concurrent 2 s requests took %v; the worker served them one at a time", elapsed)
	}

	// A client that gives up does not cost the worker.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(h.GatewayURL, "/")+abandonPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+key)
	if resp, err := (&http.Client{}).Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("GET %s answered %d; want the client to give up", abandonPath, resp.StatusCode)
	}
	cancel()
	// Past the runner's 5 s cancel deadline, so a missed acknowledgement
	// would have restarted the worker.
	time.Sleep(7 * time.Second)
	var after sharedWorkerReply
	for deadline := time.Now().Add(45 * time.Second); ; time.Sleep(time.Second) {
		after = sharedWorkerGet(t, h, key, host, "/fast", time.Minute)
		if wantCancels == "" || after.cancels == wantCancels || time.Now().After(deadline) {
			break
		}
	}
	t.Logf("ADR-1342 evidence: %s: after an abandoned %s, worker %s (first %s) reports %s cancel(s)", slug, abandonPath, after.pid, first.pid, after.cancels)
	if wantCancels != "" && after.cancels != wantCancels {
		t.Fatalf("worker reports %s cancels; want %s", after.cancels, wantCancels)
	}
	if after.pid != first.pid {
		t.Fatalf("worker %s replaced by %s; an abandoned request must not restart a responsive worker", first.pid, after.pid)
	}
}

// checkWritableRoot is the #2529 acceptance: a fresh function's writable
// root is the plan's ephemeral disk, not a sliver above its app layer, and
// starts below the 80% near-full threshold.
func checkWritableRoot(t *testing.T, slug string, reply sharedWorkerReply) {
	t.Helper()
	allowance := api.MustLimitsFor(api.PlanPro).EphemeralDiskMaxMB()
	total, totalErr := strconv.Atoi(reply.diskTotal)
	free, freeErr := strconv.Atoi(reply.diskFree)
	if totalErr != nil || freeErr != nil {
		t.Errorf("%s: handler reported disk_total_mb=%q disk_free_mb=%q", slug, reply.diskTotal, reply.diskFree)
		return
	}
	t.Logf("#2529 evidence: %s: fresh guest writable root %d MB, %d MB free (%d%% used); Pro allowance %d MB",
		slug, total, free, (total-free)*100/max(total, 1), allowance)
	if total < allowance*85/100 || total > allowance {
		t.Errorf("%s: writable root is %d MB; want the Pro ephemeral disk allowance (%d MB, less filesystem overhead)", slug, total, allowance)
	}
	if (total-free)*100 >= total*80 {
		t.Errorf("%s: a fresh guest starts %d%% full; want below the 80%% near-full threshold", slug, (total-free)*100/total)
	}
}

// deployRuntimeFunction creates a public function app with
// function_concurrency and deploys one handler file to it.
func deployRuntimeFunction(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key, slug, runtime, file, source string, concurrency int) buildResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
	defer cancel()
	public := false
	create := api.CreateAppRequest{Slug: slug, Type: "function", Runtime: runtime, RequireAuthn: &public, FunctionConcurrency: concurrency}
	if got := postOK(t, h, key, "/v1/apps", create); got != http.StatusCreated {
		t.Fatalf("create %s function %s: status=%d", runtime, slug, got)
	}
	body, status := postBinaryFunctionDeployment(t, h, key, slug, buildTarGz(t, map[string]string{file: source}), runtime, "handler.handler")
	if status != http.StatusAccepted {
		t.Fatalf("deploy %s: status=%d body=%s", slug, status, body)
	}
	depID, buildID := parseQueuedDeployment(t, body)
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("%s function %s (build %s, deployment %s) did not go live: %v", runtime, slug, buildID, depID, err)
	}
	return buildResult{deploymentID: depID, buildID: buildID, slug: slug}
}

// The interpreter handlers answer like the C handler: "key=value" fields
// naming the worker's pid, its slots, its cancel count and its writable
// root's size. /slow takes 2 s; /hang never answers; /abandon takes 3 s.
const nodeSharedHandler = `const fs = require("fs");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
let cancels = 0;
function disk() {
  const st = fs.statfsSync("/");
  return "disk_total_mb=" + Math.floor(st.blocks * st.bsize / 1048576) + " disk_free_mb=" + Math.floor(st.bavail * st.bsize / 1048576);
}
exports.handler = async (event, ctx) => {
  if (event.path === "/slow") await sleep(2000);
  if (event.path === "/hang") await new Promise((_, reject) => ctx.signal.addEventListener("abort", () => {
    cancels += 1;
    reject(ctx.signal.reason);
  }, {once:true}));
  return {
    statusCode: 200,
    headers: { "content-type": "text/plain" },
    body: "who=node pid=" + process.pid + " slots=" + (process.env.FAAS_FUNCTION_CONCURRENCY || "-") + " cancels=" + cancels + " " + disk() + " path=" + event.path + " rss_mb=" + Math.ceil(process.resourceUsage().maxRSS / 1024),
  };
};
`

const pythonAsyncSharedHandler = `import asyncio
import os
import resource

cancels = 0

def disk():
    st = os.statvfs("/")
    return "disk_total_mb=%d disk_free_mb=%d" % (st.f_blocks * st.f_frsize // 1048576, st.f_bavail * st.f_frsize // 1048576)

async def handler(event, ctx):
    global cancels
    path = event["path"]
    try:
        if path == "/slow":
            await asyncio.sleep(2)
        if path == "/hang":
            await asyncio.Event().wait()
    except asyncio.CancelledError:
        cancels += 1
        raise
    body = "who=python pid=%d slots=%s cancels=%d %s path=%s" % (os.getpid(), os.environ.get("FAAS_FUNCTION_CONCURRENCY", "-"), cancels, disk(), path)
    body += " rss_mb=%d" % (resource.getrusage(resource.RUSAGE_SELF).ru_maxrss // 1024)
    return {"statusCode": 200, "headers": {"content-type": "text/plain"}, "body": body}
`

const pythonSyncSharedHandler = `import os
import time
import resource

def disk():
    st = os.statvfs("/")
    return "disk_total_mb=%d disk_free_mb=%d" % (st.f_blocks * st.f_frsize // 1048576, st.f_bavail * st.f_frsize // 1048576)

def handler(event, ctx):
    path = event["path"]
    if path == "/slow":
        time.sleep(2)
    if path == "/abandon":
        time.sleep(3)
    body = "who=python-sync pid=%d slots=%s cancels=0 %s path=%s" % (os.getpid(), os.environ.get("FAAS_FUNCTION_CONCURRENCY", "-"), disk(), path)
    body += " rss_mb=%d" % (resource.getrusage(resource.RUSAGE_SELF).ru_maxrss // 1024)
    return {"statusCode": 200, "headers": {"content-type": "text/plain"}, "body": body}
`

// secretSealingEnv gives the daemons a host and fleet age key and an HMAC
// key, so apid can seal app secrets and wakes can deliver them.
func secretSealingEnv(t *testing.T) []string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keys := t.TempDir()
	for _, name := range []string{"host.age", "fleet.age"} {
		if err := secretbox.WriteHostKeyAtPath(filepath.Join(keys, name), identity); err != nil {
			t.Fatal(err)
		}
	}
	public := filepath.Join(keys, "host.age.pub")
	if err := secretbox.WriteRecipientFile(public, identity); err != nil {
		t.Fatal(err)
	}
	hmac := make([]byte, 32)
	if _, err := rand.Read(hmac); err != nil {
		t.Fatal(err)
	}
	hmacPath := filepath.Join(keys, "host.hmac")
	if err := os.WriteFile(hmacPath, hmac, 0o400); err != nil {
		t.Fatal(err)
	}
	return []string{
		"FAAS_HOST_HMAC_KEY_PATH=" + hmacPath,
		"FAAS_HOST_KEY_PATH=" + filepath.Join(keys, "host.age"),
		"FAAS_HOST_AGE_IDENTITY_PATH=" + filepath.Join(keys, "host.age"),
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + public,
		"FAAS_FLEET_AGE_RECIPIENT_PATH=" + public,
	}
}

type sharedWorkerReply struct {
	who, pid, slots, cancels, tok, path string
	diskTotal, diskFree                 string
	rssMB                               string
	elapsed                             time.Duration
}

// sharedWorkerGet sends GET path to the function behind host and parses the
// handler's "key=value" reply.
func sharedWorkerGet(t *testing.T, h *e2etest.Harness, key, host, path string, timeout time.Duration) sharedWorkerReply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(h.GatewayURL, "/")+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Errorf("GET %s%s: %v", host, path, err)
		return sharedWorkerReply{}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s%s: status %d body %q", host, path, resp.StatusCode, body)
		return sharedWorkerReply{}
	}
	var reply sharedWorkerReply
	for _, part := range strings.Fields(string(body)) {
		name, value, _ := strings.Cut(part, "=")
		switch name {
		case "who":
			reply.who = value
		case "pid":
			reply.pid = value
		case "slots":
			reply.slots = value
		case "cancels":
			reply.cancels = value
		case "tok":
			reply.tok = value
		case "disk_total_mb":
			reply.diskTotal = value
		case "disk_free_mb":
			reply.diskFree = value
		case "path":
			reply.path = value
		case "rss_mb":
			reply.rssMB = value
		}
	}
	return reply
}

func mustGetJSON(t *testing.T, h *e2etest.Harness, key, path string, out any) {
	t.Helper()
	body, status := doReq(t, h, key, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, status, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode %s: %v body=%s", path, err, body)
	}
}

func containsSecret(list api.AppSecretListResponse, name string) bool {
	for _, secret := range list.Secrets {
		if secret.Key == name {
			return true
		}
	}
	return false
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sharedWorkerCHandlerSource(t *testing.T) []byte {
	t.Helper()
	return buildTarGz(t, sharedWorkerCHandlerFiles())
}

// sharedWorkerCHandlerFiles is a static C handler speaking the shared worker
// protocol (ADR-1342): it advertises FAAS_FUNCTION_CONCURRENCY slots, answers
// each request on its own thread with the id the runner sent, and
// acknowledges cancels. /slow answers after 2 s and /hang never answers;
// every reply names the worker's pid, its WHO env value, its slots, how many
// cancels it has acknowledged, the length of its USERS_TOKEN secret and its
// writable root's size.
func sharedWorkerCHandlerFiles() map[string]string {
	const handlerC = `#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/statvfs.h>
#include <unistd.h>

static const char protocol[] = "FAAS_PERSISTENT_PROTOCOL_V1";
static pthread_mutex_t out = PTHREAD_MUTEX_INITIALIZER;
static int cancels;
static char b64[65];

static void alphabet(void) {
	for (int i = 0; i < 26; i++) { b64[i] = 'A' + i; b64[26 + i] = 'a' + i; }
	for (int i = 0; i < 10; i++) b64[52 + i] = '0' + i;
	b64[62] = '+'; b64[63] = '/';
}

static void encode(const char *in, char *dst) {
	size_t n = strlen(in), i = 0, o = 0;
	const unsigned char *s = (const unsigned char *)in;
	for (; i + 2 < n; i += 3) {
		unsigned v = s[i] << 16 | s[i + 1] << 8 | s[i + 2];
		dst[o++] = b64[v >> 18 & 63]; dst[o++] = b64[v >> 12 & 63];
		dst[o++] = b64[v >> 6 & 63]; dst[o++] = b64[v & 63];
	}
	if (n - i == 1) {
		unsigned v = s[i] << 16;
		dst[o++] = b64[v >> 18 & 63]; dst[o++] = b64[v >> 12 & 63]; dst[o++] = '='; dst[o++] = '=';
	} else if (n - i == 2) {
		unsigned v = s[i] << 16 | s[i + 1] << 8;
		dst[o++] = b64[v >> 18 & 63]; dst[o++] = b64[v >> 12 & 63]; dst[o++] = b64[v >> 6 & 63]; dst[o++] = '=';
	}
	dst[o] = 0;
}

/* field copies the string value of "key":"..." in line into dst. */
static void field(const char *line, const char *key, char *dst, size_t n) {
	char pattern[64];
	snprintf(pattern, sizeof pattern, "\"%s\":\"", key);
	const char *at = strstr(line, pattern);
	dst[0] = 0;
	if (!at) return;
	at += strlen(pattern);
	size_t i = 0;
	while (at[i] && at[i] != '"' && i + 1 < n) { dst[i] = at[i]; i++; }
	dst[i] = 0;
}

struct job { char id[32]; char path[128]; };

static void *serve(void *arg) {
	struct job *job = arg;
	if (strcmp(job->path, "/hang") == 0) { free(job); return NULL; }
	if (strcmp(job->path, "/slow") == 0) sleep(2);
	const char *who = getenv("WHO"), *slots = getenv("FAAS_FUNCTION_CONCURRENCY"), *token = getenv("USERS_TOKEN");
	struct statvfs st;
	long total = -1, free_mb = -1;
	if (statvfs("/", &st) == 0) {
		total = (long)((unsigned long long)st.f_blocks * st.f_frsize / 1048576);
		free_mb = (long)((unsigned long long)st.f_bavail * st.f_frsize / 1048576);
	}
	char text[400], encoded[560];
	pthread_mutex_lock(&out);
	snprintf(text, sizeof text, "who=%s pid=%d slots=%s cancels=%d tok=%d disk_total_mb=%ld disk_free_mb=%ld path=%s",
		who ? who : "-", (int)getpid(), slots ? slots : "-", cancels, token ? (int)strlen(token) : 0, total, free_mb, job->path);
	encode(text, encoded);
	if (job->id[0])
		printf("{\"__faas_id\":\"%s\",\"status\":200,\"headers\":{\"content-type\":\"text/plain\"},\"body_b64\":\"%s\"}\n", job->id, encoded);
	else
		printf("{\"status\":200,\"headers\":{\"content-type\":\"text/plain\"},\"body_b64\":\"%s\"}\n", encoded);
	pthread_mutex_unlock(&out);
	free(job);
	return NULL;
}

int main(void) {
	alphabet();
	setvbuf(stdout, NULL, _IOLBF, 0);
	const char *configured = getenv("FAAS_FUNCTION_CONCURRENCY");
	int slots = configured ? atoi(configured) : 1;
	if (slots < 1) slots = 1;
	const char *persistent = getenv("FAAS_PERSISTENT_WORKER");
	if (persistent && strcmp(persistent, "1") == 0)
		printf("{\"__faas_ready\":true,\"concurrency\":%d,\"protocol\":\"%s\"}\n", slots, protocol);
	char *line = NULL;
	size_t cap = 0;
	while (getline(&line, &cap, stdin) > 0) {
		if (line[0] == '\n') continue;
		char cancel[32];
		field(line, "__faas_cancel", cancel, sizeof cancel);
		if (cancel[0]) {
			pthread_mutex_lock(&out);
			cancels++;
			printf("{\"__faas_cancelled\":\"%s\"}\n", cancel);
			pthread_mutex_unlock(&out);
			continue;
		}
		struct job *job = calloc(1, sizeof *job);
		field(line, "__faas_id", job->id, sizeof job->id);
		field(line, "path", job->path, sizeof job->path);
		if (slots <= 1) { serve(job); continue; }
		pthread_t thread;
		pthread_create(&thread, NULL, serve, job);
		pthread_detach(thread);
	}
	return 0;
}
`
	const dockerfile = `FROM alpine:3.20 AS build
RUN apk add --no-cache gcc musl-dev
COPY handler.c /src/handler.c
RUN gcc -static -O2 -pthread -o /src/handler /src/handler.c
FROM scratch
COPY --from=build /src/handler /app/handler
ENTRYPOINT ["/app/handler"]
`
	return map[string]string{"Dockerfile": dockerfile, "handler.c": handlerC}
}
