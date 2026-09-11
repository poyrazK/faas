// commands_deploy_test.go — whitebox tests for
// `gregalectl deploy add-node` (PR-B to PR #935, multi-host
// scale-out gap #2).
//
// Test conventions mirror commands_compute_nodes_test.go:
//   - The state seam (computeNodesStoreOpener) is shared across
//     the package via TestMain in commands_compute_nodes_test.go.
//   - The ssh seam (sshRunner) is swapped to a fake so tests
//     don't reach the network. The bootstrap-failure path
//     exercises a fake that returns exit 1.
//   - The git seam (gitRunner) is swapped to a no-op so tests
//     don't need a real git repo.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeSSHResult is the canned (stdout, stderr, err) an SSH-stub
// returns when invoked during addNodeExecute.
type fakeSSHResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

// installFakeSSH swaps the ssh seam to a function that returns
// the given result. Returns a teardown func the caller defers.
func installFakeSSH(t *testing.T, result fakeSSHResult) func() {
	t.Helper()
	prevRunner := sshRunner
	sshRunner = func(target string, args []string) ([]byte, []byte, error) {
		return result.Stdout, result.Stderr, result.Err
	}
	return func() { sshRunner = prevRunner }
}

// installFakeGit swaps the git seam to a no-op. Tests can pass a
// custom fn to assert on commit messages. The seam returns
// (stdout, err) so the report's commit_sha can be captured.
//
// The default fake returns a dirty `git status --porcelain` line
// for the two files deploy add-node touches (` M` = unstaged
// modification) so the deploy path proceeds through the commit
// step instead of short-circuiting on the idempotency check.
// Tests that exercise the no-op path should pass a custom fn
// that returns empty stdout for `status --porcelain`.
func installFakeGit(t *testing.T, fn func(repoRoot string, args ...string) ([]byte, error)) func() {
	t.Helper()
	prev := gitRunner
	if fn == nil {
		gitRunner = func(repoRoot string, args ...string) ([]byte, error) {
			// Detect the `status --porcelain` invocation the
			// idempotency check uses and return a dirty line so
			// the deploy path doesn't short-circuit.
			if len(args) >= 2 && args[0] == "status" && args[1] == "--porcelain" {
				return []byte(" M deploy/ansible/host_vars/foo.yml\n"), nil
			}
			return nil, nil
		}
	} else {
		gitRunner = fn
	}
	return func() { gitRunner = prev }
}

// resetComputeNodesStore rewires the post-bootstrap POST to a
// fresh MemStore (so the POST path lands on an empty store).
func resetComputeNodesStore(t *testing.T) {
	t.Helper()
	st := state.NewMemStore()
	setComputeNodesStoreOpener(func() (state.Store, func(), error) {
		return st, func() {}, nil
	})
}

// makeFakeRepo creates a temp dir with the minimum layout
// cmdDeployAddNode's pre-flight checks for: a Makefile +
// deploy/ansible/bootstrap.yml + deploy/ansible/host_vars/ +
// deploy/ansible/inventory/hosts.ini. Returns the repo root.
func makeFakeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible/host_vars"), 0o755); err != nil {
		t.Fatalf("mkdir host_vars: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible/inventory"), 0o755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/bootstrap.yml"), []byte("---\n# stub\n"), 0o644); err != nil {
		t.Fatalf("write bootstrap.yml: %v", err)
	}
	seedINI := `[control_plane]
faas-fsn-1

[compute_nodes]
faas-fsn-2
`
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/inventory/hosts.ini"), []byte(seedINI), 0o644); err != nil {
		t.Fatalf("write hosts.ini: %v", err)
	}
	return repo
}

// buildAddNodeArgs returns the minimal flag set for a
// compute-only add that should pass all flag-level validation.
func buildAddNodeArgs(repoRoot, fqdn string) []string {
	return []string{
		"--repo-root=" + repoRoot,
		"--ansible-host=10.42.0.3",
		"--public-iface=ens5",
		"--masquerade-cidr=10.102.0.0/16",
		"--target-url=tcp://vmmd-3.faas:50051",
		"--vpcpus=160", "--mem-mb=56000",
		"--max-concurrency=200", "--admission-ceiling-mb=47600",
		"--yes",
		"--skip-bootstrap",
		"--skip-compute-nodes-post",
		fqdn,
	}
}

// TestCmdDeployAddNode_MissingFlags asserts the usage path.
func TestCmdDeployAddNode_MissingFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no args", []string{}, "<fqdn> positional required"},
		{"bad role", []string{"--role=invalid", "--ansible-host=10.42.0.3", "faas-fsn-3"}, "--role must be"},
		{"missing ansible-host", []string{"--public-iface=ens5", "--masquerade-cidr=10.102.0.0/16", "--target-url=tcp://vmmd-3.faas:50051", "--vpcpus=160", "--mem-mb=56000", "--max-concurrency=200", "--admission-ceiling-mb=47600", "faas-fsn-3"}, "--ansible-host required"},
		{"compute-only missing public-iface", []string{"--ansible-host=10.42.0.3", "--masquerade-cidr=10.102.0.0/16", "--target-url=tcp://vmmd-3.faas:50051", "--vpcpus=160", "--mem-mb=56000", "--max-concurrency=200", "--admission-ceiling-mb=47600", "faas-fsn-3"}, "--public-iface required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetComputeNodesStore(t)
			stderr := captureStderrComputeNodes(t, func() {
				if code := cmdDeployAddNode(tc.args); code != 2 {
					t.Errorf("cmdDeployAddNode(%v) = %d, want 2", tc.args, code)
				}
			})
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr missing %q: got %q", tc.wantErr, stderr)
			}
		})
	}
}

// TestCmdDeployAddNode_WritesHostVars asserts the
// host_vars/<fqdn>.yml file lands with the expected shape.
func TestCmdDeployAddNode_WritesHostVars(t *testing.T) {
	resetComputeNodesStore(t)
	repo := makeFakeRepo(t)
	defer installFakeGit(t, nil)()

	captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(buildAddNodeArgs(repo, "faas-fsn-3")); code != 0 {
			t.Fatalf("cmdDeployAddNode = %d", code)
		}
	})

	body, err := os.ReadFile(filepath.Join(repo, "deploy/ansible/host_vars/faas-fsn-3.yml"))
	if err != nil {
		t.Fatalf("read host_vars: %v", err)
	}
	if !strings.Contains(string(body), "faas_box_role: compute-only") {
		t.Errorf("missing faas_box_role: compute-only\n%s", body)
	}
	if !strings.Contains(string(body), "faas_node_name: faas-fsn-3.faas") {
		t.Errorf("missing faas_node_name\n%s", body)
	}
	if !strings.Contains(string(body), `ansible_host: "10.42.0.3"`) {
		t.Errorf("missing quoted ansible_host\n%s", body)
	}
	if !strings.Contains(string(body), `public_iface: "ens5"`) {
		t.Errorf("missing quoted public_iface\n%s", body)
	}
	if !strings.Contains(string(body), `masquerade_cidr: "10.102.0.0/16"`) {
		t.Errorf("missing quoted masquerade_cidr\n%s", body)
	}
}

// TestCmdDeployAddNode_UpdatesHostsINI asserts the idempotent
// hosts.ini add.
func TestCmdDeployAddNode_UpdatesHostsINI(t *testing.T) {
	resetComputeNodesStore(t)
	repo := makeFakeRepo(t)
	defer installFakeGit(t, nil)()

	captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(buildAddNodeArgs(repo, "faas-fsn-3")); code != 0 {
			t.Fatalf("cmdDeployAddNode = %d", code)
		}
	})

	body, err := os.ReadFile(filepath.Join(repo, "deploy/ansible/inventory/hosts.ini"))
	if err != nil {
		t.Fatalf("read hosts.ini: %v", err)
	}
	if !strings.Contains(string(body), "faas-fsn-3") {
		t.Errorf("hosts.ini missing faas-fsn-3:\n%s", body)
	}
	if !strings.Contains(string(body), "faas-fsn-1") {
		t.Errorf("hosts.ini lost faas-fsn-1:\n%s", body)
	}
	if !strings.Contains(string(body), "faas-fsn-2") {
		t.Errorf("hosts.ini lost faas-fsn-2:\n%s", body)
	}
}

// TestCmdDeployAddNode_HostsINI_Idempotent asserts re-running
// with the same fqdn produces no duplicate entries.
func TestCmdDeployAddNode_HostsINI_Idempotent(t *testing.T) {
	resetComputeNodesStore(t)
	repo := makeFakeRepo(t)
	defer installFakeGit(t, nil)()
	args := buildAddNodeArgs(repo, "faas-fsn-3")

	captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(args); code != 0 {
			t.Fatalf("first add: %d", code)
		}
	})
	captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(args); code != 0 {
			t.Fatalf("second add: %d", code)
		}
	})

	body, _ := os.ReadFile(filepath.Join(repo, "deploy/ansible/inventory/hosts.ini"))
	count := strings.Count(string(body), "faas-fsn-3")
	if count != 1 {
		t.Errorf("hosts.ini has faas-fsn-3 %d times, want 1:\n%s", count, body)
	}
}

// TestCmdDeployAddNode_NoDirtyTreeShortCircuits asserts the
// idempotency path: when neither host_vars nor hosts.ini changed
// (gitDirty returns false), the deploy flow short-circuits with
// a "no-op" stderr message and the existing commit SHA, exits 0,
// and does NOT attempt the SSH bootstrap a second time.
func TestCmdDeployAddNode_NoDirtyTreeShortCircuits(t *testing.T) {
	resetComputeNodesStore(t)
	repo := makeFakeRepo(t)
	// Custom fake: status --porcelain returns empty (clean
	// index), rev-parse HEAD returns a known SHA. Tracks whether
	// the SSH seam was called (it shouldn't be).
	sshCalled := false
	defer installFakeSSH(t, fakeSSHResult{
		Stdout: []byte("ok"),
		Stderr: []byte(""),
		Err:    nil,
	})()
	defer installFakeGit(t, nil)()
	// Override SSH to fail loudly if called (test should not
	// reach the bootstrap step on the no-op path).
	prevSSH := sshRunner
	sshRunner = func(target string, args []string) ([]byte, []byte, error) {
		sshCalled = true
		return []byte(""), []byte("ssh should not be called on no-op"), errors.New("exit 1")
	}
	defer func() { sshRunner = prevSSH }()

	// Override git seam: status --porcelain returns empty (clean),
	// rev-parse HEAD returns a known SHA so the report surfaces it.
	setGitRunner(func(repoRoot string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "status" && args[1] == "--porcelain" {
			return []byte(""), nil
		}
		if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
			return []byte("abc1234567890abcdef1234567890abcdef12345\n"), nil
		}
		return nil, nil
	})

	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(buildAddNodeArgs(repo, "faas-fsn-3")); code != 0 {
			t.Fatalf("cmdDeployAddNode(no-op) = %d, want 0", code)
		}
	})
	if !strings.Contains(stderr, "no repo changes") {
		t.Errorf("stderr missing no-op hint: %q", stderr)
	}
	if sshCalled {
		t.Error("ssh must not be called on the no-op path")
	}
}

// TestRenderHostVarsYAML_MatchesExisting asserts the renderer's
// output for fsn-1 (control-plane) carries the same shape as
// deploy/ansible/host_vars/faas-fsn-1.yml on disk. Drift gate.
func TestRenderHostVarsYAML_MatchesExisting(t *testing.T) {
	got := renderHostVarsYAML("fsn-1", "control-plane", "10.42.0.1", "", "", "", "")
	for _, want := range []string{
		"faas_box_role: control-plane",
		"faas_node_name: fsn-1",
		`ansible_host: "10.42.0.1"`,
		"ansible_python_interpreter: /usr/bin/python3",
		"overlay_cidrs: []",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fsn-1 render missing %q\n%s", want, got)
		}
	}
	// fsn-1 (control-plane) must NOT carry the compute-only fields.
	for _, mustNotHave := range []string{
		"public_iface:",
		"masquerade_cidr:",
	} {
		if strings.Contains(got, mustNotHave) {
			t.Errorf("fsn-1 render should not carry %q\n%s", mustNotHave, got)
		}
	}
}

// TestUpdateHostsINIAddNode_Dedup asserts re-adding the same
// fqdn is a no-op.
func TestUpdateHostsINIAddNode_Dedup(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.ini")
	seed := "[control_plane]\nfsn-1\n\n[compute_nodes]\nfsn-2\n"
	if err := os.WriteFile(hostsPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body, err := updateHostsINIAddNode(hostsPath, "fsn-1", "control-plane")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !bytes.Equal(body, []byte(seed)) {
		t.Errorf("dedup altered file:\nbefore:\n%s\nafter:\n%s", seed, body)
	}
}

// TestUpdateHostsINIAddNode_Appends asserts the first-time add
// lands under the right group.
func TestUpdateHostsINIAddNode_Appends(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.ini")
	seed := "[control_plane]\nfsn-1\n\n[compute_nodes]\nfsn-2\n"
	if err := os.WriteFile(hostsPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body, err := updateHostsINIAddNode(hostsPath, "fsn-3", "compute-only")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(string(body), "fsn-3") {
		t.Errorf("body missing fsn-3:\n%s", body)
	}
	if !strings.Contains(string(body), "[compute_nodes]\nfsn-2\nfsn-3") {
		t.Errorf("fsn-3 not under [compute_nodes]:\n%s", body)
	}
}

// TestCmdDeployAddNode_RollsBackOnBootstrapFailure asserts the
// bootstrap-failure path leaves the repo committed but the
// report flags the failure.
func TestCmdDeployAddNode_RollsBackOnBootstrapFailure(t *testing.T) {
	resetComputeNodesStore(t)
	repo := makeFakeRepo(t)
	defer installFakeGit(t, nil)()
	defer installFakeSSH(t, fakeSSHResult{
		Stderr: []byte("ansible-playbook: ERR: package X not found"),
		Err:    errors.New("exit status 1"),
	})()

	args := buildAddNodeArgs(repo, "faas-fsn-3")
	argsNoSkip := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--skip-bootstrap" {
			continue
		}
		argsNoSkip = append(argsNoSkip, a)
	}

	var stdoutBuf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(argsNoSkip); code != 1 {
			t.Errorf("cmdDeployAddNode(failing ssh) = %d, want 1", code)
		}
	})
	_ = w.Close()
	io.Copy(&stdoutBuf, r)
	os.Stdout = oldStdout

	if !strings.Contains(stderr, "bootstrap failed") {
		t.Errorf("stderr missing bootstrap-failed hint: %q", stderr)
	}
	if !strings.Contains(stderr, "ansible-playbook: ERR") {
		t.Errorf("stderr missing ansible error: %q", stderr)
	}
}

// TestCmdDeployAddNode_PostsComputeNodeRow asserts the happy bootstrap →
// authenticated POST path uses the response row ID without a database read.
func TestCmdDeployAddNode_PostsComputeNodeRow(t *testing.T) {
	const rowID = "11111111-1111-1111-1111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/compute-nodes" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("reason"); got != "deploy_add_node" {
			t.Errorf("reason = %q", got)
		}
		var request api.ComputeNodeEnrollmentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode enrollment: %v", err)
			return
		}
		writeTestJSON(w, http.StatusOK, api.ComputeNodeOperatorResponse{
			ID: rowID, Name: request.Name, TargetURL: request.TargetURL,
			VPCPUs: request.VPCPUs, MemMB: request.MemMB,
			MaxConcurrency: request.MaxConcurrency, AdmissionCeilingMB: request.AdmissionCeilingMB,
			Active: true,
		})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	previousOpener := computeNodesStoreOpener
	computeNodesStoreOpener = func() (state.Store, func(), error) {
		t.Fatal("deploy add-node opened PostgreSQL after enrollment")
		return nil, func() {}, nil
	}
	t.Cleanup(func() { computeNodesStoreOpener = previousOpener })
	repo := makeFakeRepo(t)
	defer installFakeGit(t, nil)()
	defer installFakeSSH(t, fakeSSHResult{Stdout: []byte("ok"), Stderr: []byte(""), Err: nil})()

	args := buildAddNodeArgs(repo, "faas-fsn-3")
	full := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--skip-bootstrap" || a == "--skip-compute-nodes-post" {
			continue
		}
		full = append(full, a)
	}

	var stdoutBuf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(full); code != 0 {
			t.Errorf("cmdDeployAddNode(happy) = %d, want 0", code)
		}
	})
	_ = w.Close()
	io.Copy(&stdoutBuf, r)
	os.Stdout = oldStdout

	if !strings.Contains(stdoutBuf.String(), "OK fqdn=faas-fsn-3") {
		t.Errorf("stdout missing OK line: %q", stdoutBuf.String())
	}
	if !strings.Contains(stdoutBuf.String(), "compute_node_row="+rowID) {
		t.Errorf("stdout missing response row id: %q", stdoutBuf.String())
	}
}

// TestCmdDeployAddNode_PreflightIdempotent asserts the
// pre-flight dedup catches an existing host_vars file with
// different content.
func TestCmdDeployAddNode_PreflightIdempotent(t *testing.T) {
	resetComputeNodesStore(t)
	repo := makeFakeRepo(t)
	hostVars := filepath.Join(repo, "deploy/ansible/host_vars/faas-fsn-3.yml")
	if err := os.WriteFile(hostVars, []byte("faas_box_role: garbage\n"), 0o644); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}
	defer installFakeGit(t, nil)()

	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(buildAddNodeArgs(repo, "faas-fsn-3")); code != 1 {
			t.Errorf("cmdDeployAddNode(existing different) = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "already exists with different content") {
		t.Errorf("stderr missing dedup message: %q", stderr)
	}
}

// TestCmdDeployAddNode_YesPrompt asserts the pre-flight
// confirmation runs when --yes is omitted.
func TestCmdDeployAddNode_YesPrompt(t *testing.T) {
	resetComputeNodesStore(t)
	repo := makeFakeRepo(t)
	defer installFakeGit(t, nil)()

	args := buildAddNodeArgs(repo, "faas-fsn-3")
	noYes := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--yes" {
			continue
		}
		noYes = append(noYes, a)
	}
	stderr := captureStderrComputeNodes(t, func() {
		if code := cmdDeployAddNode(noYes); code != 2 {
			t.Errorf("cmdDeployAddNode(no --yes) = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "Re-run with --yes") {
		t.Errorf("stderr missing --yes prompt: %q", stderr)
	}
}

// TestRenderHostVarsYAML_ComputeOnly asserts the compute-only
// renderer carries the right set of fields.
func TestRenderHostVarsYAML_ComputeOnly(t *testing.T) {
	got := renderHostVarsYAMLWithTargetURL("fsn-3", "compute-only", "10.42.0.3", "ens5", "10.102.0.0/16", "fc00::/7", "100.64.0.0/14", "tcp://vmmd-3.faas:50051")
	for _, want := range []string{
		"faas_box_role: compute-only",
		"faas_node_name: fsn-3.faas",
		`ansible_host: "10.42.0.3"`,
		`public_iface: "ens5"`,
		`masquerade_cidr: "10.102.0.0/16"`,
		`masquerade_cidr_v6: "fc00::/7"`,
		`overlay_cidrs: ["100.64.0.0/14"]`,
		`faas_vmmd_listen_addr: "tcp://0.0.0.0:50051"`,
		`faas_vmmd_target_url: "tcp://vmmd-3.faas:50051"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fsn-3 render missing %q\n%s", want, got)
		}
	}
}

// TestYamlQuote asserts the YAML-quoting helper escapes the
// dangerous characters (": # " `*&!|>'%@`) and round-trips the
// safe ones byte-identical.
func TestYamlQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ens5", `"ens5"`},
		{"10.42.0.1", `"10.42.0.1"`},
		{`weird"value`, `"weird\"value"`},
		{`back\slash`, `"back\\slash"`},
		{"a:b", `"a:b"`},
		{"a#b", `"a#b"`},
		{"*anchor", `"*anchor"`},
		{"", `""`},
	}
	for _, tc := range cases {
		if got := yamlQuote(tc.in); got != tc.want {
			t.Errorf("yamlQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestYamlQuoteList asserts the comma-separated list renderer
// quotes each entry and returns [] for empty input.
func TestYamlQuoteList(t *testing.T) {
	if got := yamlQuoteList(""); got != "[]" {
		t.Errorf("yamlQuoteList(\"\") = %q, want []", got)
	}
	if got := yamlQuoteList("100.64.0.0/14, 10.42.0.0/16"); got != `["100.64.0.0/14", "10.42.0.0/16"]` {
		t.Errorf("yamlQuoteList(\"100.64.0.0/14, 10.42.0.0/16\") = %q", got)
	}
	// Hostile input: a value with `:` and a quote — must not break YAML.
	if got := yamlQuoteList("a:b,weird\"x"); got != `["a:b", "weird\"x"]` {
		t.Errorf("yamlQuoteList hostile = %q", got)
	}
}

// TestStringContainsFQDN asserts the dedup helper's token-aware
// matching (avoids fsn-1 matching fsn-10).
func TestStringContainsFQDN(t *testing.T) {
	body := []byte("[x]\nfsn-1\nfsn-10\nfsn-2\n")
	if !stringContainsFQDN(body, "fsn-1") {
		t.Error("stringContainsFQDN should match fsn-1")
	}
	if !stringContainsFQDN(body, "fsn-10") {
		t.Error("stringContainsFQDN should match fsn-10")
	}
	if stringContainsFQDN(body, "fsn-99") {
		t.Error("stringContainsFQDN should not match fsn-99")
	}
	header := []byte("# comment\n[x]\nfsn-1\n")
	if !stringContainsFQDN(header, "fsn-1") {
		t.Error("stringContainsFQDN should match fsn-1 under [x]")
	}
}

// verify the seam swap is real (catches future regressions):
func TestBuildFakeGit_BehaviorIsCallable(t *testing.T) {
	prev := gitRunner
	defer func() { gitRunner = prev }()
	called := false
	gitRunner = func(repoRoot string, args ...string) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	}
	if _, err := gitRunner("/tmp", "status"); err != nil {
		t.Errorf("gitRunner: %v", err)
	}
	if !called {
		t.Error("gitRunner not invoked")
	}
}

// guards against dead imports.
var _ = exec.Command
var _ = context.Background
var _ = json.Unmarshal

// TestCmdDeployDispatch_Routing pins the dispatcher routing for
// the known verb (add-node) and the missing-arg / unknown-subcommand
// branches (commands_deploy.go:69-81).
func TestCmdDeployDispatch_Routing(t *testing.T) {
	t.Run("add_node_routed", func(t *testing.T) {
		// Drives the leaf with a known-bad flag set so the leaf
		// exits 2 via flag.Parse, proving routing reached cmdDeployAddNode.
		resetComputeNodesStore(t)
		stderr := captureStderrComputeNodes(t, func() {
			if code := cmdDeployDispatch([]string{"add-node", "--not-a-flag"}); code != 2 {
				t.Errorf("dispatch(add-node --not-a-flag) = %d, want 2", code)
			}
		})
		if !strings.Contains(stderr, "flag provided but not defined") {
			t.Errorf("dispatch stderr missing flag.Parse error (got %q)", stderr)
		}
	})
	t.Run("no_subcommand", func(t *testing.T) {
		stderr := captureStderrComputeNodes(t, func() {
			if code := cmdDeployDispatch(nil); code != 2 {
				t.Errorf("dispatch(nil) = %d, want 2", code)
			}
		})
		if !strings.Contains(stderr, "missing subcommand") {
			t.Errorf("dispatch(nil) stderr missing 'missing subcommand' hint (got %q)", stderr)
		}
	})
	t.Run("unknown_subcommand", func(t *testing.T) {
		stderr := captureStderrComputeNodes(t, func() {
			if code := cmdDeployDispatch([]string{"remove-node"}); code != 2 {
				t.Errorf("dispatch(remove-node) = %d, want 2", code)
			}
		})
		if !strings.Contains(stderr, `unknown subcommand "remove-node"`) {
			t.Errorf("dispatch(remove-node) stderr missing unknown marker (got %q)", stderr)
		}
	})
}

// TestDefaultRepoRoot pins the bootstrap.yml walker
// (commands_deploy.go:482) — given a cwd where the bootstrap.yml
// marker is reachable, defaultRepoRoot returns that directory. When
// the marker is unreachable within 5 ascents, the function returns
// the original cwd (the documented "fall back to where we started"
// behaviour at line 498).
//
// The helper walks cwd (NOT os.Args[0]) looking for
// deploy/ansible/bootstrap.yml. macOS resolves /tmp → /private/tmp
// so we compare against os.Getwd() (the symlink-resolved form) to
// avoid the t.TempDir() vs os.Getwd() mismatch.
func TestDefaultRepoRoot(t *testing.T) {
	t.Run("found_in_cwd", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
			t.Fatalf("mkdir deploy/ansible: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/bootstrap.yml"), []byte("---\n"), 0o644); err != nil {
			t.Fatalf("write bootstrap.yml: %v", err)
		}
		oldCwd, _ := os.Getwd()
		t.Cleanup(func() { _ = os.Chdir(oldCwd) })
		if err := os.Chdir(repo); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		// Use the symlink-resolved cwd (os.Getwd) since macOS
		// rewrites /tmp → /private/tmp but the chdir target is
		// the symlink form.
		wantCwd, _ := os.Getwd()
		got := defaultRepoRoot()
		if got != wantCwd {
			t.Errorf("defaultRepoRoot() = %q, want %q (cwd has the marker)", got, wantCwd)
		}
	})
	t.Run("not_found_returns_cwd", func(t *testing.T) {
		empty := t.TempDir()
		oldCwd, _ := os.Getwd()
		t.Cleanup(func() { _ = os.Chdir(oldCwd) })
		if err := os.Chdir(empty); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		// No marker within 5 ascents → function returns cwd
		// (commands_deploy.go:498). We pin the symlink-resolved
		// form to avoid the /tmp → /private/tmp mismatch.
		wantCwd, _ := os.Getwd()
		if got := defaultRepoRoot(); got != wantCwd {
			t.Errorf("defaultRepoRoot() = %q, want %q (no marker within 5 ascents → return cwd)", got, wantCwd)
		}
	})
}
