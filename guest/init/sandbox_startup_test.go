package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// adr: 20261027090013 step 10 — version 2 manifests carry a template's
// commands, version 1 may not, and a ready command needs a timeout.
func TestParseSandboxManifestCommands(t *testing.T) {
	for raw, ok := range map[string]bool{
		`{"kind":"sandbox","version":1}`: true,
		`{"kind":"sandbox","version":2,"start_cmd":"serve","ready_cmd":"probe","ready_timeout_ms":5000}`: true,
		`{"kind":"sandbox","version":2,"start_cmd":"serve"}`:                                             true,
		`{"kind":"sandbox","version":1,"start_cmd":"serve"}`:                                             false,
		`{"kind":"sandbox","version":2,"ready_cmd":"probe"}`:                                             false,
		`{"kind":"sandbox","version":3}`:                                                                 false,
		`{"kind":"sandbox","version":2,"extra":1}`:                                                       false,
	} {
		if _, err := parseSandboxManifest([]byte(raw)); (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", raw, err, ok)
		}
	}
}

// The start command keeps running and the ready command is retried until it
// passes; a ready command that never passes fails startup at its timeout.
func TestRunSandboxStartup(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := execRunner{uid: -1}
	marker := filepath.Join(dir, "up")
	var out bytes.Buffer
	m := sandboxManifest{StartCmd: "echo starting; sleep 2; touch " + marker + "; sleep 30",
		ReadyCmd: "test -e " + marker, ReadyTimeoutMs: 15_000}
	start := time.Now()
	if err := runSandboxStartup(context.Background(), runner, m, dir, dir, &out, log); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 2*time.Second {
		t.Fatalf("ready after %v, before the start command made the marker", took)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(out.String(), "starting") {
		t.Fatalf("start output %q", out.String())
	}

	m = sandboxManifest{ReadyCmd: "exit 1", ReadyTimeoutMs: 2_000}
	if err := runSandboxStartup(context.Background(), runner, m, dir, dir, io.Discard, log); !errors.Is(err, errSandboxNotReady) {
		t.Fatalf("never ready = %v", err)
	}
	if err := runSandboxStartup(context.Background(), runner, sandboxManifest{}, dir, dir, io.Discard, log); err != nil {
		t.Fatalf("no commands = %v", err)
	}
}
