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
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

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
