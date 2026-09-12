package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

const validManifestYAML = `schema_version: "1.0.0"
fleet:
  hosts:
    - name: fsn-1
      role: control-plane
daemons:
  schedd:
    bind: tcp://0.0.0.0:7100
overlay:
  provider: wireguard
  cidr: 10.42.0.0/24
dns:
  apps_domain: apps.gregale.dev
  mode: cloudflare
private_dns:
  mode: managed_hosts
  zone: gregale.dev
postgresql:
  dsn: postgres://faas@127.0.0.1:5432/faas
  database: faas
  migration_max_slot: 10
  policy: on-boot
release:
  id: v1.4.0
  git_sha: abc1234567890abcdef1234567890abcdef12345
  architecture: x86_64
  firecracker_version: 1.10.0
  firecracker_digest: 0000000000000000000000000000000000000000000000000000000000000000
  kernel_digest: 0000000000000000000000000000000000000000000000000000000000000000
  builder_base_digest: 0000000000000000000000000000000000000000000000000000000000000000
  runtime_base_digest: 0000000000000000000000000000000000000000000000000000000000000000
storage:
  fast_root: /srv/fc
  spool_root: /var/spool/faas
  log_root: /var/log/faas
  run_dir: /run/faas
cgroups:
  slice: faas-cp.slice
  controllers: "memory,cpu,io,pids"
pki:
  root_dir: /etc/faas/tls
  ca_fingerprint: 0000000000000000000000000000000000000000000000000000000000000000
`

func writeSplitboxManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func TestCmdManifestValidate_Valid(t *testing.T) {
	p := writeSplitboxManifest(t, validManifestYAML)
	if code := cmdManifestValidate([]string{"--file", p}); code != 0 {
		t.Fatalf("cmdManifestValidate = %d, want 0", code)
	}
}

func TestCmdManifestValidate_MissingFile(t *testing.T) {
	if code := cmdManifestValidate([]string{"--file", "/nonexistent/path.yaml"}); code != 3 {
		t.Fatalf("cmdManifestValidate = %d, want 3", code)
	}
}

func TestCmdManifestValidate_MissingFlag(t *testing.T) {
	if code := cmdManifestValidate([]string{}); code != 1 {
		t.Fatalf("cmdManifestValidate = %d, want 1", code)
	}
}

func TestCmdManifestValidate_Invalid(t *testing.T) {
	// Two failures: missing memory controller + bad git_sha.
	bad := strings.Replace(validManifestYAML,
		`controllers: "memory,cpu,io,pids"`, `controllers: "cpu,io,pids"`, 1)
	bad = strings.Replace(bad,
		`git_sha: abc1234567890abcdef1234567890abcdef12345`,
		`git_sha: not-a-sha`, 1)
	p := writeSplitboxManifest(t, bad)
	if code := cmdManifestValidate([]string{"--file", p}); code != 1 {
		t.Fatalf("cmdManifestValidate = %d, want 1", code)
	}
}

func TestCmdManifestValidate_JSON(t *testing.T) {
	// Smoke test that the JSON output is valid JSON and carries
	// the right shape.
	p := writeSplitboxManifest(t, validManifestYAML)
	jsonOutput = true
	defer func() { jsonOutput = false }()

	// Capture stdout.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	code := cmdManifestValidate([]string{"--file", p})
	w.Close()
	if code != 0 {
		t.Fatalf("cmdManifestValidate = %d, want 0", code)
	}
	raw := make([]byte, 4096)
	n, _ := r.Read(raw)
	var report manifestReport
	if err := json.Unmarshal(raw[:n], &report); err != nil {
		t.Fatalf("JSON unmarshal: %v\nraw: %s", err, raw[:n])
	}
	if !report.Valid {
		t.Errorf("Valid = false, want true")
	}
	if report.Schema != "1.0.0" {
		t.Errorf("Schema = %q, want 1.0.0", report.Schema)
	}
	if len(report.Daemons) != 10 {
		t.Errorf("Daemons = %d, want 10", len(report.Daemons))
	}
}

func TestCmdManifestDispatch_Unknown(t *testing.T) {
	if code := cmdManifestDispatch([]string{"unknown"}); code != 1 {
		t.Fatalf("cmdManifestDispatch = %d, want 1", code)
	}
}

func TestCmdManifestDispatch_NoArgs(t *testing.T) {
	if code := cmdManifestDispatch([]string{}); code != 1 {
		t.Fatalf("cmdManifestDispatch = %d, want 1", code)
	}
}

func TestCmdManifestDispatch_Help(t *testing.T) {
	if code := cmdManifestDispatch([]string{"--help"}); code != 0 {
		t.Fatalf("cmdManifestDispatch = %d, want 0", code)
	}
	if code := cmdManifestDispatch([]string{"-h"}); code != 0 {
		t.Fatalf("cmdManifestDispatch(-h) = %d, want 0", code)
	}
}

func TestCmdManifestValidate_Help(t *testing.T) {
	if code := cmdManifestValidate([]string{"--help"}); code != 0 {
		t.Fatalf("cmdManifestValidate(--help) = %d, want 0", code)
	}
	if code := cmdManifestValidate([]string{"-h"}); code != 0 {
		t.Fatalf("cmdManifestValidate(-h) = %d, want 0", code)
	}
}

// PR-5 / issue #911 — TOML table-placement wiring pin.
//
// pkg/manifest.ValidateTOMLPlacement is the load-bearing check that
// the doctor (PR-4) will consume against the rendered /etc/faas/*.toml
// on each box. PR-5 wires a CLI-context pin so the validator is
// reachable from the operator surface — the doctor can call the same
// function directly, but a future `gregalectl manifest doctor --role=...`
// will run this check from this package. The test asserts the function
// surfaces the tombstone error for the canonical "tls_*_path leaked
// into [compute_node]" bug class from issue #911.
func TestTOMLPlacement_TombstoneReachableFromCLIPackage(t *testing.T) {
	// The canonical vmmd.toml render from issue #911: the operator
	// accidentally placed tls_cert_path under [compute_node] (the
	// big-endian duplicate that the production path picked up).
	rendered := map[string]string{
		"socket_path":                "/run/faas/vmmd.sock",
		"listen_addr":                "tcp://0.0.0.0:50051",
		"tls_cert_path":              "/etc/faas/tls/vmmd/server.crt",
		"tls_key_path":               "/etc/faas/tls/vmmd/server.key",
		"tls_ca_path":                "/etc/faas/tls/ca/ca.crt",
		"compute_node.name":          "fsn-2",
		"compute_node.target_url":    "tcp://fsn-1:7100",
		"compute_node.tls_cert_path": "/etc/faas/tls/vmmd/server.crt", // tombstone!
	}
	errs := manifest.ValidateTOMLPlacement("vmmd", rendered)
	if errs == nil {
		t.Fatal("ValidateTOMLPlacement = nil; want tombstone error for tls_cert_path under [compute_node]")
	}
	if !strings.Contains(errs.Error(), "tombstone") {
		t.Errorf("ValidateTOMLPlacement error = %q; want tombstone message", errs.Error())
	}
	if !strings.Contains(errs.Error(), "compute_node.tls_cert_path") {
		t.Errorf("ValidateTOMLPlacement error = %q; want compute_node.tls_cert_path in path", errs.Error())
	}
}

// PR-5 / issue #911 — TOML placement MUST also be reachable from a
// YAML manifest path for the operator CLI. Today the operator runs
// `gregalectl manifest validate --file=...` on the YAML; once the
// renderer (PR-2) ships, the same CLI dispatch chain will accept a
// --rendered-from flag and feed the produced map into
// ValidateTOMLPlacement. This test pins the contract that the
// validator is exported and callable from cmd/gregale.
func TestTOMLPlacement_HostKeysCatalogSize(t *testing.T) {
	// The doctor (PR-4) will iterate every daemon and assert its
	// rendered map is tombstone-free. Pin that the catalog hasn't
	// shrunk/grown without a matching schema update.
	keys := manifest.SortedHostKeys()
	if len(keys) != 10 {
		t.Errorf("SortedHostKeys len=%d; want 10 (catalog drift guard)", len(keys))
	}
}

// PR-2 / issue #911 — `gregalectl manifest render` CLI leaf. The
// renderer is the read-side of the release-bundle flow (PR-3 ships
// the write-side). The CLI leaf must reject missing flags, surface
// a non-zero exit on render errors, and emit a JSON-shaped
// RenderReport on success.

func TestCmdManifestRender_MissingManifestFile(t *testing.T) {
	if code := cmdManifestRender([]string{}); code != 1 {
		t.Fatalf("cmdManifestRender(no args) = %d, want 1", code)
	}
}

func TestCmdManifestRender_Help(t *testing.T) {
	if code := cmdManifestRender([]string{"--help"}); code != 0 {
		t.Fatalf("cmdManifestRender(--help) = %d, want 0", code)
	}
	if code := cmdManifestRender([]string{"-h"}); code != 0 {
		t.Fatalf("cmdManifestRender(-h) = %d, want 0", code)
	}
}

func TestCmdManifestRender_BadManifestPath(t *testing.T) {
	if code := cmdManifestRender([]string{"--manifest-file", "/no/such/file.yaml", "--dry-run"}); code != 1 {
		t.Fatalf("cmdManifestRender(bad path) = %d, want 1", code)
	}
}

func TestCmdManifestRender_DispatchUnknownSubcommand(t *testing.T) {
	if code := cmdManifestDispatch([]string{"render"}); code == 0 {
		// With no flags, render expects --manifest-file; any
		// non-zero exit means the dispatch wired correctly.
		// The test is a regression pin: a future refactor that
		// accidentally drops the render arm must flip this to 0.
		t.Fatalf("cmdManifestDispatch(render) = 0, want non-zero (render without flags should fail)")
	}
}

func TestCmdManifestRender_DryRunSucceeds(t *testing.T) {
	// Smoke test: dry-run against the canonical manifest produces
	// a RenderReport and exits 0. The renderer never touches the
	// filesystem in dry-run mode.
	dir := t.TempDir()
	manifestPath := writeSplitboxManifest(t, validManifestYAML)
	if code := cmdManifestRender([]string{
		"--manifest-file", manifestPath,
		"--releases-root", filepath.Join(dir, "releases"),
		"--etc-faas-dir", filepath.Join(dir, "etc"),
		"--systemd-dir", filepath.Join(dir, "systemd"),
		"--pki-root-dir", filepath.Join(dir, "tls"),
		"--cgroup-root", filepath.Join(dir, "cgroup"),
		"--dry-run",
	}); code != 0 {
		t.Fatalf("cmdManifestRender(dry-run) = %d, want 0", code)
	}
	// Dry-run must not write any file.
	for _, sub := range []string{"etc", "systemd", "tls", "cgroup", "releases"} {
		full := filepath.Join(dir, sub)
		entries, _ := os.ReadDir(full)
		if len(entries) > 0 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("dry-run leaked entries under %s: %v", full, names)
		}
	}
}

func TestCmdManifestRender_BadHost(t *testing.T) {
	manifestPath := writeSplitboxManifest(t, validManifestYAML)
	if code := cmdManifestRender([]string{
		"--manifest-file", manifestPath,
		"--host", "no-such-host",
		"--dry-run",
	}); code != 1 {
		t.Fatalf("cmdManifestRender(bad host) = %d, want 1", code)
	}
}

// TestCmdManifestRender_JSONShape pins the wire shape: dry-run +
// --json + a valid manifest → exit 0 + a JSON document with the
// canonical RenderReport fields (host, manifest_hash, outputs).
func TestCmdManifestRender_JSONShape(t *testing.T) {
	manifestPath := writeSplitboxManifest(t, validManifestYAML)
	jsonOutput = true
	defer func() { jsonOutput = false }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	code := cmdManifestRender([]string{"--manifest-file", manifestPath, "--dry-run"})
	w.Close()
	if code != 0 {
		t.Fatalf("cmdManifestRender = %d, want 0", code)
	}
	raw := make([]byte, 8192)
	n, _ := r.Read(raw)
	var report struct {
		Host         string `json:"host"`
		ManifestHash string `json:"manifest_hash"`
		Outputs      []struct {
			Path   string `json:"path"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
			Action string `json:"action"`
		} `json:"outputs"`
		Skipped bool `json:"skipped"`
	}
	if err := json.Unmarshal(raw[:n], &report); err != nil {
		t.Fatalf("JSON unmarshal: %v\nraw: %s", err, raw[:n])
	}
	if report.Host != "fsn-1" {
		t.Errorf("Host = %q, want fsn-1", report.Host)
	}
	if len(report.ManifestHash) != 64 {
		t.Errorf("ManifestHash len = %d, want 64", len(report.ManifestHash))
	}
	if len(report.Outputs) == 0 {
		t.Errorf("Outputs is empty; expected rendered systemd + TOML + slice units")
	}
	for _, o := range report.Outputs {
		if o.Action != "skipped" {
			t.Errorf("output %s.Action = %q, want skipped (dry-run)", o.Path, o.Action)
		}
	}
}

// TestCmdManifestRender_IdempotentRun pins the second-run short-circuit:
// run twice with the same opts → first run writes, second run reports
// Skipped=true and every output Action=unchanged.
func TestCmdManifestRender_IdempotentRun(t *testing.T) {
	manifestPath := writeSplitboxManifest(t, validManifestYAML)
	dir := t.TempDir()
	args := []string{
		"--manifest-file", manifestPath,
		"--releases-root", filepath.Join(dir, "releases"),
		"--etc-faas-dir", filepath.Join(dir, "etc"),
		"--systemd-dir", filepath.Join(dir, "systemd"),
		"--pki-root-dir", filepath.Join(dir, "tls"),
		"--cgroup-root", filepath.Join(dir, "cgroup"),
	}
	// First run.
	if code := cmdManifestRender(args); code != 0 {
		t.Fatalf("first render = %d, want 0", code)
	}
	// Second run.
	jsonOutput = true
	defer func() { jsonOutput = false }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()
	code := cmdManifestRender(args)
	w.Close()
	if code != 0 {
		t.Fatalf("second render = %d, want 0", code)
	}
	raw := make([]byte, 16384)
	n, _ := r.Read(raw)
	var report struct {
		Skipped bool `json:"skipped"`
		Outputs []struct {
			Action string `json:"action"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw[:n], &report); err != nil {
		t.Fatalf("JSON unmarshal: %v\nraw: %s", err, raw[:n])
	}
	if !report.Skipped {
		t.Errorf("second run Skipped = false, want true (idempotent)")
	}
	for _, o := range report.Outputs {
		if o.Action != "unchanged" {
			t.Errorf("output Action = %q, want unchanged", o.Action)
		}
	}
}
