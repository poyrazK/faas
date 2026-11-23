package journeys

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sandboxPauseWait bounds running → paused: a capture of the session's
// memory and volume on its node (ADR-20261122132055 §2).
const sandboxPauseWait = 3 * time.Minute

// persistentView is the part of GET /v1/sandboxes/{id} a persistent
// session adds (ADR-20261122132055 §9).
type persistentView struct {
	sandboxView
	Kind        string `json:"kind"`
	PausedAt    string `json:"paused_at"`
	Durability  string `json:"durability"`
	ResumedCold bool   `json:"resumed_cold"`
	ForkedFrom  string `json:"forked_from"`
	StoredBytes int64  `json:"stored_bytes"`
}

// persistent: a persistent session keeps its memory and /workspace across a
// pause (a process it started is still running after the resume), and a
// fork starts with both as they were (ADR-20261122132055). It runs on its
// own session, so the journey's main session is untouched. Skips where
// persistent sessions are switched off or the plan has none.
func (j *sandboxJourney) persistent(t *testing.T) {
	id := j.createPersistent(t)
	token := j.waitReady(t, id)

	// A process in memory and a file on the volume, both checked after the
	// resume and in the fork.
	j.mustRunIn(t, id, token, "echo kept > /workspace/kept.txt")
	keeper := j.data(http.MethodPost, id, token, "/exec", nil, map[string]any{
		"cmd": []string{"/bin/sh", "-c", "echo $$ > /workspace/keeper.pid; exec sleep 86400"}, "background": true,
	})
	if keeper.err != nil || keeper.status/100 != 2 {
		t.Fatalf("start a background process: %s", j.tg.redact(keeper.String()))
	}
	pid := j.mustRunIn(t, id, token, "for i in $(seq 50); do [ -s /workspace/keeper.pid ] && cat /workspace/keeper.pid && exit 0; sleep 0.2; done; exit 1")

	start := time.Now()
	if res := j.tg.apiCall(http.MethodPost, "/v1/sandboxes/"+id+"/pause", map[string]any{}, j.key); res.err != nil || res.status/100 != 2 {
		t.Fatalf("pause: %s", j.tg.redact(res.String()))
	}
	paused := j.waitState(t, id, "paused", sandboxPauseWait, "pausing", "ready", "frozen")
	t.Logf("session paused in %s (durability %s, %d bytes stored)", time.Since(start).Round(time.Millisecond), paused.Durability, paused.StoredBytes)
	if paused.PausedAt == "" {
		t.Errorf("a paused session's view has no paused_at")
	}
	if paused.Token != "" {
		t.Errorf("a paused session's view still carries a data-plane token")
	}

	start = time.Now()
	if res := j.tg.apiCall(http.MethodPost, "/v1/sandboxes/"+id+"/resume", map[string]any{}, j.key); res.err != nil || res.status/100 != 2 {
		t.Fatalf("resume: %s", j.tg.redact(res.String()))
	}
	j.waitState(t, id, "ready", sandboxReadyWait, "paused", "resuming", "queued", "starting")
	token = j.waitReady(t, id)
	t.Logf("session resumed in %s", time.Since(start).Round(time.Millisecond))
	if v := j.persistentView(t, id); v.ResumedCold {
		t.Errorf("a resume on a healthy installation booted cold (memory lost)")
	}
	if got := j.mustRunIn(t, id, token, "cat /workspace/kept.txt"); got != "kept" {
		t.Errorf("after the resume /workspace/kept.txt holds %q, want kept", got)
	}
	if out := j.run(t, id, token, "", "kill -0 "+pid); out.ExitCode != 0 {
		t.Errorf("the process started before the pause (pid %s) is gone after the resume", pid)
	}

	start = time.Now()
	res := j.tg.apiCall(http.MethodPost, "/v1/sandboxes/"+id+"/fork", map[string]any{"count": 1}, j.key)
	if res.err != nil || res.status/100 != 2 {
		if problemCode(res.body) == "agent_limit" && strings.Contains(string(res.body), "SandboxForkMax") {
			t.Skipf("this account's plan has no forks: %s", tail(string(res.body), 300))
		}
		t.Fatalf("fork: %s", j.tg.redact(res.String()))
	}
	var forked struct {
		Forks []persistentView `json:"forks"`
	}
	if err := json.Unmarshal(res.body, &forked); err != nil || len(forked.Forks) != 1 {
		t.Fatalf("fork answered %s", tail(string(res.body), 300))
	}
	fork := forked.Forks[0].ID
	t.Cleanup(func() { j.tg.apiCall(http.MethodDelete, "/v1/sandboxes/"+fork, nil, j.key) })
	forkToken := j.waitReady(t, fork)
	t.Logf("fork ready in %s", time.Since(start).Round(time.Millisecond))
	if v := j.persistentView(t, fork); v.ForkedFrom != id || v.Kind != "ephemeral" {
		t.Errorf("fork view: forked_from %q kind %q, want %s ephemeral", v.ForkedFrom, v.Kind, id)
	}
	if got := j.mustRunIn(t, fork, forkToken, "cat /workspace/kept.txt"); got != "kept" {
		t.Errorf("the fork's /workspace/kept.txt holds %q, want kept", got)
	}
	if out := j.run(t, fork, forkToken, "", "kill -0 "+pid); out.ExitCode != 0 {
		t.Errorf("the fork has no copy of the source's process (pid %s)", pid)
	}
	// The source runs on, unchanged by the fork.
	if got := j.mustRunIn(t, id, token, "cat /workspace/kept.txt"); got != "kept" {
		t.Errorf("after the fork the source's /workspace/kept.txt holds %q, want kept", got)
	}
	j.tg.apiCall(http.MethodDelete, "/v1/sandboxes/"+fork, nil, j.key)
	j.tg.apiCall(http.MethodDelete, "/v1/sandboxes/"+id, nil, j.key)
}

// createPersistent opens a persistent session, skipping where they are not
// offered: switched off on the installation (sandbox_unsupported) or a
// plan without them (SandboxPersistentMax 0).
func (j *sandboxJourney) createPersistent(t *testing.T) string {
	t.Helper()
	body := map[string]any{"preset": "codex", "kind": "persistent", "labels": map[string]string{"e2ej": j.label}}
	res := j.tg.apiCall(http.MethodPost, "/v1/sandboxes", body, j.key)
	if res.err != nil {
		t.Fatalf("create persistent sandbox: %v", res.err)
	}
	switch code := problemCode(res.body); {
	case code == "sandbox_unsupported":
		t.Skipf("persistent sessions are switched off on this installation: %s", tail(string(res.body), 300))
	case code == "agent_limit" && strings.Contains(string(res.body), "SandboxPersistentMax"):
		t.Skipf("this account's plan has no persistent sessions: %s", tail(string(res.body), 300))
	}
	if res.status/100 != 2 {
		j.skipUnavailable(t, res)
		t.Fatalf("create persistent sandbox: %s", j.tg.redact(res.String()))
	}
	var v sandboxView
	if err := json.Unmarshal(res.body, &v); err != nil || v.ID == "" {
		t.Fatalf("create persistent sandbox answered no id: %s", tail(string(res.body), 500))
	}
	t.Cleanup(func() { j.tg.apiCall(http.MethodDelete, "/v1/sandboxes/"+v.ID, nil, j.key) })
	return v.ID
}

func (j *sandboxJourney) persistentView(t *testing.T, id string) persistentView {
	t.Helper()
	res := j.tg.apiCall(http.MethodGet, "/v1/sandboxes/"+id, nil, j.key)
	if res.err != nil || res.status != http.StatusOK {
		t.Fatalf("read sandbox %s: %s", id, j.tg.redact(res.String()))
	}
	var v persistentView
	if err := json.Unmarshal(res.body, &v); err != nil {
		t.Fatalf("decode sandbox view: %v", err)
	}
	if v.Token != "" {
		j.tg.addSensitive(v.Token)
	}
	return v
}

// waitState polls until the session reaches want, failing if it leaves the
// passing states or takes longer than wait.
func (j *sandboxJourney) waitState(t *testing.T, id, want string, wait time.Duration, passing ...string) persistentView {
	t.Helper()
	deadline := time.Now().Add(wait)
	for delay := 250 * time.Millisecond; ; delay = min(delay*2, 2*time.Second) {
		v := j.persistentView(t, id)
		if v.State == want {
			return v
		}
		ok := false
		for _, p := range passing {
			ok = ok || v.State == p
		}
		if !ok {
			outcome := ""
			if v.Outcome != nil {
				outcome = v.Outcome.Code + " " + v.Outcome.Message
			}
			t.Fatalf("sandbox %s went %s waiting for %s: %s", id, v.State, want, outcome)
		}
		if time.Now().After(deadline) {
			t.Fatalf("sandbox %s still %s after %s, want %s", id, v.State, wait, want)
		}
		time.Sleep(delay)
	}
}

func (j *sandboxJourney) mustRunIn(t *testing.T, id, token, cmd string) string {
	t.Helper()
	out := j.run(t, id, token, "", cmd)
	if out.ExitCode != 0 || out.TimedOut {
		t.Fatalf("exec %q exited %d (timed out %t): %s%s", cmd, out.ExitCode, out.TimedOut, tail(out.Stdout, 500), tail(out.Stderr, 500))
	}
	return strings.TrimSpace(out.Stdout)
}
