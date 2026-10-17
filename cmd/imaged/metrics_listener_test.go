package main

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// H10-105: a hotfix restart of imaged on fsn-2 paged FaasDaemonDown for ten
// minutes because /metrics only bound after boot staging (scan renewals and a
// rebuilt base). The listener now serves while staging; /readyz still waits.
func TestImagedMetricsListenerServesWhileBootStaging(t *testing.T) {
	probe := &wire.ReadyzProbe{}
	staging := probe.Register()
	staging.Set(false, "staging boot-critical bases")
	addr, stop, err := startImagedMetricsListener(t.Context(), "127.0.0.1:0", &Config{}, wire.NewOpsMetrics("imaged"), probe, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, _ := get("/metrics"); code != http.StatusOK {
		t.Fatalf("/metrics while staging = %d, want 200 so up stays 1", code)
	}
	if code, body := get("/readyz"); code == http.StatusOK || !strings.Contains(body, "staging boot-critical bases") {
		t.Fatalf("/readyz while staging = %d %q, want not ready naming the staging", code, body)
	}
	staging.Set(true, "")
	if code, _ := get("/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz after staging = %d, want 200", code)
	}
}

// run must bind the listener before any boot staging and never in the
// pre-stage one-shot, which shares the serving imaged's config and port.
func TestRunStartsMetricsListenerBeforeBootStaging(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	listener := strings.Index(src, "startImagedMetricsListener(ctx, metricsAddr")
	staging := strings.Index(src, "h.EnsureBaseExt4(ctx, baseRef")
	if listener < 0 || staging < 0 || listener > staging {
		t.Fatalf("metrics listener (at %d) must start before boot staging (at %d)", listener, staging)
	}
	if !strings.Contains(src, `if metricsAddr != "" && !prestageOnlyFromEnv(getenv) {`) {
		t.Fatal("the pre-stage one-shot must not bind the metrics listener")
	}
	if ready, staged := strings.Index(src, `log.Info("imaged ready"`), strings.Index(src, "bootStaging.Set(true"); staged < 0 || staged > ready {
		t.Fatal("boot staging must be marked done before imaged reports ready")
	}
}
