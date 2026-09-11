// commands_compute_nodes_test.go — whitebox tests for the
// operator-side `gregalectl compute-nodes add` subcommand
// (Tier 1 multi-host scale-out gap #1; PR-A).
//
// The four sibling subcommands (drain / drain-status / activate /
// force-drain) are exercised by cmd/deployctl/upgrade_test.go and
// the cmd/e2e suite; add is new and lives here.
//
// Test conventions:
//   - The state seam (computeNodesStoreOpener) hands back a single
//     MemStore + a no-op close for the lifetime of each test. The
//     seam returns the SAME store across calls so the second
//     upsert in TestCmdComputeNodesAdd_Idempotent exercises the
//     real ON CONFLICT path.
//   - stdout/stderr capture: we redirect with os.Pipe(); the
//     helper restore() runs the io.Copy to drain the buffer. Tests
//     call restore() before they read the buffer (else writes
//     remain in the pipe).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestMain wires the MemStore seam before any test runs. Each
// test re-wires its OWN seam with a fresh MemStore (see
// resetMemStore) — TestMain only installs a default so a stray
// test that forgets to reset still produces a deterministic
// failure instead of a nil-pointer panic on the typed-nil pool.
func TestMain(m *testing.M) {
	setComputeNodesStoreOpener(func() (state.Store, func(), error) {
		return state.NewMemStore(), func() {}, nil
	})
	previousNoColor, hadNoColor := os.LookupEnv("NO_COLOR")
	_ = os.Unsetenv("NO_COLOR")
	noColorCached.Store(false)
	noColorVal = false
	code := m.Run()
	if hadNoColor {
		_ = os.Setenv("NO_COLOR", previousNoColor)
	} else {
		_ = os.Unsetenv("NO_COLOR")
	}
	os.Exit(code)
}

// resetMemStore re-wires the seam so successive calls inside the
// same test return the SAME MemStore. Each call must hit the
// shared store, not a new one, so the second upsert is an UPDATE
// not an INSERT.
func resetMemStore(t *testing.T) {
	t.Helper()
	st := state.NewMemStore()
	setComputeNodesStoreOpener(func() (state.Store, func(), error) {
		return st, func() {}, nil
	})
}

// captureStderrComputeNodes runs fn with os.Stderr redirected to
// a buffer, returning whatever was written. Renamed to avoid
// collision with the package-wide captureStderr at
// commands_release_sbom_gate_test.go:107.
func captureStderrComputeNodes(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	origWriter := osStderr
	os.Stderr = w
	osStderr = w
	defer func() {
		os.Stderr = orig
		osStderr = origWriter
	}()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read: %v", err)
	}
	return buf.String()
}

// captureStdoutComputeNodes runs fn with os.Stdout redirected; the
// returned restore() drains the pipe into buf BEFORE returning.
// Tests must invoke restore() and THEN read buf — otherwise the
// pipe buffer is still holding the bytes.
//
// Pattern:
//
//	buf, restore := captureStdoutComputeNodes(t)
//	restore()
//	...asserts on buf.String()...
func captureStdoutComputeNodes(t *testing.T, fn func()) (*bytes.Buffer, func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = orig
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return &buf, func() {}
}

// TestCmdComputeNodesAdd_MissingFlags asserts the usage path for
// every required flag. Mirrors the apid handler's 400 surface.
func TestCmdComputeNodesAdd_MissingFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing all flags",
			args:    []string{},
			wantErr: "--name required",
		},
		{
			name:    "missing target-url",
			args:    []string{"--name=fsn-3", "--vpcpus=160", "--mem-mb=56000", "--max-concurrency=200", "--admission-ceiling-mb=47600"},
			wantErr: "--target-url required",
		},
		{
			name:    "missing capacity",
			args:    []string{"--name=fsn-3", "--target-url=tcp://vmmd-3.faas:50051"},
			wantErr: "must all be > 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetMemStore(t)
			stderr := captureStderrComputeNodes(t, func() {
				if code := cmdComputeNodesAdd(tc.args); code != 2 {
					t.Errorf("cmdComputeNodesAdd(%v) = %d, want 2 (usage)", tc.args, code)
				}
			})
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr missing %q: got %q", tc.wantErr, stderr)
			}
		})
	}
}

// TestCmdComputeNodesAdd_BadTargetURL covers the target_url
// validation rules.
func TestCmdComputeNodesAdd_BadTargetURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"loopback-ipv4", "tcp://127.0.0.1:50051", "non-routable"},
		{"any-address", "tcp://0.0.0.0:50051", "non-routable"},
		{"loopback-ipv6", "tcp://[::1]:50051", "non-routable"},
		{"unspecified-ipv6", "tcp://[::]:50051", "non-routable"},
		{"link-local-ipv6", "tcp://[fe80::1]:50051", "non-routable"},
		{"private-ipv4", "tcp://10.42.0.1:50051", "non-routable"},
		{"private-ipv6-ula", "tcp://[fc00::1]:50051", "non-routable"},
		{"missing-port", "tcp://vmmd-3.faas", "missing port"},
		{"missing-scheme", "vmmd-3.faas:50051", "scheme must be"},
		{"unix-empty", "unix://", "with empty path"},
		{"dns-empty", "dns://", "with empty hostname"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetMemStore(t)
			stderr := captureStderrComputeNodes(t, func() {
				if code := cmdComputeNodesAdd([]string{
					"--name=fsn-3",
					"--target-url=" + tc.url,
					"--vpcpus=160", "--mem-mb=56000",
					"--max-concurrency=200", "--admission-ceiling-mb=47600",
				}); code != 2 {
					t.Errorf("cmdComputeNodesAdd(%s) = %d, want 2", tc.url, code)
				}
			})
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("target_url %q: stderr missing %q, got %q", tc.url, tc.wantErr, stderr)
			}
		})
	}
}

// TestCmdComputeNodesAdd_BadName covers the name validator.
func TestCmdComputeNodesAdd_BadName(t *testing.T) {
	cases := []string{
		"-leading-dash",
		"trailing-dash-",
		"UPPERCASE",
		"has spaces",
		"has_underscore",
	}
	for _, name := range cases {
		t.Run("name="+name, func(t *testing.T) {
			resetMemStore(t)
			stderr := captureStderrComputeNodes(t, func() {
				if code := cmdComputeNodesAdd([]string{
					"--name=" + name,
					"--target-url=tcp://vmmd-3.faas:50051",
					"--vpcpus=160", "--mem-mb=56000",
					"--max-concurrency=200", "--admission-ceiling-mb=47600",
				}); code != 2 {
					t.Errorf("cmdComputeNodesAdd(name=%q) = %d, want 2", name, code)
				}
			})
			if !strings.Contains(stderr, "valid fqdn") {
				t.Errorf("name %q: stderr missing validator output, got %q", name, stderr)
			}
		})
	}
}

// TestCmdComputeNodesAdd_HappyPath asserts the canonical flow:
// all flags pass validation, the row lands in compute_nodes, the
// OK line lands on stdout, the pg_notify hint lands on stderr.
func TestCmdComputeNodesAdd_HappyPath(t *testing.T) {
	resetMemStore(t)
	var stdoutBuf *bytes.Buffer
	stderr := captureStderrComputeNodes(t, func() {
		stdoutBuf, _ = captureStdoutComputeNodes(t, func() {
			if code := cmdComputeNodesAdd([]string{
				"--name=fsn-3",
				"--target-url=tcp://vmmd-3.faas:50051",
				"--vpcpus=160", "--mem-mb=56000",
				"--max-concurrency=200", "--admission-ceiling-mb=47600",
				"--break-glass-db", "--yes", "--reason=test_node_enroll",
			}); code != 0 {
				t.Errorf("cmdComputeNodesAdd(happy) = %d, want 0", code)
			}
		})
	})

	if !strings.Contains(stdoutBuf.String(), "OK name=fsn-3") {
		t.Errorf("stdout missing OK line: %q", stdoutBuf.String())
	}
	if !strings.Contains(stdoutBuf.String(), "target_url=tcp://vmmd-3.faas:50051") {
		t.Errorf("stdout missing target_url: %q", stdoutBuf.String())
	}
	if !strings.Contains(stdoutBuf.String(), "admission_ceiling_mb=47600") {
		t.Errorf("stdout missing admission ceiling: %q", stdoutBuf.String())
	}
	if !strings.Contains(stderr, "compute_node_changed pg_notify fired") {
		t.Errorf("stderr missing pg_notify hint: %q", stderr)
	}
}

func TestCmdComputeNodesAdd_DeferActivationLeavesRowDrained(t *testing.T) {
	resetMemStore(t)
	if code := cmdComputeNodesAdd([]string{
		"--name=fsn-3",
		"--target-url=tcp://vmmd-3.faas:50051",
		"--vpcpus=4", "--mem-mb=16384",
		"--max-concurrency=200", "--admission-ceiling-mb=13926",
		"--defer-activation",
		"--break-glass-db", "--yes", "--reason=test_node_enroll",
	}); code != 0 {
		t.Fatalf("cmdComputeNodesAdd(--defer-activation) = %d, want 0", code)
	}
	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	row, err := st.ComputeNodeByName(context.Background(), "fsn-3")
	if err != nil {
		t.Fatal(err)
	}
	if row.Active {
		t.Fatal("deferred row is active; want drained")
	}
}

// TestCmdComputeNodesAdd_HappyPath_JSON asserts the --json output
// shape is what the PR-B deploy add-node will consume.
func TestCmdComputeNodesAdd_HappyPath_JSON(t *testing.T) {
	resetMemStore(t)
	stdoutBuf, _ := captureStdoutComputeNodes(t, func() {
		captureStderrComputeNodes(t, func() {
			if code := cmdComputeNodesAdd([]string{
				"--name=fsn-3",
				"--target-url=tcp://vmmd-3.faas:50051",
				"--vpcpus=160", "--mem-mb=56000",
				"--max-concurrency=200", "--admission-ceiling-mb=47600",
				"--json",
				"--break-glass-db", "--yes", "--reason=test_node_enroll",
			}); code != 0 {
				t.Errorf("cmdComputeNodesAdd(--json) = %d, want 0", code)
			}
		})
	})

	var got map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(stdoutBuf.Bytes()), &got); err != nil {
		t.Fatalf("json parse: %v\nbody: %q", err, stdoutBuf.String())
	}
	if got["name"] != "fsn-3" {
		t.Errorf("name = %v, want fsn-3", got["name"])
	}
	if got["target_url"] != "tcp://vmmd-3.faas:50051" {
		t.Errorf("target_url = %v", got["target_url"])
	}
	if got["active"] != true {
		t.Errorf("active = %v, want true", got["active"])
	}
	if got["trace_id"] == "" {
		t.Error("trace_id is empty")
	}
}

func TestComputeNodeAddUsesAuthenticatedAPI(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/compute-nodes" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		if r.Header.Get("Idempotency-Key") == "" || r.Header.Get(operatorTraceIDHeader) != traceID {
			t.Errorf("mutation headers = %#v", r.Header)
		}
		if got := r.URL.Query().Get("reason"); got != "fleet_expansion" {
			t.Errorf("reason = %q", got)
		}
		var request api.ComputeNodeEnrollmentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Name != "fsn-3" || !request.DeferActivation {
			t.Errorf("request = %+v", request)
		}
		writeTestJSON(w, http.StatusOK, api.ComputeNodeOperatorResponse{
			ID: "11111111-1111-1111-1111-111111111111", Name: request.Name,
			TargetURL: request.TargetURL, VPCPUs: request.VPCPUs, MemMB: request.MemMB,
			MaxConcurrency: request.MaxConcurrency, AdmissionCeilingMB: request.AdmissionCeilingMB,
			Active: false, TraceID: traceID,
		})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")

	previousOpener := computeNodesStoreOpener
	computeNodesStoreOpener = func() (state.Store, func(), error) {
		t.Fatal("default enrollment path opened the database")
		return nil, func() {}, nil
	}
	t.Cleanup(func() { computeNodesStoreOpener = previousOpener })

	var stdout bytes.Buffer
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesAddTo([]string{
			"--name=fsn-3", "--target-url=tcp://vmmd-3.faas:50051",
			"--vpcpus=4", "--mem-mb=16384", "--max-concurrency=20",
			"--admission-ceiling-mb=13926", "--defer-activation",
			"--reason=fleet_expansion", "--trace-id=" + traceID, "--json",
		}, &stdout); code != 0 {
			t.Fatalf("add exit = %d", code)
		}
	})
	var output computeNodeAddedJSON
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.ID == "" || output.Active || output.TraceID != traceID {
		t.Fatalf("output = %+v", output)
	}
	if !strings.Contains(stderr, "compute_node_changed pg_notify fired") {
		t.Errorf("stderr = %q", stderr)
	}
}

// TestCmdComputeNodesAdd_FromFile asserts the PR-B bridge: a JSON
// payload file drives the same upsert without per-field flags.
func TestCmdComputeNodesAdd_FromFile(t *testing.T) {
	resetMemStore(t)
	dir := t.TempDir()
	payload := `{
	  "name": "fsn-3",
	  "target_url": "tcp://vmmd-3.faas:50051",
	  "vpcpus": 160,
	  "mem_mb": 56000,
	  "max_concurrency": 200,
	  "admission_ceiling_mb": 47600
	}`
	payloadPath := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(payloadPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	stdoutBuf, _ := captureStdoutComputeNodes(t, func() {
		captureStderrComputeNodes(t, func() {
			if code := cmdComputeNodesAdd([]string{"--from-file=" + payloadPath, "--break-glass-db", "--yes", "--reason=test_node_enroll"}); code != 0 {
				t.Errorf("cmdComputeNodesAdd(--from-file) = %d, want 0", code)
			}
		})
	})

	if !strings.Contains(stdoutBuf.String(), "OK name=fsn-3") {
		t.Errorf("stdout missing OK line: %q", stdoutBuf.String())
	}
}

// TestCmdComputeNodesAdd_FromFile_Missing asserts the missing-file
// path is exit 1 (platform error), distinct from exit 2 (usage).
func TestCmdComputeNodesAdd_FromFile_Missing(t *testing.T) {
	resetMemStore(t)
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesAdd([]string{"--from-file=/nonexistent/payload.json"}); code != 1 {
			t.Errorf("cmdComputeNodesAdd(missing file) = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "read --from-file") {
		t.Errorf("stderr missing read error: %q", stderr)
	}
}

// TestCmdComputeNodesAdd_FromFile_MalformedJSON asserts the bad-JSON
// path is exit 3 (platform error), matching the release kgv exit
// codes for parse errors.
func TestCmdComputeNodesAdd_FromFile_MalformedJSON(t *testing.T) {
	resetMemStore(t)
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesAdd([]string{"--from-file=" + badPath}); code != 3 {
			t.Errorf("cmdComputeNodesAdd(bad json) = %d, want 3", code)
		}
	})
	if !strings.Contains(stderr, "parse --from-file") {
		t.Errorf("stderr missing parse error: %q", stderr)
	}
}

// TestCmdComputeNodesAdd_Idempotent asserts a re-POST with the same
// name UPSERTs (preserves ID, refreshes capacity).
func TestCmdComputeNodesAdd_Idempotent(t *testing.T) {
	resetMemStore(t)
	commonArgs := []string{
		"--name=fsn-3",
		"--target-url=tcp://vmmd-3.faas:50051",
		"--vpcpus=160", "--mem-mb=56000",
		"--max-concurrency=200", "--admission-ceiling-mb=47600",
		"--break-glass-db", "--yes", "--reason=test_node_enroll",
	}
	buf1, _ := captureStdoutComputeNodes(t, func() {
		captureStderrComputeNodes(t, func() {
			if code := cmdComputeNodesAdd(commonArgs); code != 0 {
				t.Fatalf("first add: %d", code)
			}
		})
	})
	firstLine := buf1.String()

	buf2, _ := captureStdoutComputeNodes(t, func() {
		captureStderrComputeNodes(t, func() {
			if code := cmdComputeNodesAdd(commonArgs); code != 0 {
				t.Fatalf("second add: %d", code)
			}
		})
	})
	secondLine := buf2.String()

	firstID := extractIDFromOK(firstLine)
	secondID := extractIDFromOK(secondLine)
	if firstID == "" {
		t.Fatalf("could not extract id from first OK line: %q", firstLine)
	}
	if secondID == "" {
		t.Fatalf("could not extract id from second OK line: %q", secondLine)
	}
	if firstID != secondID {
		t.Errorf("id changed across re-POST: first=%s second=%s", firstID, secondID)
	}
}

// extractIDFromOK pulls the id=... token from an OK line emitted by
// cmdComputeNodesAdd. Format: "OK name=X id=Y ...".
func extractIDFromOK(line string) string {
	const tag = " id="
	i := strings.Index(line, tag)
	if i < 0 {
		return ""
	}
	rest := line[i+len(tag):]
	end := strings.IndexAny(rest, " \n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// _ pins the context import; harmless if unused.
var _ = context.Background

// TestCmdComputeNodesList_Empty pins the no-rows path: a fresh
// MemStore (after removing the synthetic default-local row) must
// report "(no compute nodes)" rather than print nothing (which
// looks like a hang). The --json variant would emit
// {"count":0,"nodes":[]} so CI gates can rely on a stable
// contract. We don't pin --json here because the empty case is
// the only one where we drop the synthetic default-local row,
// and keeping the contract single-shape keeps the assertion
// simpler.
func TestCmdComputeNodesList_Empty(t *testing.T) {
	resetMemStore(t)
	st := getSeededStore(t)
	defaultRow, err := st.ComputeNodeByName(context.Background(), "default-local")
	if err != nil {
		t.Fatalf("look up default-local: %v", err)
	}
	if err := st.DeleteComputeNode(context.Background(), defaultRow.ID); err != nil {
		t.Fatalf("delete default-local: %v", err)
	}

	stdout, restore := captureOsStdoutComputeNodes(t)
	code := cmdComputeNodesList([]string{"--break-glass-db"})
	restore()
	if code != 0 {
		t.Fatalf("cmdComputeNodesList(empty) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "(no compute nodes)") {
		t.Errorf("empty-list text = %q, want contains %q", stdout.String(), "(no compute nodes)")
	}
}

// TestCmdComputeNodesList_JSON_HappyPath pins the wire shape
// for the common case (two registered nodes, after removing the
// synthetic default-local). count==2, the nodes array carries
// both, ordered by name (the pgstore contract is ORDER BY name).
func TestCmdComputeNodesList_JSON_HappyPath(t *testing.T) {
	resetMemStore(t)
	st := getSeededStore(t)
	defaultRow, err := st.ComputeNodeByName(context.Background(), "default-local")
	if err != nil {
		t.Fatalf("look up default-local: %v", err)
	}
	if err := st.DeleteComputeNode(context.Background(), defaultRow.ID); err != nil {
		t.Fatalf("delete default-local: %v", err)
	}
	seedNode(t, "alpha", "unix:///run/faas/alpha.sock")
	seedNode(t, "bravo", "unix:///run/faas/bravo.sock")

	stdout, restore := captureOsStdoutComputeNodes(t)
	code := cmdComputeNodesList([]string{"--json", "--break-glass-db"})
	restore()
	if code != 0 {
		t.Fatalf("cmdComputeNodesList(--json) = %d, want 0", code)
	}
	var out computeNodesListJSON
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, stdout.String())
	}
	if out.Count != 2 {
		t.Errorf("count = %d, want 2", out.Count)
	}
	if len(out.Nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(out.Nodes))
	}
	if out.Nodes[0].Name != "alpha" || out.Nodes[1].Name != "bravo" {
		t.Errorf("name order: got [%s, %s], want [alpha, bravo]", out.Nodes[0].Name, out.Nodes[1].Name)
	}
	for _, n := range out.Nodes {
		if n.TargetURL == "" {
			t.Errorf("node %s target_url empty", n.Name)
		}
		if !n.Active {
			t.Errorf("node %s active = false, fresh seed should default active=true", n.Name)
		}
		if n.VPCPUs <= 0 || n.MemMB <= 0 || n.MaxConcurrency <= 0 || n.AdmissionCeilingMB <= 0 {
			t.Errorf("node %s has zero capacity field: vpcpus=%d mem=%d max=%d adm=%d", n.Name, n.VPCPUs, n.MemMB, n.MaxConcurrency, n.AdmissionCeilingMB)
		}
	}
}

// TestCmdComputeNodesList_ActiveOnly pins the --active-only filter.
// A drained node must be excluded; the default list path must
// include it. Without the filter, the drain / drain-status UX
// can't distinguish "node drained, ready to upgrade" from
// "node registered, never been touched". The synthetic
// default-local row is dropped at setup so the counts below are
// deterministic (default-local counts as active=true on a fresh
// MemStore — keeping it would add a constant +1 to each count).
func TestCmdComputeNodesList_ActiveOnly(t *testing.T) {
	resetMemStore(t)
	st := getSeededStore(t)
	defaultRow, err := st.ComputeNodeByName(context.Background(), "default-local")
	if err != nil {
		t.Fatalf("look up default-local: %v", err)
	}
	if err := st.DeleteComputeNode(context.Background(), defaultRow.ID); err != nil {
		t.Fatalf("delete default-local: %v", err)
	}
	seedNode(t, "alpha", "unix:///run/faas/alpha.sock")
	seedNode(t, "bravo", "unix:///run/faas/bravo.sock")
	bravo, err := st.ComputeNodeByName(context.Background(), "bravo")
	if err != nil {
		t.Fatalf("look up bravo: %v", err)
	}
	if err := st.MarkComputeNodeInactive(context.Background(), bravo.ID); err != nil {
		t.Fatalf("drain bravo: %v", err)
	}

	// Default path: both visible (alpha + bravo; bravo is drained
	// but still in compute_nodes with active=false).
	stdoutAll, restoreAll := captureOsStdoutComputeNodes(t)
	if code := cmdComputeNodesList([]string{"--json", "--break-glass-db"}); code != 0 {
		t.Fatalf("list default = %d, want 0", code)
	}
	restoreAll()
	var all computeNodesListJSON
	if err := json.Unmarshal(stdoutAll.Bytes(), &all); err != nil {
		t.Fatalf("unmarshal all: %v (raw: %q)", err, stdoutAll.String())
	}
	if all.Count != 2 {
		t.Errorf("default list count = %d, want 2 (drained node should be included)", all.Count)
	}

	// --active-only: bravo excluded.
	stdoutActive, restoreActive := captureOsStdoutComputeNodes(t)
	if code := cmdComputeNodesList([]string{"--active-only", "--json", "--break-glass-db"}); code != 0 {
		t.Fatalf("list --active-only = %d, want 0", code)
	}
	restoreActive()
	var active computeNodesListJSON
	if err := json.Unmarshal(stdoutActive.Bytes(), &active); err != nil {
		t.Fatalf("unmarshal active: %v (raw: %q)", err, stdoutActive.String())
	}
	if active.Count != 1 {
		t.Errorf("active-only count = %d, want 1 (drained node should be excluded)", active.Count)
	}
	if active.Nodes[0].Name != "alpha" {
		t.Errorf("active-only name = %q, want alpha", active.Nodes[0].Name)
	}
}

func TestComputeNodeReadsUseAuthenticatedAPI(t *testing.T) {
	role := "compute-only"
	live := 2
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		node := api.ComputeNodeOperatorResponse{
			ID: "11111111-1111-1111-1111-111111111111", Name: "alpha", Role: &role,
			TargetURL: "tcp://alpha.internal:50051", GatewayTargetURL: "tcp://alpha.internal:8080",
			VPCPUs: 8, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 3500,
			Active: true, LiveInstanceCount: &live,
		}
		switch r.URL.Path {
		case "/v1/compute-nodes":
			if r.URL.Query().Get("include_inactive") != "1" {
				t.Errorf("list query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, []api.ComputeNodeOperatorResponse{node})
		case "/v1/compute-nodes/alpha":
			writeTestJSON(w, http.StatusOK, node)
		case "/v1/compute-nodes/ghost":
			writeTestJSON(w, http.StatusNotFound, api.Problem{Status: http.StatusNotFound, Code: api.CodeNotFound})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")

	previousOpener := computeNodesStoreOpener
	computeNodesStoreOpener = func() (state.Store, func(), error) {
		t.Fatal("default read path opened the database")
		return nil, func() {}, nil
	}
	t.Cleanup(func() { computeNodesStoreOpener = previousOpener })

	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdComputeNodesList([]string{"--json"}); code != 0 {
		t.Fatalf("list exit = %d stderr=%s", code, stderr.String())
	}
	var listed computeNodesListJSON
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listed.Count != 1 || listed.Nodes[0].Name != "alpha" || listed.Nodes[0].Role == nil {
		t.Fatalf("list = %+v", listed)
	}

	out.Reset()
	stderr.Reset()
	if code := cmdComputeNodesShow([]string{"--node=alpha", "--json"}); code != 0 {
		t.Fatalf("show exit = %d stderr=%s", code, stderr.String())
	}
	var shown computeNodeShowJSON
	if err := json.Unmarshal(out.Bytes(), &shown); err != nil {
		t.Fatalf("decode show: %v", err)
	}
	if shown.Name != "alpha" || shown.LiveInstanceCount != 2 || shown.TargetURL == "" {
		t.Fatalf("show = %+v", shown)
	}

	out.Reset()
	stderr.Reset()
	if code := cmdComputeNodesShow([]string{"--node=ghost", "--json"}); code != 3 {
		t.Fatalf("missing show exit = %d stderr=%s", code, stderr.String())
	}
}

// TestCmdComputeNodesShow_Missing pins the not-found exit code:
// an unknown --node value must exit 3 (the "row not found"
// convention that state.Store.ComputeNodeByName surfaces), NOT
// exit 1 (which would conflate "node missing" with "DB
// unreachable" and confuse the upgrade orchestrator's loop).
func TestCmdComputeNodesShow_Missing(t *testing.T) {
	resetMemStore(t)
	code := cmdComputeNodesShow([]string{"--node=ghost", "--break-glass-db"})
	if code != 3 {
		t.Errorf("cmdComputeNodesShow(ghost) = %d, want 3", code)
	}
}

// TestCmdComputeNodesShow_JSON_HappyPath pins the per-node wire
// shape. --json on a freshly-registered node must surface {name,
// id, target_url, vpcpus, mem_mb, max_concurrency,
// admission_ceiling_mb, active, live_instance_count}. role /
// region / zone / release_id / cert_fingerprint / generation
// are nil on a fresh seed (omitempty hides them) — the wire
// shape distinguishes "field absent because not yet stamped"
// from "field absent because typo".
func TestCmdComputeNodesShow_JSON_HappyPath(t *testing.T) {
	resetMemStore(t)
	// Use a fresh name so the synthetic default-local row doesn't
	// shadow the assertion below.
	seedNode(t, "alpha", "unix:///run/faas/alpha.sock")

	stdout, restore := captureOsStdoutComputeNodes(t)
	code := cmdComputeNodesShow([]string{"--node=alpha", "--json", "--break-glass-db"})
	restore()
	if code != 0 {
		t.Fatalf("cmdComputeNodesShow(--json) = %d, want 0", code)
	}
	var out computeNodeShowJSON
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, stdout.String())
	}
	if out.Name != "alpha" {
		t.Errorf("name = %q, want alpha", out.Name)
	}
	if out.TargetURL != "unix:///run/faas/alpha.sock" {
		t.Errorf("target_url = %q, want unix:///run/faas/alpha.sock", out.TargetURL)
	}
	if out.LiveInstanceCount != 0 {
		t.Errorf("live_instance_count = %d, want 0 (fresh node has no instances)", out.LiveInstanceCount)
	}
	if !out.Active {
		t.Errorf("active = false, want true on fresh seed")
	}
	if out.Role != nil {
		t.Errorf("role = %v, want nil on fresh seed (omitempty)", *out.Role)
	}
	if out.CertFingerprint != nil {
		t.Errorf("cert_fingerprint = %v, want nil on fresh seed", *out.CertFingerprint)
	}
}

// TestCmdComputeNodesShow_JSON_LiveInstanceCount pins the load-bearing
// contract that --json reports the right live_instance_count for a
// node that actually has live instances. The naïve implementation
// (cmdComputeNodesShow passing *node to ListInstancesByNodeID, which
// expects the row's ID column) returns 0 because the lookup
// predicate is `apps.NodeID == nodeID` and nodeID is a UUID, not the
// user-supplied fqdn. Caught by /code-review medium on PR #1044.
func TestCmdComputeNodesShow_JSON_LiveInstanceCount(t *testing.T) {
	resetMemStore(t)
	seedNode(t, "alpha", "unix:///run/faas/alpha.sock")

	// Seed one app + two instances on alpha (one live, one parked)
	// so live_instance_count must be exactly 1.
	st := getSeededStore(t)
	nodeRow, err := st.ComputeNodeByName(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("ComputeNodeByName: %v", err)
	}
	app, err := st.CreateApp(context.Background(), state.App{
		AccountID: "test-acct",
		Slug:      "alpha-app",
		Type:      state.AppTypeFunction,
		Runtime:   "node22",
		RAMMB:     256,
		NodeID:    nodeRow.ID,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	deployment, err := st.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		BuildID:     "",
		ImageDigest: "img:tag",
		Kind:        state.DeploymentKindImage,
		SourcePath:  "",
		SourceBytes: 0,
		Handler:     "",
		LogPath:     "",
		SourceURL:   "",
		CommitSHA:   "",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if _, err := st.CreateInstance(context.Background(), app.ID, deployment.ID, "RUNNING", 256, nodeRow.ID, "wake-1"); err != nil {
		t.Fatalf("CreateInstance(running): %v", err)
	}
	if _, err := st.CreateInstance(context.Background(), app.ID, deployment.ID, "PARKED", 256, nodeRow.ID, "wake-2"); err != nil {
		t.Fatalf("CreateInstance(parked): %v", err)
	}

	stdout, restore := captureOsStdoutComputeNodes(t)
	code := cmdComputeNodesShow([]string{"--node=alpha", "--json", "--break-glass-db"})
	restore()
	if code != 0 {
		t.Fatalf("cmdComputeNodesShow(--json) = %d, want 0", code)
	}
	var out computeNodeShowJSON
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, stdout.String())
	}
	if out.LiveInstanceCount != 1 {
		t.Errorf("live_instance_count = %d, want 1 (one RUNNING + one PARKED; only RUNNING counts)", out.LiveInstanceCount)
	}
}

// seedNode registers one fresh compute node through the public
// cmdComputeNodesAdd path so the list / show tests exercise the
// same wire the operator would. The MemStore seam treats the
// upsert as the source of truth.
func seedNode(t *testing.T, name, targetURL string) {
	t.Helper()
	if code := cmdComputeNodesAdd([]string{
		"--name=" + name,
		"--target-url=" + targetURL,
		"--vpcpus=8",
		"--mem-mb=4096",
		"--max-concurrency=20",
		"--admission-ceiling-mb=3500",
		"--break-glass-db",
		"--yes",
		"--reason=test_node_enroll",
	}); code != 0 {
		t.Fatalf("seedNode(%s): add returned %d", name, code)
	}
}

// getSeededStore returns the MemStore wrapped by the current
// computeNodesStoreOpener. Used by TestComputeNodesList_ActiveOnly
// to call MarkComputeNodeInactive without re-wiring the seam.
func getSeededStore(t *testing.T) state.Store {
	t.Helper()
	st, _, err := computeNodesStoreOpener()
	if err != nil {
		t.Fatalf("open seeded store: %v", err)
	}
	return st
}

// _ pins the encoding/json import; the show / list tests use it.
var _ = json.Marshal

// captureOsStdoutComputeNodes swaps the package-level osStdout for
// a buffer so the show / list subcommands (which write via the
// osStdout package var, not os.Stdout) can be asserted without
// letting their JSON pollute the test runner's stdout. Returns
// a restore() closure that swaps osStdout back.
//
// Buffer type is commands_sign_keys_test.go's `buffer` (same
// package); both files import nothing extra because the type
// is defined here too.
func captureOsStdoutComputeNodes(t *testing.T) (*captureBuffer, func()) {
	t.Helper()
	old := osStdout
	buf := &captureBuffer{}
	osStdout = buf
	return buf, func() { osStdout = old }
}

// captureBuffer is the io.Writer returned by
// captureOsStdoutComputeNodes; mirrored here for self-containedness
// (commands_sign_keys_test.go has the same shape under the name
// `buffer`).
type captureBuffer struct {
	data []byte
}

func (b *captureBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *captureBuffer) Bytes() []byte  { return b.data }
func (b *captureBuffer) String() string { return string(b.data) }

// TestCmdComputeNodesDispatch_Routing pins the dispatcher routing
// for every sibling verb under `gregalectl compute-nodes <verb>`.
// Each subtest asserts the dispatcher forwards args[1:] to the
// correct leaf verb. The leaf verbs' happy paths hit Postgres
// (openComputeNodesStore), so the subtests only assert the
// exit-2 error-path that fires when --node is missing — that's
// the early exit that doesn't touch the store.
//
// The "no subcommand" and "unknown subcommand" branches are
// covered separately (they exit 2 with a diagnostic) below.
func TestCmdComputeNodesDispatch_Routing(t *testing.T) {
	cases := []struct {
		name    string
		verb    string
		wantErr string // expected stderr substring
	}{
		{name: "drain", verb: "drain", wantErr: "--node required"},
		{name: "drain_status", verb: "drain-status", wantErr: "--node required"},
		{name: "activate", verb: "activate", wantErr: "--node required"},
		{name: "force_drain", verb: "force-drain", wantErr: "--node required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stderr := captureStderrComputeNodes(t, func() {
				if code := cmdComputeNodesDispatch([]string{tc.verb}); code != 2 {
					t.Errorf("dispatch(%s) -> leaf exit %d, want 2 (--node required)", tc.verb, code)
				}
			})
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("dispatch(%s) stderr missing %q (got %q)", tc.verb, tc.wantErr, stderr)
			}
		})
	}
}

// TestCmdComputeNodesDispatch_NoSubcommand pins the missing-arg
// branch (commands_compute_nodes.go:57-60) — exit 2 with a
// diagnostic listing all known subcommands.
func TestCmdComputeNodesDispatch_NoSubcommand(t *testing.T) {
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesDispatch(nil); code != 2 {
			t.Errorf("dispatch(nil) = %d, want 2", code)
		}
	})
	for _, want := range []string{"missing subcommand", "add", "list", "show", "drain", "activate"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("dispatch(nil) stderr missing %q (got %q)", want, stderr)
		}
	}
}

// TestCmdComputeNodesDispatch_UnknownSubcommand pins the default
// branch of the dispatch switch (commands_compute_nodes.go:76-79).
func TestCmdComputeNodesDispatch_UnknownSubcommand(t *testing.T) {
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesDispatch([]string{"reboot"}); code != 2 {
			t.Errorf("dispatch(reboot) = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, `unknown subcommand "reboot"`) {
		t.Errorf("dispatch(reboot) stderr missing unknown-subcommand marker (got %q)", stderr)
	}
}

// TestCmdComputeNodesDrain_MissingNode pins the --node-required
// branch of cmdComputeNodesDrain (commands_compute_nodes.go:125-128)
// — exit 2 with a diagnostic, no Postgres touched.
func TestCmdComputeNodesDrain_MissingNode(t *testing.T) {
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesDrain(nil); code != 2 {
			t.Errorf("drain(nil) = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "--node required") {
		t.Errorf("drain(nil) stderr missing --node required (got %q)", stderr)
	}
}

// TestCmdComputeNodesDrainStatus_MissingNode pins the --node-required
// branch of cmdComputeNodesDrainStatus (commands_compute_nodes.go:160-163).
func TestCmdComputeNodesDrainStatus_MissingNode(t *testing.T) {
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesDrainStatus(nil); code != 2 {
			t.Errorf("drain-status(nil) = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "--node required") {
		t.Errorf("drain-status(nil) stderr missing --node required (got %q)", stderr)
	}
}

// TestCmdComputeNodesActivate_MissingNode pins the --node-required
// branch of cmdComputeNodesActivate (commands_compute_nodes.go:202-205).
func TestCmdComputeNodesActivate_MissingNode(t *testing.T) {
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesActivate(nil); code != 2 {
			t.Errorf("activate(nil) = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "--node required") {
		t.Errorf("activate(nil) stderr missing --node required (got %q)", stderr)
	}
}

// TestCmdComputeNodesActivate_ResolvesName verifies the production wire
// contract: the CLI accepts compute_nodes.name, while the state mutation is
// keyed by the row UUID returned by ComputeNodeByName.
func TestCmdComputeNodesActivate_ResolvesName(t *testing.T) {
	st := state.NewMemStore()
	setComputeNodesStoreOpener(func() (state.Store, func(), error) {
		return st, func() {}, nil
	})

	row, err := st.CreateComputeNode(context.Background(), state.ComputeNode{
		Name:               "fsn-2.faas",
		TargetURL:          "tcp://fsn-2.gregale.dev:50051",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
		Active:             false,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	if code := cmdComputeNodesActivate([]string{"--node", row.Name, "--break-glass-db", "--yes", "--reason", "test_repair"}); code != 0 {
		t.Fatalf("activate exit code = %d, want 0", code)
	}
	got, err := st.ComputeNodeByID(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("read node: %v", err)
	}
	if !got.Active {
		t.Fatal("activate left the node drained")
	}
}

// TestCmdComputeNodesForceDrain_RequiresYes pins the loud-warning
// branch (commands_compute_nodes.go:238-241) — exit 2 with a
// diagnostic that NAMES --yes so the operator can copy-paste.
// The --node check runs first, so a test that omits both --node
// and --yes should see the --node-required diagnostic (covered by
// TestCmdComputeNodesDrain_MissingNode above). Here we omit --yes
// but provide --node; the early exit fires on the --yes check.
func TestCmdComputeNodesForceDrain_RequiresYes(t *testing.T) {
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesForceDrain([]string{"--node=alpha"}); code != 2 {
			t.Errorf("force-drain(--node=alpha, no --yes) = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "--yes required") {
		t.Errorf("force-drain stderr missing --yes required (got %q)", stderr)
	}
}

// TestCmdComputeNodesForceDrain_MissingNode pins the --node-required
// branch (commands_compute_nodes.go:234-237) — runs BEFORE the
// --yes check so this fires first even when --yes is also missing.
func TestCmdComputeNodesForceDrain_MissingNode(t *testing.T) {
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdComputeNodesForceDrain(nil); code != 2 {
			t.Errorf("force-drain(nil) = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "--node required") {
		t.Errorf("force-drain(nil) stderr missing --node required (got %q)", stderr)
	}
}

// TestCmdComputeNodesForceDrain_InvalidFlag pins the flag.Parse
// error branch (commands_compute_nodes.go:231-233) — exit 2 via
// flag.ContinueOnError when an unknown flag is passed.
func TestCmdComputeNodesForceDrain_InvalidFlag(t *testing.T) {
	if code := cmdComputeNodesForceDrain([]string{"--not-a-flag"}); code != 2 {
		t.Errorf("force-drain(--not-a-flag) = %d, want 2", code)
	}
}
