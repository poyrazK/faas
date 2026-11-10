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

	"github.com/onebox-faas/faas/pkg/sandboxproto"
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

// adr: 20261027090013 §12 — a session restored from a template's warm
// snapshot gets its commands by template_start: they run once, the answer
// comes when the ready command passes, and a second start is refused.
func TestSandboxTemplateStart(t *testing.T) {
	f := newSandboxFixture(t)
	f.srv.startLog = filepath.Join(t.TempDir(), "start.log")
	marker := filepath.Join(f.workspace, "up")
	req := sandboxproto.Request{Op: sandboxproto.OpTemplateStart, StartCmd: "echo starting; sleep 1; touch " + marker + "; sleep 30",
		ReadyCmd: "test -e " + marker, TimeoutMillis: 15_000}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if res := f.call(t, req, nil, nil); res.err != nil {
		t.Fatalf("template_start: %v", res.err)
	}
	if took := time.Since(start); took < time.Second {
		t.Fatalf("answered after %v, before the ready command could pass", took)
	}
	if res := f.call(t, req, nil, nil); res.code() != sandboxproto.CodeExists {
		t.Fatalf("second start = %v, want %s", res.err, sandboxproto.CodeExists)
	}
	time.Sleep(100 * time.Millisecond)
	if out, err := os.ReadFile(f.srv.startLog); err != nil || !strings.Contains(string(out), "starting") {
		t.Fatalf("start log %q, %v", out, err)
	}

	never := newSandboxFixture(t)
	never.srv.startLog = filepath.Join(t.TempDir(), "start.log")
	if res := never.call(t, sandboxproto.Request{Op: sandboxproto.OpTemplateStart, ReadyCmd: "exit 1", TimeoutMillis: 1_500}, nil, nil); res.code() != sandboxproto.CodeStartFailed {
		t.Fatalf("never ready = %v, want %s", res.err, sandboxproto.CodeStartFailed)
	}
	for _, bad := range []sandboxproto.Request{
		{Op: sandboxproto.OpTemplateStart},
		{Op: sandboxproto.OpTemplateStart, ReadyCmd: "probe"},
		{Op: sandboxproto.OpTemplateStart, StartCmd: "serve\x00"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	if !sandboxproto.IsHostOnly(sandboxproto.OpTemplateStart) || sandboxproto.IsHostOnly(sandboxproto.OpExec) {
		t.Fatal("template_start must be vmmd's own and exec a caller's")
	}
}
