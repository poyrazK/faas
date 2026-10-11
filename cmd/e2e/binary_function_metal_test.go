//go:build metal

// binary_function_metal_test.go — ADR-987 acceptance: a `binary` function
// goes from source to served requests through the real pipeline. apid takes
// the multipart upload, builderd builds the customer's Dockerfile with
// buildctl in a builder microVM, imaged keeps the one executable the image
// names and layers the go124 runner over the go124 base, and the gateway
// wakes the function from its snapshot.
//
// Two functions are deployed:
//
//   - the CLI's function-rust template, unchanged, proves the customer path
//     (Rust toolchain, crates, static musl binary, persistent protocol);
//   - a static C handler that reports FAAS_RUNTIME and a token drawn from
//     getrandom when its process starts. Two restore wakes from the same
//     snapshot must return different tokens: a handler process resumed from
//     the snapshot would answer both with the token it held at capture
//     (ADR-687, GHSA-24j2-p895-mwc9).
//
// Requires, beyond the TestBuildMetal environment, the real linux/amd64
// go124 runner at FAAS_E2E_FUNCTION_RUNNER_GO124 (the harness stages
// placeholder runners otherwise) and a host that can pull the go124 runtime
// base.

package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBinaryFunctionMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal binary function test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping metal binary function test")
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
	h := e2etest.Start(t, pool, e2etest.All, "FAAS_FUNCTION_RUNNER_GO124="+runner)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	t.Run("rust-template", func(t *testing.T) {
		result := deployBinaryFunction(t, h, pool, key, "rustfn", rustTemplateSource(t))
		assertBinaryBuiltFromDockerfile(t, pool, result.deploymentID)
		body, _, status := getBinaryFunction(t, h, key, result.slug, "hello?x=1")
		if status != http.StatusOK {
			t.Fatalf("GET /hello: status=%d body=%s", status, body)
		}
		for _, want := range []string{`"ok":true`, `"method":"GET"`, `"path":"/hello"`, `"query":"x=1"`} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("GET /hello body=%s, want %s", body, want)
			}
		}
		t.Logf("rust template served: %s", body)
	})

	t.Run("restore-replaces-handler", func(t *testing.T) {
		result := deployBinaryFunction(t, h, pool, key, "cfn", staticCHandlerSource(t))
		appID := mustGetAppID(t, h, key, result.slug)
		var tokens []string
		for wake := 1; wake <= 2; wake++ {
			// The hosting smoke and the previous wake leave an instance
			// running; park so the next request restores from the snapshot.
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+result.slug+"/park", nil); status != http.StatusNoContent {
				cancel()
				t.Fatalf("park before wake %d: status=%d", wake, status)
			}
			if _, err := waitForSourceAppParked(ctx, h, appID, 30*time.Second); err != nil {
				cancel()
				t.Fatalf("park before wake %d: %v", wake, err)
			}
			body, wakeID, status := getBinaryFunction(t, h, key, result.slug, "")
			if status != http.StatusOK {
				cancel()
				t.Fatalf("wake %d: status=%d body=%s", wake, status, body)
			}
			if wakeID == "" {
				cancel()
				t.Fatalf("wake %d: response carried no x-faas-wake-id; the request did not wake the app", wake)
			}
			if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, "restore", 10*time.Second); err != nil {
				cancel()
				t.Fatalf("wake %d (%s) was not a snapshot restore: %v", wake, wakeID, err)
			}
			cancel()
			fields := strings.Fields(string(body))
			if len(fields) != 2 || fields[0] != "runtime=binary" || !strings.HasPrefix(fields[1], "token=") {
				t.Fatalf("wake %d body=%q, want \"runtime=binary token=<hex>\"", wake, body)
			}
			tokens = append(tokens, fields[1])
			t.Logf("wake %d (%s, restore): %s", wake, wakeID, strings.TrimSpace(string(body)))
		}
		if tokens[0] == tokens[1] {
			t.Fatalf("two restores from one snapshot served the same handler process (%s); "+
				"the runner did not replace it on restore", tokens[0])
		}
	})
}

// deployBinaryFunction creates a `binary` function app, uploads source, and
// waits for the build to succeed and the deployment to go live.
func deployBinaryFunction(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key, slug string, source []byte) buildResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
	defer cancel()
	public := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "function", Runtime: api.FunctionRuntimeBinary, RequireAuthn: &public,
	}); got != http.StatusCreated {
		t.Fatalf("create binary function %s: status=%d", slug, got)
	}
	body, status := postFunctionDeployment(t, h, key, slug, source, api.FunctionRuntimeBinary, "handler")
	if status != http.StatusAccepted {
		t.Fatalf("deploy %s: status=%d body=%s", slug, status, body)
	}
	depID, buildID := parseQueuedDeployment(t, body)
	dep, build, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID,
		e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling)
	if err != nil {
		t.Fatalf("binary function %s (build %s, deployment %s) did not go live: %v", slug, buildID, depID, err)
	}
	if build.Status != state.BuildSucceeded || dep.Status != state.DeployLive {
		t.Fatalf("binary function %s: build=%s deployment=%s", slug, build.Status, dep.Status)
	}
	return buildResult{deploymentID: depID, buildID: buildID, slug: slug}
}

// getBinaryFunction calls a function through the gateway with the account's
// API key (Pro apps default to bearer public auth) and returns the body, the
// x-faas-wake-id header and the status.
func getBinaryFunction(t *testing.T, h *e2etest.Harness, key, slug, path string) ([]byte, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayAppURL(h, slug)+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = slug + ".apps.test.example"
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET %s/%s: %v", slug, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.Header.Get("x-faas-wake-id"), resp.StatusCode
}

// postFunctionDeployment uploads a function source with the runtime and
// handler fields the CLI sends for a function deploy.
func postFunctionDeployment(t *testing.T, h *e2etest.Harness, key, slug string, source []byte, runtime, handler string) ([]byte, int) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("source", "src.tar.gz")
	if err != nil {
		t.Fatalf("multipart CreateFormFile: %v", err)
	}
	if _, err := part.Write(source); err != nil {
		t.Fatalf("multipart Write source: %v", err)
	}
	for name, value := range map[string]string{"runtime": runtime, "handler": handler} {
		if err := mw.WriteField(name, value); err != nil {
			t.Fatalf("multipart WriteField %s: %v", name, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart Close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1/apps/%s/deployments", h.APIDURL, slug), &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("function deploy POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode
}

// assertBinaryBuiltFromDockerfile checks builderd's durable build log: a
// binary function must build through the Dockerfile framework (ADR-987).
func assertBinaryBuiltFromDockerfile(t *testing.T, pool *pgxpool.Pool, deploymentID string) {
	t.Helper()
	var log string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(string_agg(line, '' ORDER BY seq), '') FROM deployment_logs WHERE deployment_id = $1 AND stream = 'build'`,
		deploymentID).Scan(&log); err != nil {
		t.Fatalf("read build log for %s: %v", deploymentID, err)
	}
	if !strings.Contains(log, "using function runtime framework: docker") {
		t.Fatalf("build log for %s does not show the Dockerfile framework:\n%s", deploymentID, log)
	}
}

// rustTemplateSource packs the CLI's function-rust template as shipped.
func rustTemplateSource(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{}
	err := fs.WalkDir(templates.FS, "function-rust", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(templates.FS, path)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(path, "function-rust/")] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("read function-rust template: %v", err)
	}
	if _, ok := files["Dockerfile"]; !ok {
		t.Fatalf("function-rust template has no Dockerfile: %v", files)
	}
	return buildTarGz(t, files)
}

// staticCHandlerSource is a persistent-protocol handler in C, built static
// with musl. Its token is fixed when the process starts.
func staticCHandlerSource(t *testing.T) []byte {
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
#include <sys/random.h>

static const char protocol[] = "FAAS_PERSISTENT_PROTOCOL_V1";
static char b64[65];

static void alphabet(void) {
	for (int i = 0; i < 26; i++) { b64[i] = 'A' + i; b64[26 + i] = 'a' + i; }
	for (int i = 0; i < 10; i++) b64[52 + i] = '0' + i;
	b64[62] = '+'; b64[63] = '/';
}

static void encode(const unsigned char *in, size_t n, char *out) {
	size_t i = 0, o = 0;
	for (; i + 2 < n; i += 3) {
		unsigned v = in[i] << 16 | in[i + 1] << 8 | in[i + 2];
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63];
		out[o++] = b64[v >> 6 & 63]; out[o++] = b64[v & 63];
	}
	if (n - i == 1) {
		unsigned v = in[i] << 16;
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63]; out[o++] = '='; out[o++] = '=';
	} else if (n - i == 2) {
		unsigned v = in[i] << 16 | in[i + 1] << 8;
		out[o++] = b64[v >> 18 & 63]; out[o++] = b64[v >> 12 & 63]; out[o++] = b64[v >> 6 & 63]; out[o++] = '=';
	}
	out[o] = 0;
}

int main(void) {
	unsigned char raw[16];
	char token[33], body[128], encoded[192];
	alphabet();
	if (getrandom(raw, sizeof raw, 0) != (ssize_t)sizeof raw) return 1;
	for (int i = 0; i < 16; i++) sprintf(token + 2 * i, "%02x", raw[i]);
	const char *runtime = getenv("FAAS_RUNTIME");
	int n = snprintf(body, sizeof body, "runtime=%s token=%s\n", runtime ? runtime : "", token);
	encode((const unsigned char *)body, (size_t)n, encoded);
	setvbuf(stdout, NULL, _IOLBF, 0);
	const char *persistent = getenv("FAAS_PERSISTENT_WORKER");
	if (persistent && strcmp(persistent, "1") == 0)
		printf("{\"__faas_ready\":true,\"protocol\":\"%s\"}\n", protocol);
	char *line = NULL;
	size_t cap = 0;
	while (getline(&line, &cap, stdin) > 0) {
		if (line[0] == '\n') continue;
		printf("{\"status\":200,\"headers\":{\"content-type\":\"text/plain\"},\"body_b64\":\"%s\"}\n", encoded);
	}
	return 0;
}
`
	return buildTarGz(t, map[string]string{"Dockerfile": dockerfile, "handler.c": handlerC})
}
