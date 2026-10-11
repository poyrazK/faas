//go:build linux

// adr:684
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

	"github.com/onebox-faas/faas/pkg/healthcheckproto"
	"golang.org/x/sys/unix"
)

func monitorGuestExchange(t *testing.T, kind, ack uint32, req healthcheckproto.Request, response any) {
	t.Helper()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	server, client := os.NewFile(uintptr(pair[0]), "monitor-server"), os.NewFile(uintptr(pair[1]), "monitor-client")
	done := make(chan struct{})
	go func() { defer close(done); handleLivenessConn(server, slog.Default()) }()
	defer func() { _ = client.Close(); <-done }()
	if err := healthcheckproto.Write(client, kind, req); err != nil {
		t.Fatal(err)
	}
	if err := healthcheckproto.Read(client, ack, response); err != nil {
		t.Fatal(err)
	}
}

func TestImageHealthcheckMonitorGuestFreshSingleAttempts(t *testing.T) {
	dir := t.TempDir()
	r := readinessTestRuntime([]string{"CMD-SHELL", `printf x >> attempts; test "$CHECK_SECRET" = accepted && test -f healthy`}, 3)
	cmd := exec.Command("/unused")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CHECK_SECRET=accepted")
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	retire := installImageReadinessRuntime(cmd, r.manifest)
	defer retire()
	request := healthcheckproto.Request{Nonce: strings.Repeat("a", 64), BudgetMS: 1000}
	var config healthcheckproto.Config
	monitorGuestExchange(t, healthcheckproto.ConfigProbe, healthcheckproto.ConfigAck, request, &config)
	if err := config.Validate(); err != nil || config.Retries != 3 || config.IntervalNS != int64(5*time.Millisecond) {
		t.Fatalf("config=%+v error=%v", config, err)
	}
	request.RuntimeID = config.RuntimeID
	if err := os.WriteFile(filepath.Join(dir, "healthy"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if i == 1 {
			if err := os.Remove(filepath.Join(dir, "healthy")); err != nil {
				t.Fatal(err)
			}
		}
		var response healthcheckproto.Response
		monitorGuestExchange(t, healthcheckproto.CheckOnce, healthcheckproto.CheckAck, request, &response)
		if response.Healthy != (i == 0) || response.Nonce != request.Nonce || response.RuntimeID != config.RuntimeID {
			t.Fatalf("response=%+v", response)
		}
	}
	contents, err := os.ReadFile(filepath.Join(dir, "attempts"))
	if err != nil || string(contents) != "xxx" {
		t.Fatalf("recurring probes retried internally: %q %v", contents, err)
	}
	retire()
	var unavailable healthcheckproto.Response
	monitorGuestExchange(t, healthcheckproto.CheckOnce, healthcheckproto.CheckAck, request, &unavailable)
	if unavailable.Error != "runtime_unavailable" || unavailable.Healthy {
		t.Fatal("retired main process returned health")
	}
	replacement := installImageReadinessRuntime(cmd, r.manifest)
	defer replacement()
	var changed healthcheckproto.Response
	monitorGuestExchange(t, healthcheckproto.CheckOnce, healthcheckproto.CheckAck, request, &changed)
	if changed.Error != "runtime_changed" || changed.Healthy {
		t.Fatal("old monitor admitted replacement runtime")
	}
}

func TestImageHealthcheckMonitorGuestGraceAndTimeout(t *testing.T) {
	r := readinessTestRuntime([]string{"CMD", "/bin/false"}, 3)
	r.manifest.Healthcheck.ImageTiming.StartPeriodNS = int64(time.Second)
	if got := r.runCheck(t.Context(), slog.Default(), true); got != "starting" {
		t.Fatal(got)
	}
	r.startedAt = time.Now().Add(-2 * time.Second)
	if got := r.runCheck(t.Context(), slog.Default(), true); got != "unhealthy" {
		t.Fatal(got)
	}
	r.manifest.Healthcheck.Test = []string{"CMD-SHELL", "sleep 60"}
	started := time.Now()
	if got := r.runCheck(t.Context(), slog.Default(), true); got != "unhealthy" {
		t.Fatal(got)
	}
	if time.Since(started) > time.Second {
		t.Fatal("timed-out recurring command leaked descendants")
	}
}

func TestImageHealthcheckMonitorReplacesLegacyPoll(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("/unused")
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	r := readinessTestRuntime([]string{"CMD", "/bin/true"}, 1)
	retire := installImageReadinessRuntime(cmd, r.manifest)
	defer retire()
	request := healthcheckproto.Request{Nonce: strings.Repeat("b", 64), BudgetMS: 1000}
	config := imageHealthcheckConfig(request, mainImageReadiness.Load())
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	marker := filepath.Join(dir, "duplicate")
	runHealthcheckPollLoop(ctx, -1, []string{"/usr/bin/touch", marker}, r.manifest, time.Millisecond, time.Second, 0, 1, slog.Default())
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("legacy polling duplicated host-owned command checks")
	}
}

func TestImageHealthcheckMonitorSerializesLegacyNegotiation(t *testing.T) {
	r := readinessTestRuntime([]string{"CMD", "/bin/true"}, 1)
	mainImageReadiness.Store(r)
	defer mainImageReadiness.CompareAndSwap(r, nil)
	r.busy <- struct{}{}
	defer func() { <-r.busy }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, release, permitted := acquireLegacyImageHealthcheck(ctx); permitted {
		release()
		t.Fatal("legacy command overlapped a fresh check")
	}
}

func TestImageHealthcheckMonitorDisconnectCancelsCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	r := readinessTestRuntime([]string{"CMD-SHELL", "touch \"$STARTED_MARKER\"; sleep 60"}, 1)
	r.manifest.Healthcheck.ImageTiming.TimeoutNS = int64(time.Minute)
	cmd := exec.Command("/unused")
	cmd.Env = append(os.Environ(), "STARTED_MARKER="+marker)
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	retire := installImageReadinessRuntime(cmd, r.manifest)
	defer retire()
	runtime := mainImageReadiness.Load()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	server, client := os.NewFile(uintptr(pair[0]), "disconnect-server"), os.NewFile(uintptr(pair[1]), "disconnect-client")
	defer func() { _ = client.Close() }()
	done := make(chan struct{})
	go func() { defer close(done); handleLivenessConn(server, slog.Default()) }()
	request := healthcheckproto.Request{Nonce: strings.Repeat("c", 64), BudgetMS: 60000, RuntimeID: runtime.id}
	if err := healthcheckproto.Write(client, healthcheckproto.CheckOnce, request); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command never started")
		}
		time.Sleep(time.Millisecond)
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("host disconnect left the guest command running")
	}
	if len(runtime.busy) != 0 {
		t.Fatal("canceled command retained the probe semaphore")
	}
}
