//go:build linux

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
)

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
