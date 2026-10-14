//go:build metal

package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestFunctionConcurrencyMetal is the ADR-1190 acceptance on real guests. A
// binary function whose handler speaks the shared worker protocol, deployed
// with function_concurrency=8, serves eight slow requests at once from one
// worker process, and a request its client abandons is cancelled without
// restarting that worker. The gregale CLI then deploys a functions manifest:
// per-function settings, env and secrets reach each app before its build,
// and the entry function's host routes /users/* to another function.
func TestFunctionConcurrencyMetal(t *testing.T) {
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
	h := e2etest.Start(t, pool, e2etest.All, append(secretSealingEnv(t), "FAAS_FUNCTION_RUNNER_GO124="+runner)...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanPro)

	t.Run("shared worker", func(t *testing.T) {
		result := deployBinaryFunctionWith(t, h, pool, key, "concfn", sharedWorkerCHandlerSource(t),
			func(req *api.CreateAppRequest) { req.FunctionConcurrency = 8 })
		host := result.slug + ".apps.test.example"
		first := sharedWorkerGet(t, h, key, host, "/fast", time.Minute)
		if first.slots != "8" {
			t.Fatalf("handler saw FAAS_FUNCTION_CONCURRENCY=%q; want imaged's 8", first.slots)
		}

		// One worker with eight slots answers eight 2 s requests together.
		// One request per worker, four workers, would take at least 4 s.
		started := time.Now()
		var wg sync.WaitGroup
		replies := make(chan sharedWorkerReply, 8)
		for range 8 {
			wg.Go(func() { replies <- sharedWorkerGet(t, h, key, host, "/slow", time.Minute) })
		}
		wg.Wait()
		close(replies)
		elapsed := time.Since(started)
		pids := map[string]bool{}
		for reply := range replies {
			pids[reply.pid] = true
		}
		t.Logf("ADR-1190 evidence: 8 concurrent 2 s requests answered in %v by %d worker process(es) %v (first request pid %s)",
			elapsed.Round(10*time.Millisecond), len(pids), sortedKeys(pids), first.pid)
		if len(pids) != 1 || !pids[first.pid] {
			t.Fatalf("requests ran in workers %v; want the one shared worker %s", sortedKeys(pids), first.pid)
		}
		if elapsed > 3500*time.Millisecond {
			t.Fatalf("8 concurrent 2 s requests took %v; the worker served them one at a time", elapsed)
		}

		// A client that gives up does not cost the worker: the runner cancels
		// the request and the worker acknowledges it.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayAppURL(h, result.slug)+"hang", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+key)
		if resp, err := (&http.Client{}).Do(req); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("GET /hang answered %d; want the client to give up", resp.StatusCode)
		}
		cancel()
		var after sharedWorkerReply
		for deadline := time.Now().Add(45 * time.Second); ; time.Sleep(time.Second) {
			after = sharedWorkerGet(t, h, key, host, "/fast", time.Minute)
			if after.cancels != "0" || time.Now().After(deadline) {
				break
			}
		}
		t.Logf("ADR-1190 evidence: after an abandoned request, worker %s reports %s acknowledged cancel(s)", after.pid, after.cancels)
		if after.cancels == "0" {
			t.Fatal("the abandoned request was never cancelled")
		}
		if after.pid != first.pid {
			t.Fatalf("worker %s replaced by %s; a cancel must not restart a responsive worker", first.pid, after.pid)
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
    secrets_from_env: [USERS_TOKEN]
    routes: ["/users/*"]
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
		t.Logf("ADR-1190 evidence: %s/users/42 -> who=%s slots=%s secret bytes=%s; %s/about -> who=%s slots=%s",
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

		// A second deploy with nothing changed converges without new rules.
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
	})
}

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

type sharedWorkerReply struct{ who, pid, slots, cancels, tok, path string }

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
		case "path":
			reply.path = value
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
// protocol (ADR-1190): it advertises FAAS_FUNCTION_CONCURRENCY slots, answers
// each request on its own thread with the id the runner sent, and
// acknowledges cancels. /slow answers after 2 s and /hang never answers;
// every reply names the worker's pid, its WHO env value, its slots, how many
// cancels it has acknowledged and the length of its USERS_TOKEN secret.
func sharedWorkerCHandlerFiles() map[string]string {
	const handlerC = `#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
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
	char text[320], encoded[480];
	pthread_mutex_lock(&out);
	snprintf(text, sizeof text, "who=%s pid=%d slots=%s cancels=%d tok=%d path=%s",
		who ? who : "-", (int)getpid(), slots ? slots : "-", cancels, token ? (int)strlen(token) : 0, job->path);
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
