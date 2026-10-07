//go:build linux

// adr: 413, 414, 642
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

func TestComposeImageHealthcheckStartupExecution(t *testing.T) {
	for _, tc := range []struct {
		name        string
		test        []string
		wantFailure bool
	}{
		{"successful exec", []string{"CMD", "/bin/sh", "-c", "exit 0"}, false},
		{"failed shell", []string{"CMD-SHELL", "exit 1"}, true},
		{"disabled image check", []string{"NONE"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := oci.ApplyComposeHealthcheck(oci.ImageConfig{Cmd: []string{"/server"},
				Healthcheck: &oci.ImageHealthcheck{Test: []string{"CMD-SHELL", "exit 1"}}},
				&api.ComposeHealthcheck{Test: tc.test, Retries: 1, TimeoutNS: int64(time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := oci.ManifestFromConfig(oci.Config{Cmd: config.Cmd, Healthcheck: config.Healthcheck})
			if err != nil {
				t.Fatal(err)
			}
			err = runStartupHealthcheck(manifest, nil, "", "", 0, nil, nil)
			if (err != nil) != tc.wantFailure {
				t.Fatalf("startup check = %v; want failure %t", err, tc.wantFailure)
			}
		})
	}
}

func TestHealthcheckPollUsesScopedEnvironmentAfterMainStarts(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "observed")
	probe := filepath.Join(dir, "image-probe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nprintf '%s|%s|%s|%s' \"$SCOPED_SECRET\" \"$DEPLOYMENT_MARKER\" \"$PORT\" \"$PWD\" > \"$MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := api.AppManifest{User: "0", WorkingDir: dir, Port: 9090, Env: map[string]string{"PATH": dir, "MARKER": marker}}
	var secret atomic.Value
	secret.Store("initial")
	started := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runHealthcheckPollLoop(ctx, -1, []string{"image-probe"}, manifest, 5*time.Millisecond, time.Second, 0, 1,
			slog.New(slog.NewTextHandler(io.Discard, nil)), healthcheckPollOptions{
				Started: started,
				Environment: func() []string {
					return BuildEnvWithSecrets(nil, manifest, map[string]string{"SCOPED_SECRET": secret.Load().(string)}, map[string]string{"DEPLOYMENT_MARKER": "deployment", "PORT": "1234"})
				},
			})
	}()
	t.Cleanup(func() { cancel(); <-done })
	// Init/dependency waiting must not execute an image probe early.
	time.Sleep(30 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("probe ran before main started: %v", err)
	}
	close(started)
	waitHealthcheckMarker(t, marker, "initial|deployment|9090|"+dir)
	secret.Store("rotated")
	waitHealthcheckMarker(t, marker, "rotated|deployment|9090|"+dir)
}

func TestHealthcheckPollCancellationWhileMainPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var invoked atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runHealthcheckPollLoop(ctx, -1, []string{"/bin/true"}, api.AppManifest{User: "0"}, time.Millisecond, time.Second, 0, 1,
			slog.New(slog.NewTextHandler(io.Discard, nil)), healthcheckPollOptions{
				Started:     make(chan struct{}),
				Environment: func() []string { invoked.Add(1); return nil },
			})
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller did not stop while main was pending")
	}
	if invoked.Load() != 0 {
		t.Fatal("probe environment evaluated before main started")
	}
}

func TestHealthcheckPollMissingCgroupDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runHealthcheckPollLoop(ctx, -1, []string{"/bin/sh", "-c", `printf executed > "$1"`, "sh", marker}, api.AppManifest{User: "0"}, time.Millisecond, time.Second, 0, 1,
		slog.New(slog.NewTextHandler(io.Discard, nil)), healthcheckPollOptions{CgroupLeaf: filepath.Join(dir, "missing")})
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("probe executed despite failed cgroup acquisition: %v", err)
	}
}

func waitHealthcheckMarker(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil && string(body) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	body, err := os.ReadFile(path)
	t.Fatalf("probe runtime contract: got %q (err=%v), want %q", body, err, want)
}

func TestHealthcheckExactImageTiming(t *testing.T) {
	check := &api.AppManifestHealthcheck{
		Test: []string{"CMD", "/check"}, IntervalS: 2, TimeoutS: 1, StartPeriodS: 1,
		ImageTiming: &api.OCIHealthcheckTiming{IntervalNS: int64(1500 * time.Millisecond), TimeoutNS: int64(250 * time.Millisecond), StartPeriodNS: int64(750 * time.Millisecond), StartIntervalNS: int64(50 * time.Millisecond)},
	}
	interval, timeout, grace, retries := healthcheckDefaults(check)
	if interval != 1500*time.Millisecond || timeout != 250*time.Millisecond || grace != 750*time.Millisecond || retries != 3 {
		t.Fatalf("exact timings replaced by compatibility seconds: %s %s %s %d", interval, timeout, grace, retries)
	}
	probe := sidecarProbeFromHealthcheck(check)
	period, probeTimeout, _, probeGrace, _, _ := sidecarProbeSettings(probe)
	if period != interval || probeTimeout != timeout || probeGrace != grace {
		t.Fatalf("companion probe lost image timing: %s %s %s", period, probeTimeout, probeGrace)
	}
	if delay := imageHealthcheckPollDelay(check, 0, interval, grace); delay != 50*time.Millisecond {
		t.Fatalf("image StartInterval ignored: %s", delay)
	}
	if delay := imageHealthcheckPollDelay(check, time.Second, interval, grace); delay != interval {
		t.Fatalf("startup cadence continued after grace: %s", delay)
	}
	check.ImageTiming.StartIntervalNS = 0
	if delay := imageHealthcheckPollDelay(check, 0, interval, grace); delay != api.OCIHealthcheckDefaultStartInterval {
		t.Fatalf("incorrect image startup cadence default: %s", delay)
	}
}

func TestHealthcheckImageStartupRetryBudget(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "attempts")
	check := &api.AppManifestHealthcheck{
		Test:    []string{"CMD", "/bin/sh", "-c", `n=0; if [ -r "$COUNTER" ]; then read -r n < "$COUNTER"; fi; n=$((n+1)); printf '%s\n' "$n" > "$COUNTER"; [ "$n" -ge 2 ]`},
		Retries: 3, ImageTiming: &api.OCIHealthcheckTiming{IntervalNS: int64(5 * time.Millisecond), TimeoutNS: int64(time.Second)},
	}
	if err := runStartupHealthcheck(api.AppManifest{Healthcheck: check}, []string{"COUNTER=" + marker}, "", "", 0, nil, nil); err != nil {
		t.Fatalf("transient image startup failure exhausted retries early: %v", err)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "2\n" {
		t.Fatalf("startup did not retry exactly once: %q err=%v", body, err)
	}
}

func TestHealthcheckImageStartupGrace(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "attempts")
	// Passing on attempt two must be possible even with retries=1 while
	// startup grace is active. This proves the first failure wasn't charged.
	check := &api.AppManifestHealthcheck{
		Test:    []string{"CMD", "/bin/sh", "-c", `n=0; if [ -r "$COUNTER" ]; then read -r n < "$COUNTER"; fi; n=$((n+1)); printf '%s\n' "$n" > "$COUNTER"; [ "$n" -ge 2 ]`},
		Retries: 1, ImageTiming: &api.OCIHealthcheckTiming{IntervalNS: int64(time.Second), TimeoutNS: int64(time.Second), StartPeriodNS: int64(time.Second), StartIntervalNS: int64(5 * time.Millisecond)},
	}
	if err := runStartupHealthcheck(api.AppManifest{Healthcheck: check}, []string{"COUNTER=" + marker}, "", "", 0, nil, nil); err != nil {
		t.Fatalf("startup grace consumed retry budget: %v", err)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "2\n" {
		t.Fatalf("startup grace did not permit the next probe: %q err=%v", body, err)
	}
}

func TestHealthcheckGraceUsesProbeStart(t *testing.T) {
	started := time.Now().Add(-time.Second)
	probeStarted := started.Add(10 * time.Millisecond)
	check := &api.AppManifestHealthcheck{ImageTiming: &api.OCIHealthcheckTiming{}}
	if !healthcheckWithinStartupGrace(check, started, probeStarted, 50*time.Millisecond) {
		t.Fatal("slow image probe lost its startup grace")
	}
	if healthcheckWithinStartupGrace(nil, started, probeStarted, 50*time.Millisecond) {
		t.Fatal("legacy result-time behavior changed")
	}
	if healthcheckWithinStartupGrace(check, started, started.Add(50*time.Millisecond), 50*time.Millisecond) {
		t.Fatal("probe at grace boundary was exempted")
	}
}

func TestHealthcheckSlowStartupProbeRetainsGrace(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "attempts")
	check := &api.AppManifestHealthcheck{
		Test:    []string{"CMD", "/bin/sh", "-c", `n=0; if [ -r "$COUNTER" ]; then read -r n < "$COUNTER"; fi; n=$((n+1)); printf '%s\n' "$n" > "$COUNTER"; if [ "$n" -eq 1 ]; then /bin/sleep 0.2; exit 1; fi`},
		Retries: 1, ImageTiming: &api.OCIHealthcheckTiming{IntervalNS: int64(5 * time.Millisecond), TimeoutNS: int64(time.Second), StartPeriodNS: int64(50 * time.Millisecond)},
	}
	if err := runStartupHealthcheck(api.AppManifest{Healthcheck: check}, []string{"COUNTER=" + marker}, "", "", 0, nil, nil); err != nil {
		t.Fatalf("probe started within grace consumed retry budget: %v", err)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "2\n" {
		t.Fatalf("slow probe not retried: %q, %v", body, err)
	}
}
