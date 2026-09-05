package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDevSourceFingerprintTracksDeployableFiles(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := devSourceFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}

	ignoredDir := filepath.Join(dir, "node_modules")
	if err := os.Mkdir(ignoredDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignoredDir, "noise.js"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	ignored, err := devSourceFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ignored != first {
		t.Fatal("default-excluded file changed developer source fingerprint")
	}

	if err := os.WriteFile(source, []byte("package main\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(time.Second)
	if err := os.Chtimes(source, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	changed, err := devSourceFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("source edit did not change developer source fingerprint")
	}
}

func TestWaitForDevSourceChangeDebouncesWriteBurst(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "package.json")
	if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := devSourceFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}

	writesDone := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		if writeErr := os.WriteFile(source, []byte("{\"step\":1}\n"), 0o600); writeErr != nil {
			writesDone <- writeErr
			return
		}
		time.Sleep(60 * time.Millisecond)
		writesDone <- os.WriteFile(source, []byte("{\"step\":2,\"done\":true}\n"), 0o600)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := waitForDevSourceChangeWithIntervals(ctx, dir, before, 5*time.Millisecond, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-writesDone; err != nil {
		t.Fatal(err)
	}
	want, err := devSourceFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("watch returned before the source write burst settled")
	}
}

func TestRunDevWatchLoopRetriesAfterFailedDeploy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var first, second [sha256.Size]byte
	second[0] = 1
	initial := devSourceConfig{shape: shapeApp}
	updated := devSourceConfig{shape: shapeFunction, runtime: runtimePython312, handler: "handler.handler"}

	var deployments []devSourceConfig
	var refreshed []devSourceConfig
	var failures []int
	waitCalls := 0
	exit := runDevWatchLoop(ctx, "project", first, initial, false, devLoopOps{
		deploy: func(config devSourceConfig) int {
			deployments = append(deployments, config)
			if len(deployments) == 1 {
				return 7
			}
			return 0
		},
		waitForChange: func(_ context.Context, _ string, previous [sha256.Size]byte) ([sha256.Size]byte, error) {
			waitCalls++
			if waitCalls == 1 {
				if previous != first {
					t.Fatalf("first previous fingerprint = %x, want %x", previous, first)
				}
				return second, nil
			}
			if previous != second {
				t.Fatalf("retry previous fingerprint = %x, want %x", previous, second)
			}
			cancel()
			return second, context.Canceled
		},
		resolve: func(string) (devSourceConfig, error) { return updated, nil },
		refresh: func(config devSourceConfig) error {
			refreshed = append(refreshed, config)
			return nil
		},
		onDeployFailed: func(code int) { failures = append(failures, code) },
	})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if len(deployments) != 2 || deployments[0] != initial || deployments[1] != updated {
		t.Fatalf("deployments = %+v, want initial then re-resolved config", deployments)
	}
	if len(refreshed) != 1 || refreshed[0] != updated {
		t.Fatalf("refreshed = %+v, want updated config", refreshed)
	}
	if len(failures) != 1 || failures[0] != 7 {
		t.Fatalf("failure callbacks = %v, want [7]", failures)
	}
}

func TestRunDevWatchLoopSkipsUndeployableSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var first, second [sha256.Size]byte
	second[0] = 1
	resolveErr := errors.New("source is between generated states")

	deployments := 0
	resolveFailures := 0
	waitCalls := 0
	exit := runDevWatchLoop(ctx, "project", first, devSourceConfig{shape: shapeApp}, false, devLoopOps{
		deploy: func(devSourceConfig) int {
			deployments++
			return 0
		},
		waitForChange: func(_ context.Context, _ string, _ [sha256.Size]byte) ([sha256.Size]byte, error) {
			waitCalls++
			if waitCalls == 1 {
				return second, nil
			}
			cancel()
			return second, context.Canceled
		},
		resolve: func(string) (devSourceConfig, error) { return devSourceConfig{}, resolveErr },
		refresh: func(devSourceConfig) error { t.Fatal("refresh called for invalid source"); return nil },
		onResolveFailed: func(err error) {
			resolveFailures++
			if !errors.Is(err, resolveErr) {
				t.Errorf("resolve error = %v", err)
			}
		},
	})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if deployments != 1 {
		t.Fatalf("deployments = %d, want only the initial deployment", deployments)
	}
	if resolveFailures != 1 {
		t.Fatalf("resolve failures = %d, want 1", resolveFailures)
	}
}

func TestResolveDevSourceConfigReflectsRuntimeChanges(t *testing.T) {
	dir := t.TempDir()
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	js := filepath.Join(dir, "handler.js")
	if err := os.WriteFile(js, []byte("export function handler() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := resolveDevSourceConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.shape != shapeFunction || first.runtime != runtimeNode22 {
		t.Fatalf("JavaScript config = %+v, want node function", first)
	}

	if err := os.Remove(js); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "handler.py"), []byte("def handler(event, context): pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := resolveDevSourceConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.shape != shapeFunction || second.runtime != runtimePython312 {
		t.Fatalf("Python config = %+v, want python function", second)
	}
	if first == second {
		t.Fatal("developer source configuration was cached across changes")
	}
}
