package main

// adr: 20261122132055 §3 — a session restored as a fork learns its source:
// later commands see GREGALE_FORKED_FROM over the session's and their own
// env, and running background commands that asked at exec get SIGUSR2 to
// reseed; the others are left alone. Only vmmd sends forked.

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

func TestSandboxForkedMarksCommandsAndSignalsReseeds(t *testing.T) {
	f := newSandboxFixture(t)
	// Each command writes a line when SIGUSR2 reaches it, and keeps running.
	trap := `trap 'echo reseeded' USR2; echo ready; while :; do sleep 0.05; done`
	asked := execReq("sh", "-c", trap)
	asked.ReseedOnFork = true
	reseeding := startBackground(t, f, asked)
	plain := startBackground(t, f, execReq("sh", "-c", trap))
	waitOutput := func(handle, want string) sandboxproto.ExecOutput {
		t.Helper()
		var out sandboxproto.ExecOutput
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(string(out.Stdout), want) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: no %q in %q", handle, want, out.Stdout)
			}
			time.Sleep(20 * time.Millisecond)
			f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecOutput, Handle: handle}, nil, nil).decode(t, &out)
		}
		return out
	}
	waitOutput(reseeding, "ready")
	waitOutput(plain, "ready")

	const source = "6f1c1c0e-1111-4222-8333-944444444444"
	var res sandboxproto.ForkedResult
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpForked, ForkedFrom: source}, nil, nil).decode(t, &res)
	if res.Signaled != 1 {
		t.Fatalf("signaled %d commands, want 1", res.Signaled)
	}
	waitOutput(reseeding, "reseeded")
	time.Sleep(200 * time.Millisecond)
	var out sandboxproto.ExecOutput
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecOutput, Handle: plain}, nil, nil).decode(t, &out)
	if strings.Contains(string(out.Stdout), "reseeded") || !out.Running {
		t.Fatalf("a command that did not ask was signaled: %+v", out)
	}

	// The marker goes over the session's env and the command's own.
	var env sandboxproto.SessionEnvResult
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpSessionEnv, Env: map[string]string{sandboxproto.ForkedFromEnv: "spoofed"}}, nil, nil).decode(t, &env)
	req := execReq("sh", "-c", `printf '%s' "$GREGALE_FORKED_FROM"`)
	req.Env = map[string]string{sandboxproto.ForkedFromEnv: "mine"}
	r := f.call(t, req, nil, nil)
	r.decode(t, &sandboxproto.ExecResult{})
	if got := r.stdout.String(); got != source {
		t.Fatalf("GREGALE_FORKED_FROM = %q, want %q", got, source)
	}

	for _, bad := range []string{"", "../x", strings.Repeat("a", 200)} {
		if err := (sandboxproto.Request{Op: sandboxproto.OpForked, ForkedFrom: bad}).Validate(); err == nil {
			t.Fatalf("forked_from %q accepted", bad)
		}
	}
	if !sandboxproto.IsHostOnly(sandboxproto.OpForked) {
		t.Fatal("callers may send forked")
	}
}
