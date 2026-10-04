package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

func TestPreloadFailurePreservesRunningDaemon(t *testing.T) {
	original := runCommand
	t.Cleanup(func() { runCommand = original })
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
	}{
		{name: "missing"},
		{name: "non-executable", prepare: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("candidate"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "directory", prepare: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", prepare: func(t *testing.T, path string) {
			if err := os.Symlink("not-a-release-executable", path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.prepare != nil {
				tt.prepare(t, filepath.Join(dir, "apid"))
			}
			called := false
			runCommand = func(context.Context, string, ...string) error {
				called = true
				return nil
			}
			r := hostRuntime{binaryDir: dir, serviceOrder: []string{"apid"}}
			if err := r.Restart(t.Context(), releasebundle.Manifest{}); err == nil {
				t.Fatal("expected preload failure")
			}
			if called {
				t.Fatal("a preload failure must not issue any service mutation")
			}
		})
	}
}

func TestCancelledPreloadPreservesRunningDaemon(t *testing.T) {
	original := runCommand
	t.Cleanup(func() { runCommand = original })
	called := false
	runCommand = func(context.Context, string, ...string) error {
		called = true
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := hostRuntime{binaryDir: t.TempDir(), serviceOrder: []string{"apid"}}
	if err := r.Restart(ctx, releasebundle.Manifest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if called {
		t.Fatal("cancelled preload must not issue any service mutation")
	}
}

func TestDefaultRuntimePreloadsPublishedRelease(t *testing.T) {
	if got := defaultHostRuntime().binaryDir; got != "/opt/faas/current/bin" {
		t.Fatalf("preload directory = %q", got)
	}
}
