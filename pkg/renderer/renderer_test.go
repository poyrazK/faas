package renderer

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/manifest"
)

// fixtureManifest writes a minimal valid manifest YAML to a temp
// file and returns the path. The host role is `hostRole`. The
// per-daemon Daemons block is intentionally minimal — the renderer
// tolerates a missing block (Audit entry) so the test surface
// stays focused on the host-resolution / validate / short-circuit
// logic.
func fixtureManifest(t *testing.T, hostName, hostRole string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	manifestYAML := `schema_version: "1.0.0"
fleet:
  hosts:
    - name: ` + hostName + `
      role: ` + hostRole + `
daemons:
  schedd:
    bind: unix:///run/faas/schedd.sock
  apid:
    bind: unix:///run/faas/apid.sock
overlay:
  provider: tailscale
  cidr: 10.42.0.0/24
dns:
  apps_domain: apps.gregale.dev
  mode: cloudflare
postgresql:
  dsn: postgres://localhost/faas
  database: faas
  migration_max_slot: 1
  policy: on-boot
release:
  id: v1.0.0
  git_sha: 0123456789abcdef0123456789abcdef01234567
  architecture: x86_64
  firecracker_version: "1.10.0"
  firecracker_digest: ` + strings.Repeat("a", 64) + `
  kernel_digest: ` + strings.Repeat("b", 64) + `
  builder_base_digest: ` + strings.Repeat("c", 64) + `
  runtime_base_digest: ` + strings.Repeat("d", 64) + `
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
  ca_fingerprint: ` + strings.Repeat("e", 64) + `
`
	if err := os.WriteFile(path, []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func TestRenderer_ValidatesManifestBeforeFilesystemTouch(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.yaml")
	// Bad: missing required pki.ca_fingerprint.
	badYAML := `schema_version: "1.0.0"
fleet:
  hosts:
    - name: x
      role: single-box
`
	if err := os.WriteFile(badPath, []byte(badYAML), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	_, err := Render(RenderOptions{
		ManifestPath: badPath,
		DryRun:       true,
	})
	if err == nil {
		t.Errorf("invalid manifest = nil err, want error")
	}
	// No files should have been written.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir has %d entries; expected only the manifest: %v", len(entries), names)
	}
}

func TestRenderer_CurrentSymlinkUsesGitSHA(t *testing.T) {
	dir := t.TempDir()
	releases := filepath.Join(dir, "releases")
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	if err := os.MkdirAll(filepath.Join(releases, gitSHA), 0o755); err != nil {
		t.Fatalf("mkdir release: %v", err)
	}
	path := fixtureManifest(t, "fsn-1", "single-box")
	if _, err := Render(RenderOptions{
		ManifestPath: path,
		ReleasesRoot: releases,
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
	}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := os.Readlink(filepath.Join(dir, "current"))
	if err != nil {
		t.Fatalf("Readlink(current): %v", err)
	}
	want := filepath.Join(releases, gitSHA)
	if got != want {
		t.Fatalf("current = %q, want %q", got, want)
	}
}

func TestEndpointSANsIncludesPrivateEndpointAndConfiguredNames(t *testing.T) {
	sans, err := endpointSANs(manifest.Host{
		Name:    "fsn-3",
		Role:    "compute-only",
		Address: "fsn-3.gregale.dev:50051",
	}, []string{"vmmd-fsn-3.faas"})
	if err != nil {
		t.Fatalf("endpointSANs: %v", err)
	}
	if len(sans.DNSNames) != 2 || sans.DNSNames[0] != "fsn-3.gregale.dev" || sans.DNSNames[1] != "vmmd-fsn-3.faas" {
		t.Fatalf("DNS SANs = %v, want endpoint plus configured SAN", sans.DNSNames)
	}

	sans, err = endpointSANs(manifest.Host{
		Name:    "fsn-3",
		Role:    "compute-only",
		Address: "10.42.0.3:50051",
	}, nil)
	if err != nil {
		t.Fatalf("endpointSANs literal IP: %v", err)
	}
	if len(sans.IPAddresses) != 1 || sans.IPAddresses[0].String() != "10.42.0.3" {
		t.Fatalf("IP SANs = %v, want 10.42.0.3", sans.IPAddresses)
	}
}

func TestRenderer_ComputePKIIncludesPrivateEndpointSAN(t *testing.T) {
	dir := t.TempDir()
	path := fixtureManifest(t, "fsn-2", "compute-only")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}
	body = []byte(strings.Replace(string(body),
		"    - name: fsn-2\n      role: compute-only\n",
		"    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n", 1))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write endpoint manifest: %v", err)
	}

	_, err = Render(RenderOptions{
		ManifestPath: path,
		Host:         "fsn-2",
		ReleasesRoot: filepath.Join(dir, "releases"),
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	certPEM, err := os.ReadFile(filepath.Join(dir, "tls", "vmmd", "server.crt"))
	if err != nil {
		t.Fatalf("read vmmd server cert: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("vmmd server cert is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse vmmd server cert: %v", err)
	}
	if !containsString(cert.DNSNames, "vmmd.faas") || !containsString(cert.DNSNames, "fsn-2.gregale.dev") {
		t.Fatalf("vmmd server SANs = %v, want role and endpoint identities", cert.DNSNames)
	}
	if cert.Subject.CommonName != "fsn-2.faas" {
		t.Fatalf("vmmd server CN = %q, want fsn-2.faas", cert.Subject.CommonName)
	}

	certPEM, err = os.ReadFile(filepath.Join(dir, "tls", "gatewayd", "apid-client.crt"))
	if err != nil {
		t.Fatalf("read gatewayd apid client cert: %v", err)
	}
	block, _ = pem.Decode(certPEM)
	if block == nil {
		t.Fatal("gatewayd apid client cert is not PEM")
	}
	cert, err = x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse gatewayd apid client cert: %v", err)
	}
	if cert.Subject.CommonName != "fsn-2.faas" {
		t.Fatalf("gatewayd apid client CN = %q, want fsn-2.faas", cert.Subject.CommonName)
	}
	if !containsString(cert.DNSNames, "gatewayd.faas") || !containsString(cert.DNSNames, "fsn-2.gregale.dev") {
		t.Fatalf("gatewayd apid client SANs = %v, want role and endpoint identities", cert.DNSNames)
	}
}

func TestRenderer_PKITrustOnlyDoesNotRequireCAKey(t *testing.T) {
	dir := t.TempDir()
	path := fixtureManifest(t, "fsn-2", "compute-only")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body),
		"    - name: fsn-2\n      role: compute-only\n",
		"    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n", 1))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	opts := RenderOptions{
		ManifestPath: path,
		Host:         "fsn-2",
		ReleasesRoot: filepath.Join(dir, "releases"),
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
	}
	if err := os.MkdirAll(filepath.Join(opts.ReleasesRoot, "0123456789abcdef0123456789abcdef01234567"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(opts); err != nil {
		t.Fatalf("initial Render: %v", err)
	}
	caKey := filepath.Join(opts.PKIRootDir, "ca", "ca.key")
	if err := os.Remove(caKey); err != nil {
		t.Fatal(err)
	}
	opts.PKITrustOnly = true
	if _, err := Render(opts); err != nil {
		t.Fatalf("trust-only Render: %v", err)
	}
}

func TestRenderer_ResolvesSingleBoxHost(t *testing.T) {
	dir := t.TempDir()
	path := fixtureManifest(t, "fsn-1", "single-box")
	opts := RenderOptions{
		ManifestPath: path,
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
		DryRun:       true,
	}
	report, err := Render(opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if report.Host != "fsn-1" {
		t.Errorf("Host = %q, want fsn-1", report.Host)
	}
	if report.Role != "single-box" {
		t.Errorf("Role = %q, want single-box (PR-2 computes role for the role-write step)", report.Role)
	}
	if len(report.Outputs) == 0 {
		t.Errorf("Outputs is empty")
	}
}

func TestRenderer_ResolvesExplicitHost(t *testing.T) {
	// Write a multi-host manifest; render only the second host.
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	yaml := `schema_version: "1.0.0"
fleet:
  hosts:
    - name: fsn-1
      role: control-plane
      address: 10.42.0.1:7100
    - name: fsn-2
      role: compute-only
      address: 10.42.0.2:50051
daemons:
  schedd: {bind: unix:///run/faas/schedd.sock}
  vmmd: {bind: unix:///run/faas/vmmd.sock}
  apid: {bind: unix:///run/faas/apid.sock}
overlay:
  provider: tailscale
  cidr: 10.42.0.0/24
dns:
  apps_domain: apps.gregale.dev
  mode: cloudflare
postgresql:
  dsn: postgres://localhost/faas
  database: faas
  migration_max_slot: 1
  policy: on-boot
release:
  id: v1.0.0
  git_sha: 0123456789abcdef0123456789abcdef01234567
  architecture: x86_64
  firecracker_version: "1.10.0"
  firecracker_digest: ` + strings.Repeat("a", 64) + `
  kernel_digest: ` + strings.Repeat("b", 64) + `
  builder_base_digest: ` + strings.Repeat("c", 64) + `
  runtime_base_digest: ` + strings.Repeat("d", 64) + `
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
  ca_fingerprint: ` + strings.Repeat("e", 64) + `
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	opts := RenderOptions{
		ManifestPath: path,
		Host:         "fsn-2",
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
		DryRun:       true,
	}
	report, err := Render(opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if report.Host != "fsn-2" {
		t.Errorf("Host = %q, want fsn-2", report.Host)
	}
}

func TestRenderer_RefusesUnknownHost(t *testing.T) {
	path := fixtureManifest(t, "fsn-1", "single-box")
	_, err := Render(RenderOptions{
		ManifestPath: path,
		Host:         "no-such-host",
		DryRun:       true,
	})
	if err == nil {
		t.Errorf("unknown host = nil err, want error")
	}
	if err != nil && !strings.Contains(err.Error(), "no-such-host") {
		t.Errorf("err = %v, want host name in message", err)
	}
}

func TestRenderer_FailsWithoutMemoryController(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	// Copy the fixture, but flip controllers to drop memory.
	yaml := `schema_version: "1.0.0"
fleet:
  hosts:
    - name: fsn-1
      role: single-box
daemons:
  schedd: {bind: unix:///run/faas/schedd.sock}
overlay:
  provider: tailscale
  cidr: 10.42.0.0/24
dns:
  apps_domain: apps.gregale.dev
  mode: cloudflare
postgresql:
  dsn: postgres://localhost/faas
  database: faas
  migration_max_slot: 1
  policy: on-boot
release:
  id: v1.0.0
  git_sha: 0123456789abcdef0123456789abcdef01234567
  architecture: x86_64
  firecracker_version: "1.10.0"
  firecracker_digest: ` + strings.Repeat("a", 64) + `
  kernel_digest: ` + strings.Repeat("b", 64) + `
  builder_base_digest: ` + strings.Repeat("c", 64) + `
  runtime_base_digest: ` + strings.Repeat("d", 64) + `
storage:
  fast_root: /srv/fc
  spool_root: /var/spool/faas
  log_root: /var/log/faas
  run_dir: /run/faas
cgroups:
  slice: faas-cp.slice
  controllers: "cpu,io,pids"
pki:
  root_dir: /etc/faas/tls
  ca_fingerprint: ` + strings.Repeat("e", 64) + `
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The validator already rejects a missing-memory-c manifest
	// (PR-0 carve-out). The renderer's invariant is that the
	// validator runs BEFORE any renderPKI/renderCgroup call.
	// Without the validator's check, the renderer would silently
	// emit a non-functional subtree_control. With the validator's
	// check, we surface the error at load-then-validate time.
	_, err := Render(RenderOptions{
		ManifestPath: path,
		DryRun:       true,
	})
	if err == nil {
		t.Errorf("missing-memory = nil err, want error")
	}
	if err != nil && !strings.Contains(err.Error(), "memory") {
		t.Errorf("err = %v, want 'memory' in message", err)
	}
}

func TestRenderer_ShortCircuitsOnIdempotentHash(t *testing.T) {
	// Run twice. First run writes; second run should report
	// unchanged + Action="unchanged" for every output.
	dir := t.TempDir()
	path := fixtureManifest(t, "fsn-1", "single-box")
	opts := RenderOptions{
		ManifestPath: path,
		ReleasesRoot: filepath.Join(dir, "releases"),
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
	}

	// First run.
	r1, err := Render(opts)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	if r1.Skipped {
		t.Errorf("first run Skipped = true, want false")
	}

	// Second run: same opts, same on-disk content.
	r2, err := Render(opts)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if !r2.Skipped {
		t.Errorf("second run Skipped = false, want true (idempotent)")
	}
	for _, o := range r2.Outputs {
		if o.Action != "unchanged" {
			t.Errorf("second run %s.Action = %q, want unchanged", o.Path, o.Action)
		}
	}
}

func TestRenderer_ResolvesControlPlaneDaemons(t *testing.T) {
	dir := t.TempDir()
	path := fixtureManifest(t, "fsn-1", "control-plane")
	opts := RenderOptions{
		ManifestPath: path,
		Host:         "fsn-1",
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
		DryRun:       true,
	}
	report, err := Render(opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Schema-drift guard: a control-plane box MUST NOT emit
	// vmmd.toml / faas-vmmd.service (those belong on a compute-
	// only box). Failure here is the canonical "role filter
	// regressed" signal.
	for _, mustNotContain := range []string{
		"vmmd.toml",
		"faas-vmmd.service",
		"builderd.toml",
		"imaged.toml",
		"gatewayd-internal.toml",
	} {
		for _, o := range report.Outputs {
			if strings.Contains(o.Path, mustNotContain) {
				t.Errorf("control-plane box must not emit %s, got %s", mustNotContain, o.Path)
			}
		}
	}
	// And it MUST emit the control-plane set.
	gotPaths := map[string]bool{}
	for _, o := range report.Outputs {
		gotPaths[filepath.Base(o.Path)] = true
	}
	for _, want := range []string{
		"apid.toml", "schedd.toml", "meterd.toml", "githubd.toml",
		"gatewayd-public.toml", "faas-cp.slice",
	} {
		if !gotPaths[want] {
			t.Errorf("control-plane box missing %s in output", want)
		}
	}
}

func TestRenderer_ResolvesComputeOnlyDaemons(t *testing.T) {
	dir := t.TempDir()
	path := fixtureManifest(t, "fsn-2", "compute-only")
	opts := RenderOptions{
		ManifestPath: path,
		Host:         "fsn-2",
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
		DryRun:       true,
	}
	report, err := Render(opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Schema-drift guard: a compute-only box MUST NOT emit
	// apid / meterd / githubd / gatewayd-public TOMLs or service
	// units. M9 deliberately emits a node-local schedd alongside vmmd.
	for _, mustNotContain := range []string{
		"apid.toml", "meterd.toml",
		"githubd.toml", "gatewayd-public.toml",
		"faas-apid.service",
	} {
		for _, o := range report.Outputs {
			if strings.Contains(o.Path, mustNotContain) {
				t.Errorf("compute-only box must not emit %s, got %s", mustNotContain, o.Path)
			}
		}
	}
	// And it MUST emit the complete compute-only set.
	gotPaths := map[string]bool{}
	for _, o := range report.Outputs {
		gotPaths[filepath.Base(o.Path)] = true
	}
	for _, want := range []string{
		"vmmd.toml", "schedd.toml", "imaged.toml", "builderd.toml", "gatewayd-internal.toml",
		"faas-cp.slice",
	} {
		if !gotPaths[want] {
			t.Errorf("compute-only box missing %s in output", want)
		}
	}
}

func TestRenderer_RequiresAbsolutePaths(t *testing.T) {
	path := fixtureManifest(t, "fsn-1", "single-box")
	_, err := Render(RenderOptions{
		ManifestPath: path,
		EtcFaasDir:   "relative/path",
	})
	if err == nil {
		t.Errorf("relative path = nil err, want error")
	}
	if err != nil && !strings.Contains(err.Error(), "absolute") {
		t.Errorf("err = %v, want 'absolute' in message", err)
	}
}

func TestRenderer_RequiresManifestPath(t *testing.T) {
	_, err := Render(RenderOptions{})
	if err == nil {
		t.Errorf("empty ManifestPath = nil err, want error")
	}
}

// TestRenderer_DaemonConfigFor round-trips the registry-name ↔
// manifest-name bridge. Every registry daemon must map to a
// manifest.DaemonConfig field.
func TestRenderer_DaemonConfigFor(t *testing.T) {
	m := &manifest.Manifest{
		Daemons: manifest.Daemons{
			Schedd:           &manifest.DaemonConfig{},
			Vmmd:             &manifest.DaemonConfig{},
			Apid:             &manifest.DaemonConfig{},
			Meterd:           &manifest.DaemonConfig{},
			Githubd:          &manifest.DaemonConfig{},
			GatewaydInternal: &manifest.DaemonConfig{},
			GatewaydPublic:   &manifest.DaemonConfig{},
			Imaged:           &manifest.DaemonConfig{},
			Builderd:         &manifest.DaemonConfig{},
		},
	}
	for _, name := range []string{
		"vmmd", "apid", "schedd", "meterd", "githubd",
		"gatewayd-internal", "gatewayd-public", "builderd", "imaged",
	} {
		dc := daemonConfigFor(m, name)
		if dc == nil {
			t.Errorf("daemonConfigFor(%q) = nil", name)
		}
	}
	if dc := daemonConfigFor(m, "no-such-daemon"); dc != nil {
		t.Errorf("daemonConfigFor(unsupported) = %v, want nil", dc)
	}
}

func TestFilterDaemons(t *testing.T) {
	got := filterDaemons([]string{"vmmd", "apid", "schedd", "meterd"}, "vmmd", "schedd")
	want := []string{"vmmd", "schedd"}
	if !equalStrings(got, want) {
		t.Errorf("filterDaemons = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// PR-2 (issue #911 / ADR-110) tests: renderSystemd must thread
// host.Name into the rendered unit via an Environment=FAAS_NODE_NAME=
// line. The line is inserted into the [Service] block (after the
// last Environment= directive emitted by daemonunit.Unit), preserved
// across every daemon, and omitted on empty host.Name (single-box
// dev).

func TestRenderSystemd_ThreadsHostNameIntoUnit(t *testing.T) {
	// Every daemon in the registry must gain the env line when
	// hostName is non-empty. The POST-PROCESS is the renderer
	// contract; daemonunit itself does not know about FAAS_NODE_NAME.
	for _, daemon := range daemonunitspec.ActivationOrder() {
		t.Run(daemon, func(t *testing.T) {
			body, err := renderSystemd(daemon, "fsn-1")
			if err != nil {
				t.Fatalf("renderSystemd(%s): %v", daemon, err)
			}
			if !bytes.Contains(body, []byte("Environment=FAAS_NODE_NAME=fsn-1")) {
				t.Errorf("rendered unit missing Environment=FAAS_NODE_NAME=fsn-1:\n%s", body)
			}
			serviceStart := bytes.Index(body, []byte("[Service]\n"))
			if serviceStart < 0 {
				t.Fatal("rendered unit missing [Service] section")
			}
			identityAt := bytes.Index(body, []byte("Environment=FAAS_NODE_NAME=fsn-1"))
			if identityAt < serviceStart {
				t.Errorf("node identity landed before [Service]:\n%s", body)
			}
		})
	}
}

func TestRenderSystemd_EmptyHostNameOmitsEnvLine(t *testing.T) {
	// Single-box dev: no host.Name in the manifest or the host
	// is "" in the renderer opts. The rendered unit must NOT
	// carry an Environment=FAAS_NODE_NAME= line (the daemon's
	// TOML default applies and the loader's no-NodeName posture
	// is the explicit single-box posture).
	for _, daemon := range daemonunitspec.ActivationOrder() {
		t.Run(daemon, func(t *testing.T) {
			body, err := renderSystemd(daemon, "")
			if err != nil {
				t.Fatalf("renderSystemd(%s): %v", daemon, err)
			}
			if bytes.Contains(body, []byte("FAAS_NODE_NAME=")) {
				t.Errorf("rendered unit carries FAAS_NODE_NAME= despite empty hostName:\n%s", body)
			}
		})
	}
}

func TestRenderSystemd_IdempotentOnExistingNodeName(t *testing.T) {
	// Re-rendering must not append a duplicate line on the
	// second pass. The renderer is safe for two consecutive
	// `manifest render` runs against the same fleet.
	body1, err := renderSystemd("schedd", "fsn-1")
	if err != nil {
		t.Fatalf("renderSystemd 1: %v", err)
	}
	if bytes.Count(body1, []byte("FAAS_NODE_NAME=")) != 1 {
		t.Errorf("rendered unit has %d FAAS_NODE_NAME= lines, want 1", bytes.Count(body1, []byte("FAAS_NODE_NAME=")))
	}
	// The marker-based dedup is the load-bearing idempotency
	// guarantee: re-injecting on a body that already carries the
	// line is a no-op.
	body2, err := injectNodeNameEnvironment(body1, "fsn-1")
	if err != nil {
		t.Fatalf("injectNodeNameEnvironment 2: %v", err)
	}
	if bytes.Count(body2, []byte("FAAS_NODE_NAME=")) != 1 {
		t.Errorf("re-injected unit has %d FAAS_NODE_NAME= lines, want 1 (idempotent)", bytes.Count(body2, []byte("FAAS_NODE_NAME=")))
	}
}

func TestRenderer_RendersNodeNameIntoSystemdOutput(t *testing.T) {
	// End-to-end: dry-run render against a manifest with a non-
	// empty host name. Assert every emitted faas-<daemon>.service
	// path matches the env line by reading the file from disk.
	dir := t.TempDir()
	path := fixtureManifest(t, "fsn-1", "single-box")
	opts := RenderOptions{
		ManifestPath: path,
		ReleasesRoot: filepath.Join(dir, "releases"),
		EtcFaasDir:   filepath.Join(dir, "etc"),
		SystemdDir:   filepath.Join(dir, "systemd"),
		PKIRootDir:   filepath.Join(dir, "tls"),
		CgroupRoot:   filepath.Join(dir, "cgroup"),
	}
	report, err := Render(opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, o := range report.Outputs {
		if !strings.HasSuffix(o.Path, ".service") {
			continue
		}
		body, err := os.ReadFile(o.Path)
		if err != nil {
			t.Errorf("read %s: %v", o.Path, err)
			continue
		}
		if !bytes.Contains(body, []byte("Environment=FAAS_NODE_NAME=fsn-1")) {
			t.Errorf("%s missing Environment=FAAS_NODE_NAME=fsn-1", o.Path)
		}
	}
}

func TestRenderer_OmitsNodeNameOnEmptyHost(t *testing.T) {
	// The manifest validator rejects hosts with empty name, so
	// the renderer's "no host name" path is reachable only via
	// the lower-level renderSystemd entry. The behaviour is
	// pinned by TestRenderSystemd_EmptyHostNameOmitsEnvLine above;
	// this test pins the renderer's call-site contract: even a
	// single-box host (host.Name == "") must NOT inject the env
	// line into the rendered unit. The renderer routes "" through
	// renderSystemd unchanged.
	body, err := renderSystemd("schedd", "")
	if err != nil {
		t.Fatalf("renderSystemd: %v", err)
	}
	if bytes.Contains(body, []byte("FAAS_NODE_NAME=")) {
		t.Errorf("renderSystemd(\"schedd\", \"\") carries FAAS_NODE_NAME= despite empty hostName:\n%s", body)
	}
}
