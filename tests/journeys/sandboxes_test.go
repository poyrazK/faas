package journeys

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Sandbox sessions (ADR-20261027090013): a seller's harness drives a live
// microVM over the public API. The journey plays that harness:
//
//   - create a session from the default preset and wait until it is placed;
//   - run commands as the guest user and as root, in the foreground and in
//     the background;
//   - write, read and list workspace files, and see a path that leaves the
//     workspace refused;
//   - reach a package registry in the default `registries` network mode and
//     be refused the open web;
//   - leave the session idle until it freezes, then thaw it with a command;
//   - persist the workspace and start a second session from it;
//   - expose a guest port and reach it only with its preview token;
//   - pause a persistent session, resume it with its memory and workspace
//     kept, and fork it (ADR-20261122132055; skipped where persistent
//     sessions are off);
//   - close the session and see its data-plane token stop working.
//
// It skips when sandbox sessions are not released to the account or are
// switched off on the installation (the documented answers, not failures).

const (
	// sandboxReadyWait bounds queued → ready: a claim by agentd, a placement
	// and a restore (or a cold boot).
	sandboxReadyWait = 3 * time.Minute
	// sandboxFreezeWait bounds an idle session's way to frozen:
	// SandboxIdleFreezeSec (30 s) plus agentd's timer tick.
	sandboxFreezeWait = 2 * time.Minute
)

type sandboxJourney struct {
	tg    *target
	key   string // deploy:write API key for the control plane
	id    string
	token string // the session's data-plane token
	label string
}

// sandboxView is GET /v1/sandboxes/{id}.
type sandboxView struct {
	ID       string            `json:"id"`
	State    string            `json:"state"`
	Token    string            `json:"token"`
	Labels   map[string]string `json:"labels"`
	ExpireAt string            `json:"expires_at"`
	Outcome  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"outcome"`
}

// sandboxExec is POST /v1/sandboxes/{id}/exec, run to completion.
type sandboxExec struct {
	Stdout    string  `json:"stdout"`
	Stderr    string  `json:"stderr"`
	ExitCode  int     `json:"exit_code"`
	TimedOut  bool    `json:"timed_out"`
	WallTimeS float64 `json:"wall_time_s"`
}

func TestSandboxes(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &sandboxJourney{tg: tg, label: tg.slug("sbx")}
	s := newStory(t)
	if !s.step("create_session", j.create) {
		return
	}
	if !s.step("session_ready", func(t *testing.T) { j.token = j.waitReady(t, j.id) }) {
		return
	}
	s.check("exec_as_user_and_root", j.execUserAndRoot)
	s.check("background_command", j.background)
	s.check("workspace_files", j.files)
	s.check("registries_egress", j.egress)
	s.check("idle_freeze_and_thaw", j.freezeAndThaw)
	s.check("persist_and_restore_workspace", j.persistAndRestore)
	s.check("private_preview_port", j.previewPort)
	s.check("persistent_pause_resume_fork", j.persistent)
	s.step("close_session", j.close)
}

// create opens the journey's session, or skips where sessions are not
// offered. The session is closed when the test ends, whatever happened.
func (j *sandboxJourney) create(t *testing.T) {
	j.key = j.tg.scopedKey(t, "sandbox", "deploy:write")
	j.id = j.createSession(t, map[string]any{})
}

func (j *sandboxJourney) createSession(t *testing.T, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"preset": "codex", "labels": map[string]string{"e2ej": j.label}}
	for k, v := range extra {
		body[k] = v
	}
	res := j.tg.apiCall(http.MethodPost, "/v1/sandboxes", body, j.key)
	if res.err != nil {
		t.Fatalf("create sandbox: %v", res.err)
	}
	if res.status != http.StatusCreated && res.status != http.StatusOK && res.status != http.StatusAccepted {
		j.skipUnavailable(t, res)
		t.Fatalf("create sandbox: %s", j.tg.redact(res.String()))
	}
	var v sandboxView
	if err := json.Unmarshal(res.body, &v); err != nil || v.ID == "" {
		t.Fatalf("create sandbox answered no id: %s", tail(string(res.body), 500))
	}
	id := v.ID
	t.Cleanup(func() { j.tg.apiCall(http.MethodDelete, "/v1/sandboxes/"+id, nil, j.key) })
	return id
}

// skipUnavailable skips on the answers that mean sessions are not offered
// here: an unreleased capability answers like an absent route, a
// control-plane host without them answers capacity_unavailable, and a plan
// without sessions (Free) answers its zero limit.
func (j *sandboxJourney) skipUnavailable(t *testing.T, res httpResult) {
	t.Helper()
	code := problemCode(res.body)
	switch {
	case res.status == http.StatusNotFound && code == "":
		t.Skip("sandbox sessions are not released to this account (the route answers 404)")
	case code == "capacity_unavailable", code == "capability_not_released":
		t.Skipf("sandbox sessions are unavailable here: %s", tail(string(res.body), 300))
	case code == "agent_limit" && strings.Contains(string(res.body), "SandboxMaxLifetimeSec"):
		t.Skipf("this account's plan has no sandbox sessions: %s", tail(string(res.body), 300))
	}
}

// decodeB64 decodes the base64 output of a background command.
func decodeB64(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	return string(b)
}

func problemCode(body []byte) string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &p)
	return p.Code
}

func (j *sandboxJourney) view(t *testing.T, id string) sandboxView {
	t.Helper()
	res := j.tg.apiCall(http.MethodGet, "/v1/sandboxes/"+id, nil, j.key)
	if res.err != nil || res.status != http.StatusOK {
		t.Fatalf("read sandbox %s: %s", id, j.tg.redact(res.String()))
	}
	var v sandboxView
	if err := json.Unmarshal(res.body, &v); err != nil {
		t.Fatalf("decode sandbox view: %v", err)
	}
	if v.Token != "" {
		j.tg.addSensitive(v.Token)
	}
	return v
}

// waitReady polls until the session is placed and returns its token.
func (j *sandboxJourney) waitReady(t *testing.T, id string) string {
	t.Helper()
	start := time.Now()
	deadline := start.Add(sandboxReadyWait)
	for delay := 250 * time.Millisecond; ; delay = min(delay*2, 2*time.Second) {
		v := j.view(t, id)
		switch v.State {
		case "ready", "frozen":
			if v.Token != "" {
				t.Logf("sandbox %s %s after %s", id, v.State, time.Since(start).Round(time.Millisecond))
				return v.Token
			}
		case "queued", "starting":
		default:
			outcome := ""
			if v.Outcome != nil {
				outcome = v.Outcome.Code + " " + v.Outcome.Message
			}
			t.Fatalf("sandbox %s ended %s before it was ready: %s", id, v.State, outcome)
		}
		if time.Now().After(deadline) {
			t.Fatalf("sandbox %s still %s after %s", id, v.State, sandboxReadyWait)
		}
		time.Sleep(delay)
	}
}

// data sends one data-plane request with the session token. body is sent
// as JSON unless it is []byte.
func (j *sandboxJourney) data(method, id, token, path string, query url.Values, body any) httpResult {
	var reader io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		reader, contentType = bytes.NewReader(b), "application/octet-stream"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return httpResult{err: err}
		}
		reader, contentType = bytes.NewReader(raw), "application/json"
	}
	u := j.tg.api + "/v1/sandboxes/" + id + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, u, reader)
	if err != nil {
		return httpResult{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "gregale-e2e-journeys")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return httpResult{err: err, elapsed: time.Since(start)}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return httpResult{status: resp.StatusCode, header: resp.Header, body: b, elapsed: time.Since(start), err: err}
}

// run executes a shell command in the journey's session and fails the step
// unless the API answered.
func (j *sandboxJourney) run(t *testing.T, id, token, runAs, cmd string) sandboxExec {
	t.Helper()
	body := map[string]any{"cmd": []string{"/bin/sh", "-c", cmd}, "timeout_ms": 120000}
	if runAs != "" {
		body["run_as"] = runAs
	}
	res := j.data(http.MethodPost, id, token, "/exec", nil, body)
	if res.err != nil || res.status != http.StatusOK {
		t.Fatalf("exec %q: %s", cmd, j.tg.redact(res.String()))
	}
	var out sandboxExec
	if err := json.Unmarshal(res.body, &out); err != nil {
		t.Fatalf("decode exec %q: %v: %s", cmd, err, tail(string(res.body), 300))
	}
	return out
}

func (j *sandboxJourney) mustRun(t *testing.T, runAs, cmd string) string {
	t.Helper()
	out := j.run(t, j.id, j.token, runAs, cmd)
	if out.ExitCode != 0 || out.TimedOut {
		t.Fatalf("exec %q exited %d (timed out %t): %s%s", cmd, out.ExitCode, out.TimedOut, tail(out.Stdout, 500), tail(out.Stderr, 500))
	}
	return strings.TrimSpace(out.Stdout)
}

// execUserAndRoot: commands run as the unprivileged guest user by default
// and as root on request (§10); the user has passwordless sudo.
func (j *sandboxJourney) execUserAndRoot(t *testing.T) {
	if uid := j.mustRun(t, "", "id -u"); uid != "1000" {
		t.Errorf("default user uid = %q, want 1000", uid)
	}
	if uid := j.mustRun(t, "root", "id -u"); uid != "0" {
		t.Errorf("run_as root uid = %q, want 0", uid)
	}
	if out := j.mustRun(t, "", "sudo -n id -u"); out != "0" {
		t.Errorf("sudo as the guest user = %q, want 0", out)
	}
	if wd := j.mustRun(t, "", "pwd"); wd != "/workspace" {
		t.Errorf("working directory = %q, want /workspace", wd)
	}
	failed := j.run(t, j.id, j.token, "", "echo out; echo err >&2; exit 3")
	if failed.ExitCode != 3 || strings.TrimSpace(failed.Stdout) != "out" || strings.TrimSpace(failed.Stderr) != "err" {
		t.Errorf("exit status and streams = %d %q %q, want 3 out err", failed.ExitCode, failed.Stdout, failed.Stderr)
	}
}

// background: a background command answers a handle at once, keeps running,
// and its output and exit status can be read later.
func (j *sandboxJourney) background(t *testing.T) {
	res := j.data(http.MethodPost, j.id, j.token, "/exec", nil, map[string]any{
		"cmd": []string{"/bin/sh", "-c", "sleep 2; echo finished"}, "background": true,
	})
	if res.err != nil || res.status != http.StatusOK && res.status != http.StatusAccepted {
		t.Fatalf("background exec: %s", j.tg.redact(res.String()))
	}
	var started struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(res.body, &started); err != nil || started.Handle == "" {
		t.Fatalf("background exec answered no handle: %s", tail(string(res.body), 300))
	}
	if res.elapsed > 2*time.Second {
		t.Errorf("background exec took %s to answer; it should not wait for the command", res.elapsed)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		out := j.data(http.MethodGet, j.id, j.token, "/exec/"+started.Handle, nil, nil)
		if out.err != nil || out.status != http.StatusOK {
			t.Fatalf("read background output: %s", j.tg.redact(out.String()))
		}
		var o struct {
			Running  bool   `json:"running"`
			ExitCode *int   `json:"exit_code"`
			Stdout   string `json:"stdout"` // base64
		}
		if err := json.Unmarshal(out.body, &o); err != nil {
			t.Fatalf("decode background output: %v", err)
		}
		if !o.Running && o.ExitCode != nil {
			stdout := decodeB64(o.Stdout)
			if *o.ExitCode != 0 || strings.TrimSpace(stdout) != "finished" {
				t.Errorf("background command = exit %d, stdout %q; want 0, finished", *o.ExitCode, stdout)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background command still running after a minute")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// files: the files API reads and writes inside /workspace only.
func (j *sandboxJourney) files(t *testing.T) {
	content := []byte("hello from " + j.label + "\n")
	put := j.data(http.MethodPut, j.id, j.token, "/files", url.Values{"path": {"notes/hello.txt"}, "parents": {"true"}}, content)
	if put.err != nil || put.status/100 != 2 {
		t.Fatalf("write file: %s", j.tg.redact(put.String()))
	}
	get := j.data(http.MethodGet, j.id, j.token, "/files", url.Values{"path": {"notes/hello.txt"}}, nil)
	if get.err != nil || get.status != http.StatusOK || !bytes.Equal(get.body, content) {
		t.Errorf("read file back = %s", j.tg.redact(get.String()))
	}
	if got := j.mustRun(t, "", "cat /workspace/notes/hello.txt"); got != strings.TrimSpace(string(content)) {
		t.Errorf("the guest sees %q, want the written content", got)
	}
	list := j.data(http.MethodGet, j.id, j.token, "/files:list", url.Values{"path": {"notes"}}, nil)
	if list.err != nil || list.status != http.StatusOK || !strings.Contains(string(list.body), "hello.txt") {
		t.Errorf("list notes = %s", j.tg.redact(list.String()))
	}
	for _, escape := range []string{"../etc/passwd", "/etc/passwd"} {
		res := j.data(http.MethodGet, j.id, j.token, "/files", url.Values{"path": {escape}}, nil)
		if res.err == nil && res.status == http.StatusOK {
			t.Errorf("reading %q outside the workspace answered 200", escape)
		}
	}
	// A symlink that leaves the workspace is refused too (§2).
	j.mustRun(t, "", "ln -sf /etc/passwd /workspace/escape")
	if res := j.data(http.MethodGet, j.id, j.token, "/files", url.Values{"path": {"escape"}}, nil); res.err == nil && res.status == http.StatusOK {
		t.Errorf("reading a symlink out of the workspace answered 200")
	}
}

// egress: registries mode reaches package registries over 443 and nothing
// else; the default mode is registries (§6).
func (j *sandboxJourney) egress(t *testing.T) {
	probe := func(u string) sandboxExec {
		return j.run(t, j.id, j.token, "", fmt.Sprintf(
			`node -e 'fetch(%q,{signal:AbortSignal.timeout(15000)}).then(r=>{console.log(r.status)},e=>{console.log("refused");process.exit(7)})'`, u))
	}
	if out := probe("https://registry.npmjs.org/"); out.ExitCode != 0 || strings.TrimSpace(out.Stdout) != "200" {
		t.Errorf("registry fetch = exit %d %q %q, want 200", out.ExitCode, out.Stdout, tail(out.Stderr, 300))
	}
	if out := probe("https://example.com/"); out.ExitCode == 0 {
		t.Errorf("registries mode reached the open web: %q", out.Stdout)
	}
}

// freezeAndThaw: with no calls the session freezes; the next command thaws
// it and runs (§5). The thaw is logged, not asserted, until it has a budget.
func (j *sandboxJourney) freezeAndThaw(t *testing.T) {
	j.mustRun(t, "", "echo before-freeze > /workspace/freeze.txt")
	deadline := time.Now().Add(sandboxFreezeWait)
	for {
		time.Sleep(10 * time.Second)
		if v := j.view(t, j.id); v.State == "frozen" {
			if v.Token != "" {
				j.token = v.Token
			}
			break
		} else if v.State != "ready" {
			t.Fatalf("an idle session went %s instead of frozen", v.State)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session did not freeze within %s of idling", sandboxFreezeWait)
		}
	}
	start := time.Now()
	got := j.mustRun(t, "", "cat /workspace/freeze.txt")
	t.Logf("first command on a frozen session answered in %s", time.Since(start).Round(time.Millisecond))
	if got != "before-freeze" {
		t.Errorf("after the thaw the workspace holds %q, want before-freeze", got)
	}
}

// persistAndRestore: a persisted workspace starts a new session (§3).
func (j *sandboxJourney) persistAndRestore(t *testing.T) {
	j.mustRun(t, "", "echo persisted > /workspace/persist.txt")
	res := j.data(http.MethodPost, j.id, j.token, "/workspace:persist", nil, map[string]any{})
	if res.err == nil && problemCode(res.body) == "sandbox_unsupported" {
		t.Skipf("persisted workspaces are switched off on this installation: %s", tail(string(res.body), 300))
	}
	if res.err != nil || res.status/100 != 2 {
		t.Fatalf("persist workspace: %s", j.tg.redact(res.String()))
	}
	var snap struct {
		SnapshotID string `json:"snapshot_id"`
		Bytes      int64  `json:"bytes"`
	}
	if err := json.Unmarshal(res.body, &snap); err != nil || snap.SnapshotID == "" || snap.Bytes <= 0 {
		t.Fatalf("persist answered %s", tail(string(res.body), 300))
	}
	second := j.createSession(t, map[string]any{"snapshot_id": snap.SnapshotID})
	token := j.waitReady(t, second)
	out := j.run(t, second, token, "", "cat /workspace/persist.txt")
	if out.ExitCode != 0 || strings.TrimSpace(out.Stdout) != "persisted" {
		t.Errorf("the restored session reads %q (exit %d), want persisted", out.Stdout, out.ExitCode)
	}
	j.tg.apiCall(http.MethodDelete, "/v1/sandboxes/"+second, nil, j.key)
}

// previewPort: a private port answers through its preview URL with its
// preview token and as an absent route without it (§11).
func (j *sandboxJourney) previewPort(t *testing.T) {
	res := j.data(http.MethodPost, j.id, j.token, "/exec", nil, map[string]any{
		"cmd": []string{"node", "-e", `require("http").createServer((q,s)=>s.end("preview-ok")).listen(8080)`}, "background": true,
	})
	if res.err != nil || res.status/100 != 2 {
		t.Fatalf("start a server in the guest: %s", j.tg.redact(res.String()))
	}
	j.mustRun(t, "", `for i in $(seq 50); do node -e 'require("net").connect(8080).on("connect",()=>process.exit(0)).on("error",()=>process.exit(1))' && exit 0; sleep 0.2; done; exit 1`)
	port := j.tg.apiCall(http.MethodPost, "/v1/sandboxes/"+j.id+"/ports", map[string]any{"port": 8080, "visibility": "private"}, j.key)
	if port.err == nil && problemCode(port.body) == "sandbox_previews_off" {
		t.Skip("preview URLs are off on this installation (FAAS_SANDBOX_PREVIEW_DOMAIN unset)")
	}
	if port.err != nil || port.status/100 != 2 {
		t.Fatalf("expose port: %s", j.tg.redact(port.String()))
	}
	var pv struct {
		URL          string `json:"url"`
		PreviewToken string `json:"preview_token"`
	}
	if err := json.Unmarshal(port.body, &pv); err != nil || pv.URL == "" || pv.PreviewToken == "" {
		t.Fatalf("expose port answered %s", tail(string(port.body), 300))
	}
	j.tg.addSensitive(pv.PreviewToken)
	get := func(token string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, pv.URL, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return resp.StatusCode, string(b)
	}
	if status, body := get(pv.PreviewToken); status != http.StatusOK || body != "preview-ok" {
		t.Errorf("preview with its token = %d %q, want 200 preview-ok", status, tail(body, 200))
	}
	if status, _ := get(""); status != http.StatusNotFound {
		t.Errorf("preview without a token = %d, want 404", status)
	}
}

// close: closing ends the session and its token stops working.
func (j *sandboxJourney) close(t *testing.T) {
	res := j.tg.apiCall(http.MethodDelete, "/v1/sandboxes/"+j.id, nil, j.key)
	if res.err != nil || res.status/100 != 2 {
		t.Fatalf("close sandbox: %s", j.tg.redact(res.String()))
	}
	deadline := time.Now().Add(time.Minute)
	for {
		if v := j.view(t, j.id); v.State == "closed" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("the session is %s a minute after close", v.State)
		}
		time.Sleep(time.Second)
	}
	after := j.data(http.MethodPost, j.id, j.token, "/exec", nil, map[string]any{"cmd": []string{"true"}})
	if after.err == nil && after.status == http.StatusOK {
		t.Errorf("a closed session's token still runs commands")
	}
}
