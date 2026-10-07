//go:build linux

// adr:683
package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/healthcheckproto"
	"golang.org/x/sys/unix"
)

func readinessTestRuntime(test []string, retries int) *imageReadinessRuntime {
	return &imageReadinessRuntime{ctx: context.Background(), startedAt: time.Now(), busy: make(chan struct{}, 1),
		env: os.Environ(), procAttr: &syscall.SysProcAttr{}, manifest: api.AppManifest{Healthcheck: &api.AppManifestHealthcheck{
			Test: test, Retries: retries, ImageTiming: &api.OCIHealthcheckTiming{
				IntervalNS: int64(5 * time.Millisecond), TimeoutNS: int64(20 * time.Millisecond)}}}}
}

func TestImageReadinessExecutesFreshCheckInRuntime(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "healthy")
	r := readinessTestRuntime([]string{"CMD-SHELL", `test "$CHECK_SECRET" = accepted && test -f healthy`}, 1)
	r.dir = dir
	r.env = append(r.env, "CHECK_SECRET=accepted")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if result := r.check(t.Context(), slog.Default()); result != "" {
		t.Fatal(result)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	// Simulates the same snapshotted runtime becoming unhealthy after restore.
	if result := r.check(t.Context(), slog.Default()); result != "unhealthy" {
		t.Fatalf("cached pass reused: %q", result)
	}
}

func TestImageReadinessUsesRotatedScopedSecrets(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "expected")
	runtime := readinessTestRuntime([]string{"CMD-SHELL", `test "$CHECK_SECRET" = "$(cat expected)" && test "$PLATFORM_MARKER" = kept && test "$PORT" = 9090`}, 1)
	runtime.dir = dir
	runtime.manifest.Port = 9090
	initial := map[string]string{"CHECK_SECRET": "initial"}
	current := initial
	started := append(os.Environ(), "CHECK_SECRET=initial", "PLATFORM_MARKER=kept", "PORT=1234")
	runtime.environment = func() []string {
		return imageReadinessSecretEnvironment(started, runtime.manifest, initial, current, map[string]string{"CHECK_SECRET": "config-fallback"})
	}
	for _, value := range []string{"initial", "rotated", "config-fallback"} {
		if value == "config-fallback" {
			current = nil
		} else {
			current = map[string]string{"CHECK_SECRET": value}
		}
		if err := os.WriteFile(want, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if result := runtime.check(t.Context(), slog.Default()); result != "" {
			t.Fatalf("%s: %s", value, result)
		}
	}
}

func TestImageReadinessGraceRetriesAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name     string
		retries  int
		grace    time.Duration
		want     string
		attempts string
	}{
		{"retry budget", 3, 0, "unhealthy", "xxx"},
		{"startup grace", 1, 100 * time.Millisecond, "", "xxx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "attempts")
			command := `printf x >> "$ATTEMPTS"; `
			if tc.grace > 0 {
				command += `test "$(cat "$ATTEMPTS")" = xxx`
			} else {
				command += "exit 1"
			}
			r := readinessTestRuntime([]string{"CMD-SHELL", command}, tc.retries)
			r.env = append(r.env, "ATTEMPTS="+marker)
			r.manifest.Healthcheck.ImageTiming.StartPeriodNS = int64(tc.grace)
			r.manifest.Healthcheck.ImageTiming.StartIntervalNS = int64(5 * time.Millisecond)
			if result := r.check(t.Context(), slog.Default()); result != tc.want {
				t.Fatalf("result %q want %q", result, tc.want)
			}
			body, err := os.ReadFile(marker)
			if err != nil || string(body) != tc.attempts {
				t.Fatalf("attempts %q: %v", body, err)
			}
		})
	}
	r := readinessTestRuntime([]string{"CMD-SHELL", "sleep 60"}, 1)
	started := time.Now()
	if result := r.check(t.Context(), slog.Default()); result != "unhealthy" {
		t.Fatal(result)
	}
	if time.Since(started) > time.Second {
		t.Fatal("probe timeout did not reap shell descendants")
	}
}

func TestImageReadinessRetirementCancelsInflightProbe(t *testing.T) {
	cmd := exec.Command("/unused")
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	r := readinessTestRuntime([]string{"CMD-SHELL", "sleep 60"}, 1)
	r.manifest.Healthcheck.ImageTiming.TimeoutNS = int64(time.Minute)
	retire := installImageReadinessRuntime(cmd, r.manifest)
	defer retire()
	current := mainImageReadiness.Load()
	done := make(chan string, 1)
	go func() { done <- current.check(t.Context(), slog.Default()) }()
	time.Sleep(20 * time.Millisecond)
	retire()
	select {
	case result := <-done:
		if result == "" {
			t.Fatal("retired process produced readiness")
		}
	case <-time.After(time.Second):
		t.Fatal("retirement left a running check")
	}
	if mainImageReadiness.Load() != nil {
		t.Fatal("retired runtime still published")
	}
}

func TestImageReadinessMissingAndCanceledFailClosed(t *testing.T) {
	for _, test := range [][]string{nil, {"NONE"}, {"CMD"}} {
		if result := readinessTestRuntime(test, 1).check(t.Context(), slog.Default()); result != "healthcheck_missing" {
			t.Fatalf("missing check passed: %q", result)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result := readinessTestRuntime([]string{"CMD", "/bin/true"}, 1).check(ctx, slog.Default()); result == "" {
		t.Fatal("canceled request passed")
	}
}

func TestImageReadinessListenerUsesFreshChallenge(t *testing.T) {
	runtime := readinessTestRuntime([]string{"CMD", "/bin/true"}, 1)
	mainImageReadiness.Store(runtime)
	t.Cleanup(func() { mainImageReadiness.CompareAndSwap(runtime, nil) })
	for _, budget := range []int64{0, 1000} {
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		server, client := os.NewFile(uintptr(pair[0]), "gate-server"), os.NewFile(uintptr(pair[1]), "gate-client")
		done := make(chan struct{})
		go func() { defer close(done); handleLivenessConn(server, slog.Default()) }()
		request := healthcheckproto.Request{Nonce: strings.Repeat("a", 64), BudgetMS: budget}
		if err := healthcheckproto.Write(client, healthcheckproto.Probe, request); err != nil {
			t.Fatal(err)
		}
		var response healthcheckproto.Response
		if err := healthcheckproto.Read(client, healthcheckproto.Ack, &response); err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		<-done
		if response.Nonce != request.Nonce || response.Healthy != (budget > 0) {
			t.Fatalf("response %+v", response)
		}
	}
}
