//go:build metal

// function_streaming_metal_test.go — ADR-999 acceptance: a function's
// streamed response reaches the client through the real runner, vmmd bridge
// and gateway as it is produced, with every Set-Cookie header intact. The
// handler is a static C `binary` function speaking the frame protocol
// directly: a head, one chunk, a two-second pause, a second chunk, the end.
// A buffered path would deliver both chunks together after the pause.
//
// Requires the TestBinaryFunctionMetal environment, including the real
// linux/amd64 go124 runner at FAAS_E2E_FUNCTION_RUNNER_GO124.

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

func TestFunctionStreamingMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal function streaming test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping metal function streaming test")
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
	// The operator streaming switch is on in production (ADR-102); Pro
	// apps default to streaming_enabled.
	h := e2etest.Start(t, pool, e2etest.All, "FAAS_FUNCTION_RUNNER_GO124="+runner, "FAAS_GATEWAY_STREAMING=true")
	key := h.SeedAccount(context.Background(), api.PlanPro)
	result := deployBinaryFunction(t, h, pool, key, "ssefn", streamingCHandlerSource(t))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayAppURL(h, result.slug)+"events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = result.slug + ".apps.test.example"
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	headersAt := time.Now()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("GET /events: status %d content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if cookies := resp.Header.Values("Set-Cookie"); !reflect.DeepEqual(cookies, []string{"a=1", "b=2"}) {
		t.Fatalf("Set-Cookie = %v, want both cookies", cookies)
	}
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	firstAt := time.Since(headersAt)
	if err != nil || first != "data: one\n" {
		t.Fatalf("first line = %q, %v", first, err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("event separator: %v", err)
	}
	second, err := reader.ReadString('\n')
	secondAt := time.Since(headersAt)
	if err != nil || second != "data: two\n" {
		t.Fatalf("second line = %q, %v", second, err)
	}
	t.Logf("first chunk %v after headers, second %v", firstAt, secondAt)
	if firstAt > time.Second {
		t.Fatalf("first chunk arrived %v after the headers; the response was buffered", firstAt)
	}
	if gap := secondAt - firstAt; gap < 1500*time.Millisecond {
		t.Fatalf("chunks arrived %v apart; the handler paused 2 s between them, so the stream was buffered", gap)
	}
}

// TestFunctionLongRequestMetal is the ADR-1150 and ADR-1180 acceptance: a Pro
// function with request_timeout_s=120 streams for about 36 s, past the
// runner's old fixed 30 s, and its handler sees the gateway's deadline header.
// An async invocation of the same function then runs for 40 s and completes
// on its first attempt.
func TestFunctionLongRequestMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal long-request test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping metal long-request test")
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
	h := e2etest.Start(t, pool, e2etest.All, "FAAS_FUNCTION_RUNNER_GO124="+runner, "FAAS_GATEWAY_STREAMING=true")
	key := h.SeedAccount(context.Background(), api.PlanPro)
	result := deployBinaryFunctionWith(t, h, pool, key, "longfn", longStreamCHandlerSource(t),
		func(req *api.CreateAppRequest) { req.RequestTimeoutS = 120 })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayAppURL(h, result.slug)+"long", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = result.slug + ".apps.test.example"
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-Faas-Deadline-Ms", "86400000") // a client cannot set the guest's deadline
	started := time.Now()
	// The harness client gives up after 30 s; this request is bounded by its
	// three-minute context instead.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("GET /long: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /long: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	elapsed := time.Since(started)
	t.Logf("long stream: %v, %d bytes: %q", elapsed, len(body), body)
	if err != nil {
		t.Fatalf("read /long after %v: %v (cut short)", elapsed, err)
	}
	if !strings.Contains(string(body), "tick 3") || elapsed < 33*time.Second {
		t.Fatalf("stream ended after %v without all four ticks; the invocation was cut short", elapsed)
	}
	match := regexp.MustCompile(`deadline=(-?\d+)`).FindStringSubmatch(string(body))
	if match == nil {
		t.Fatalf("handler did not report a deadline: %q", body)
	}
	deadline, _ := strconv.Atoi(match[1])
	if deadline <= 60_000 || deadline > 120_000 {
		t.Fatalf("handler saw X-Faas-Deadline-Ms=%d; want the gateway's budget (60–120 s), not the client's", deadline)
	}

	// A response that starts only after 40 s, not streamed, also gets the
	// whole request timeout. vmmd's stream bridge used to wait 30 s for the
	// guest's response headers and answer 502.
	workReq, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayAppURL(h, result.slug)+"work", nil)
	if err != nil {
		t.Fatal(err)
	}
	workReq.Host = result.slug + ".apps.test.example"
	workReq.Header.Set("Authorization", "Bearer "+key)
	workStarted := time.Now()
	workResp, err := (&http.Client{}).Do(workReq)
	if err != nil {
		t.Fatalf("GET /work: %v", err)
	}
	workBody, err := io.ReadAll(workResp.Body)
	_ = workResp.Body.Close()
	t.Logf("long single response: %v, status %d: %q", time.Since(workStarted), workResp.StatusCode, workBody)
	if err != nil || workResp.StatusCode != http.StatusOK || !strings.Contains(string(workBody), "worked 40s") {
		t.Fatalf("GET /work after %v: status %d body %q err %v; want the 40 s answer", time.Since(workStarted), workResp.StatusCode, workBody, err)
	}

	// ADR-1180: an async invocation of the same function may run for its
	// request timeout too. /work runs for 40 s; before ADR-1180 the runner
	// cut every async invocation at 30 s.
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+result.slug+"/invoke/async", api.InvokeRequest{Path: "/work"})
	if status != http.StatusAccepted {
		t.Fatalf("POST /invoke/async: status=%d body=%s", status, body)
	}
	var queued api.AsyncInvokeResponse
	if err := json.Unmarshal(body, &queued); err != nil || queued.ID == "" {
		t.Fatalf("decode async response: %v body=%s", err, body)
	}
	asyncStarted := time.Now()
	inv := pollUntilCompleted(t, h, key, queued.ID, 4*time.Minute)
	t.Logf("long async invocation: %s after %v, attempts=%d, result=%s, last_error=%q",
		inv.State, time.Since(asyncStarted), inv.Attempts, inv.Result, inv.LastError)
	if inv.State != "completed" || inv.Attempts != 1 {
		t.Fatalf("async invocation state=%s attempts=%d last_error=%q; want completed on its first attempt", inv.State, inv.Attempts, inv.LastError)
	}
	match = regexp.MustCompile(`worked 40s deadline=(-?\d+)`).FindStringSubmatch(string(inv.Result))
	if match == nil {
		t.Fatalf("async result %s does not show the 40 s run", inv.Result)
	}
	if asyncDeadline, _ := strconv.Atoi(match[1]); asyncDeadline <= 60_000 || asyncDeadline > 120_000 {
		t.Fatalf("async handler saw X-Faas-Deadline-Ms=%d; want the app's 120 s timeout", asyncDeadline)
	}
}

// deployBinaryFunctionWith is deployBinaryFunction with a hook on the app
// creation request.
func deployBinaryFunctionWith(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key, slug string, source []byte, configure func(*api.CreateAppRequest)) buildResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
	defer cancel()
	public := false
	create := api.CreateAppRequest{Slug: slug, Type: "function", Runtime: api.FunctionRuntimeBinary, RequireAuthn: &public}
	configure(&create)
	if got := postOK(t, h, key, "/v1/apps", create); got != http.StatusCreated {
		t.Fatalf("create binary function %s: status=%d", slug, got)
	}
	body, status := postBinaryFunctionDeployment(t, h, key, slug, source, api.FunctionRuntimeBinary, "handler")
	if status != http.StatusAccepted {
		t.Fatalf("deploy %s: status=%d body=%s", slug, status, body)
	}
	depID, buildID := parseQueuedDeployment(t, body)
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("binary function %s (build %s, deployment %s) did not go live: %v", slug, buildID, depID, err)
	}
	return buildResult{deploymentID: depID, buildID: buildID, slug: slug}
}

// longStreamCHandlerSource streams four ticks 12 s apart on /long, each
// carrying the X-Faas-Deadline-Ms value the handler received. /work runs for
// 40 s and answers once with the deadline it received (ADR-1180). Other paths
// answer at once so the deploy smoke check stays quick.
func longStreamCHandlerSource(t *testing.T) []byte {
	t.Helper()
	const handlerC = `#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static const char protocol[] = "FAAS_PERSISTENT_PROTOCOL_V1";
static char b64[65];

static void alphabet(void) {
	for (int i = 0; i < 26; i++) { b64[i] = 'A' + i; b64[26 + i] = 'a' + i; }
	for (int i = 0; i < 10; i++) b64[52 + i] = '0' + i;
	b64[62] = '+'; b64[63] = '/';
}

static void encode(const char *in, char *out) {
	size_t n = strlen(in), i = 0, o = 0;
	const unsigned char *s = (const unsigned char *)in;
	for (; i + 2 < n; i += 3) {
		unsigned v = s[i] << 16 | s[i + 1] << 8 | s[i + 2];
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63];
		out[o++] = b64[v >> 6 & 63]; out[o++] = b64[v & 63];
	}
	if (n - i == 1) {
		unsigned v = s[i] << 16;
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63]; out[o++] = '='; out[o++] = '=';
	} else if (n - i == 2) {
		unsigned v = s[i] << 16 | s[i + 1] << 8;
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63]; out[o++] = b64[v >> 6 & 63]; out[o++] = '=';
	}
	out[o] = 0;
}

int main(void) {
	alphabet();
	setvbuf(stdout, NULL, _IOLBF, 0);
	const char *persistent = getenv("FAAS_PERSISTENT_WORKER");
	if (persistent && strcmp(persistent, "1") == 0)
		printf("{\"__faas_ready\":true,\"protocol\":\"%s\"}\n", protocol);
	char *line = NULL;
	size_t cap = 0;
	while (getline(&line, &cap, stdin) > 0) {
		if (line[0] == '\n') continue;
		if (strstr(line, "\"path\":\"/work\"")) {
			const char *key = "\"X-Faas-Deadline-Ms\":\"";
			const char *at = strstr(line, key);
			long deadline = at ? atol(at + strlen(key)) : -1;
			sleep(40);
			char text[96], encoded[160];
			snprintf(text, sizeof text, "worked 40s deadline=%ld", deadline);
			encode(text, encoded);
			printf("{\"status\":200,\"headers\":{\"content-type\":\"text/plain\"},\"body_b64\":\"%s\"}\n", encoded);
			continue;
		}
		if (!strstr(line, "\"path\":\"/long\"")) {
			printf("{\"status\":200,\"headers\":{\"content-type\":\"text/plain\"},\"body_b64\":\"b2sK\"}\n");
			continue;
		}
		const char *key = "\"X-Faas-Deadline-Ms\":\"";
		const char *at = strstr(line, key);
		long deadline = at ? atol(at + strlen(key)) : -1;
		printf("{\"frame\":\"head\",\"status\":200,\"headers\":{\"content-type\":\"text/plain\"}}\n");
		for (int i = 0; i < 4; i++) {
			char text[96], encoded[160];
			snprintf(text, sizeof text, "tick %d deadline=%ld\n", i, deadline);
			encode(text, encoded);
			printf("{\"frame\":\"chunk\",\"body_b64\":\"%s\"}\n", encoded);
			if (i < 3) sleep(12);
		}
		printf("{\"frame\":\"end\"}\n");
	}
	return 0;
}
`
	const dockerfile = `FROM alpine:3.20 AS build
RUN apk add --no-cache gcc musl-dev
COPY handler.c /src/handler.c
RUN gcc -static -O2 -o /src/handler /src/handler.c
FROM scratch
COPY --from=build /src/handler /app/handler
ENTRYPOINT ["/app/handler"]
`
	return buildTarGz(t, map[string]string{"Dockerfile": dockerfile, "handler.c": handlerC})
}

// streamingCHandlerSource is a persistent-protocol C handler that streams
// two server-sent events two seconds apart (ADR-999 frames).
func streamingCHandlerSource(t *testing.T) []byte {
	t.Helper()
	const dockerfile = `FROM alpine:3.20 AS build
RUN apk add --no-cache gcc musl-dev
COPY handler.c /src/handler.c
RUN gcc -static -O2 -o /src/handler /src/handler.c
FROM scratch
COPY --from=build /src/handler /app/handler
ENTRYPOINT ["/app/handler"]
`
	const handlerC = `#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static const char protocol[] = "FAAS_PERSISTENT_PROTOCOL_V1";
static char b64[65];

static void alphabet(void) {
	for (int i = 0; i < 26; i++) { b64[i] = 'A' + i; b64[26 + i] = 'a' + i; }
	for (int i = 0; i < 10; i++) b64[52 + i] = '0' + i;
	b64[62] = '+'; b64[63] = '/';
}

static void encode(const char *in, char *out) {
	size_t n = strlen(in), i = 0, o = 0;
	const unsigned char *s = (const unsigned char *)in;
	for (; i + 2 < n; i += 3) {
		unsigned v = s[i] << 16 | s[i + 1] << 8 | s[i + 2];
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63];
		out[o++] = b64[v >> 6 & 63]; out[o++] = b64[v & 63];
	}
	if (n - i == 1) {
		unsigned v = s[i] << 16;
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63]; out[o++] = '='; out[o++] = '=';
	} else if (n - i == 2) {
		unsigned v = s[i] << 16 | s[i + 1] << 8;
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63]; out[o++] = b64[v >> 6 & 63]; out[o++] = '=';
	}
	out[o] = 0;
}

int main(void) {
	char one[64], two[64];
	alphabet();
	encode("data: one\n\n", one);
	encode("data: two\n\n", two);
	setvbuf(stdout, NULL, _IOLBF, 0);
	const char *persistent = getenv("FAAS_PERSISTENT_WORKER");
	if (persistent && strcmp(persistent, "1") == 0)
		printf("{\"__faas_ready\":true,\"protocol\":\"%s\"}\n", protocol);
	char *line = NULL;
	size_t cap = 0;
	while (getline(&line, &cap, stdin) > 0) {
		if (line[0] == '\n') continue;
		printf("{\"frame\":\"head\",\"status\":200,\"headers\":{\"content-type\":\"text/event-stream\",\"set-cookie\":[\"a=1\",\"b=2\"]}}\n");
		printf("{\"frame\":\"chunk\",\"body_b64\":\"%s\"}\n", one);
		sleep(2);
		printf("{\"frame\":\"chunk\",\"body_b64\":\"%s\"}\n", two);
		printf("{\"frame\":\"end\"}\n");
	}
	return 0;
}
`
	return buildTarGz(t, map[string]string{"Dockerfile": dockerfile, "handler.c": handlerC})
}
