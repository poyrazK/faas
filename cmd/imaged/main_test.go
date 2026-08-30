// Tests for the imaged daemon entrypoint. The actual VM work needs KVM+root
// (//go:build metal); here we only exercise the override gates (digest-only
// base ref + OCI pull timeout env) without booting pgxpool.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
)

// TestOciPullTimeout covers the FAAS_OCI_PULL_TIMEOUT_SECONDS knob —
// valid value honors, invalid/empty/non-positive fall back to
// api.OCIPullTimeoutSeconds (60s).
func TestOciPullTimeout(t *testing.T) {
	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "")
	if got := ociPullTimeout().Seconds(); got != 60 {
		t.Errorf("default ociPullTimeout = %ds, want 60s", int(got))
	}

	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "15")
	if got := ociPullTimeout().Seconds(); got != 15 {
		t.Errorf("override ociPullTimeout = %ds, want 15s", int(got))
	}

	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "garbage")
	if got := ociPullTimeout().Seconds(); got != 60 {
		t.Errorf("garbage override ociPullTimeout = %ds, want fallback 60s", int(got))
	}

	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "0")
	if got := ociPullTimeout().Seconds(); got != 60 {
		t.Errorf("zero override ociPullTimeout = %ds, want fallback 60s", int(got))
	}

	t.Setenv("FAAS_OCI_PULL_TIMEOUT_SECONDS", "-5")
	if got := ociPullTimeout().Seconds(); got != 60 {
		t.Errorf("negative override ociPullTimeout = %ds, want fallback 60s", int(got))
	}
}

// TestOverrideGate_DigestPinned covers the success path: a digest-pinned
// reference passes the gate. Mirrors the parsing logic in run() so a
// future refactor of the gate is caught here.
func TestOverrideGate_DigestPinned(t *testing.T) {
	cases := []struct {
		name string
		ref  string
	}{
		{"deploy base digest", "ghcr.io/onebox-faas/runtime-base@sha256:" + sha256hex64()},
		{"builder base digest", "ghcr.io/onebox-faas/builder-base@sha256:" + sha256hex64()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := oci.ParseReference(tc.ref)
			if err != nil {
				t.Fatalf("ParseReference(%q) = %v", tc.ref, err)
			}
			if ref.Digest == "" {
				t.Errorf("override gate must accept %q (digest empty)", tc.ref)
			}
		})
	}
}

// TestOverrideGate_BareTagRejected covers the failure path: a tag-only
// reference must be refused by the gate. The run() function itself
// returns an error, but here we exercise the parsing primitive the
// gate uses so a regression on ParseReference (e.g. accepting a tag
// as if it were a digest) is caught.
func TestOverrideGate_BareTagRejected(t *testing.T) {
	cases := []string{
		"ghcr.io/onebox-faas/builder-base:latest",
		"ghcr.io/onebox-faas/runtime-base:1.2.3",
		"docker.io/library/alpine",
	}
	for _, ref := range cases {
		t.Run(ref, func(t *testing.T) {
			parsed, err := oci.ParseReference(ref)
			if err != nil {
				t.Fatalf("ParseReference(%q) returned error: %v", ref, err)
			}
			if parsed.Digest != "" {
				t.Errorf("override gate must REJECT %q (digest %q non-empty)", ref, parsed.Digest)
			}
		})
	}
}

func TestBuilderBaseRef_MultiBoxRequiresDigest(t *testing.T) {
	t.Setenv("FAAS_NODE_NAME", "fsn-2")
	t.Setenv("FAAS_BUILDER_BASE_REF", "")
	if _, err := builderBaseRefFromEnv(); err == nil {
		t.Fatal("builderBaseRefFromEnv() accepted an unset ref on a named host")
	}
}

func TestBuilderBaseRef_SingleBoxKeepsDevelopmentDefault(t *testing.T) {
	t.Setenv("FAAS_NODE_NAME", "")
	t.Setenv("FAAS_BUILDER_BASE_REF", "")
	got, err := builderBaseRefFromEnv()
	if err != nil {
		t.Fatalf("builderBaseRefFromEnv() = error %v", err)
	}
	if got != "ghcr.io/poyrazk/builder-base:latest" {
		t.Fatalf("builderBaseRefFromEnv() = %q, want development builder default", got)
	}
}

// ADR-140 §Decision 2: FAAS_BUILDER_ARCH env. Same fail-loud gate
// shape as FAAS_BUILDER_BASE_REF above.
func TestBuilderArch_MultiBoxRequiresEnv(t *testing.T) {
	t.Setenv("FAAS_NODE_NAME", "fsn-2")
	t.Setenv("FAAS_BUILDER_ARCH", "")
	if _, err := builderArchFromEnv(); err == nil {
		t.Fatal("builderArchFromEnv() accepted unset FAAS_BUILDER_ARCH on a named host")
	}
}

func TestBuilderArch_SingleBoxDefaultsToGOARCH(t *testing.T) {
	t.Setenv("FAAS_NODE_NAME", "")
	t.Setenv("FAAS_BUILDER_ARCH", "")
	got, err := builderArchFromEnv()
	if err != nil {
		t.Fatalf("builderArchFromEnv() = error %v", err)
	}
	// amd64 on amd64 CI / developer hosts; arm64 on M-series Mac CI.
	want := runtime.GOARCH
	if got != want {
		t.Errorf("builderArchFromEnv() = %q, want %q (runtime.GOARCH)", got, want)
	}
}

func TestBuilderArch_InvalidValueRejects(t *testing.T) {
	t.Setenv("FAAS_NODE_NAME", "")
	t.Setenv("FAAS_BUILDER_ARCH", "riscv64")
	if _, err := builderArchFromEnv(); err == nil {
		t.Fatal("builderArchFromEnv() accepted riscv64 (not amd64 / arm64)")
	}
}

func TestBuilderArch_ValidValuesPass(t *testing.T) {
	for _, v := range []string{"amd64", "arm64"} {
		t.Setenv("FAAS_NODE_NAME", "")
		t.Setenv("FAAS_BUILDER_ARCH", v)
		got, err := builderArchFromEnv()
		if err != nil {
			t.Fatalf("builderArchFromEnv(=%q): %v", v, err)
		}
		if got != v {
			t.Errorf("builderArchFromEnv() = %q, want %q", got, v)
		}
	}
}

// sha256hex64 returns a 64-hex-char fake sha256 digest. Used only to
// satisfy the `sha256:<64 hex>` shape that oci.ParseReference requires;
// the bytes themselves are not cryptographically meaningful.
func sha256hex64() string {
	return "0000000000000000000000000000000000000000000000000000000000000000"
}

// PR-5 / issue #911 — manifest reconcile against FAAS_BUILDER_BASE_REF.
//
// reconcileManifestBuilderBase is the gate that ties the manifest's
// release.builder_base_digest to the env override. Three cases:
//
//   1. FAAS_MANIFEST_PATH unset (single-box dev): no-op.
//   2. Manifest sets digest, env unset → no-op (env-only behaviour).
//   3. Manifest sets digest, env overrides with non-matching digest → fatal.
//   4. Manifest sets digest, env overrides with matching digest → OK.
//   5. Manifest unreadable → fatal (load-failure surfaces `gregale manifest
//      validate`).
//   6. Manifest's builder_base_digest empty → no-op (operator opted out).

func TestReconcileManifestBuilderBase_NoManifestPath(t *testing.T) {
	// Single-box dev: FAAS_MANIFEST_PATH unset → no-op regardless of
	// FAAS_BUILDER_BASE_REF state.
	t.Setenv("FAAS_MANIFEST_PATH", "")
	t.Setenv("FAAS_BUILDER_BASE_REF", "")
	if err := reconcileManifestBuilderBase(); err != nil {
		t.Errorf("reconcile(no manifest path) = %v; want nil", err)
	}
}

func TestReconcileManifestBuilderBase_ManifestEmptyDigest(t *testing.T) {
	// Manifest present but release.builder_base_digest empty → no-op
	// (operator opted out of the contract).
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.yaml")
	yaml := `schema_version: "1.0.0"
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
postgresql:
  dsn: postgres://faas@127.0.0.1:5432/faas
  database: faas
  migration_max_slot: 10
  policy: on-boot
release:
  id: v1.0.0
  git_sha: abc1234567890abcdef1234567890abcdef12345
  architecture: x86_64
  firecracker_version: 1.10.0
  firecracker_digest: ` + sha256ColonHex() + `
  kernel_digest: ` + sha256ColonHex() + `
  builder_base_digest: ""
  runtime_base_digest: ` + sha256ColonHex() + `
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
  ca_fingerprint: ` + sha256ColonHex() + `
  allowed_sans:
    - schedd.faas
`
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("FAAS_MANIFEST_PATH", p)
	t.Setenv("FAAS_BUILDER_BASE_REF", "ghcr.io/x/base@sha256:"+sha256hex64())
	if err := reconcileManifestBuilderBase(); err != nil {
		t.Errorf("reconcile(empty digest) = %v; want nil (operator opted out)", err)
	}
}

func TestReconcileManifestBuilderBase_DigestMismatchFatal(t *testing.T) {
	// Manifest pins digest A; env overrides with digest B → fatal.
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.yaml")
	mismatch := "1111111111111111111111111111111111111111111111111111111111111111"
	if err := os.WriteFile(p, []byte(manifestWithBuilderDigest(sha256hex64())), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("FAAS_MANIFEST_PATH", p)
	t.Setenv("FAAS_BUILDER_BASE_REF", "ghcr.io/x/base@sha256:"+mismatch)
	err := reconcileManifestBuilderBase()
	if err == nil {
		t.Fatal("reconcile(digest mismatch) = nil; want fatal")
	}
	if !strings.Contains(err.Error(), "does not match manifest") {
		t.Errorf("reconcile error = %q; want digest-mismatch message", err.Error())
	}
	if !strings.Contains(err.Error(), "gregale manifest validate") {
		t.Errorf("reconcile error = %q; want pointer to gregale manifest validate", err.Error())
	}
}

func TestReconcileManifestBuilderBase_DigestMatchOK(t *testing.T) {
	// Manifest pins digest A; env overrides with same digest A → OK.
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.yaml")
	d := sha256hex64()
	if err := os.WriteFile(p, []byte(manifestWithBuilderDigest(d)), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("FAAS_MANIFEST_PATH", p)
	t.Setenv("FAAS_BUILDER_BASE_REF", "ghcr.io/x/base@sha256:"+d)
	if err := reconcileManifestBuilderBase(); err != nil {
		t.Errorf("reconcile(match) = %v; want nil", err)
	}
}

func TestReconcileManifestBuilderBase_RawManifestDigestMatchOK(t *testing.T) {
	// Production manifests use the raw 64-hex form, while OCI references
	// necessarily use sha256:<hex>. The reconcile gate must normalize both.
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.yaml")
	d := sha256hex64()
	rawManifest := strings.Replace(
		manifestWithBuilderDigest(d),
		"builder_base_digest: sha256:"+d,
		"builder_base_digest: "+d,
		1,
	)
	if err := os.WriteFile(p, []byte(rawManifest), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("FAAS_MANIFEST_PATH", p)
	t.Setenv("FAAS_BUILDER_BASE_REF", "ghcr.io/x/base@sha256:"+d)
	if err := reconcileManifestBuilderBase(); err != nil {
		t.Errorf("reconcile(raw manifest match) = %v; want nil", err)
	}
}

func TestReconcileManifestBuilderBase_ManifestUnreadable(t *testing.T) {
	t.Setenv("FAAS_MANIFEST_PATH", "/nonexistent/manifest.yaml")
	t.Setenv("FAAS_BUILDER_BASE_REF", "")
	err := reconcileManifestBuilderBase()
	if err == nil {
		t.Fatal("reconcile(unreadable) = nil; want fatal")
	}
	if !strings.Contains(err.Error(), "gregale manifest validate") {
		t.Errorf("reconcile error = %q; want pointer to gregale manifest validate", err.Error())
	}
}

// manifestWithBuilderDigest returns a minimal split-box manifest with
// release.builder_base_digest set to the given 64-hex sha256 digest
// (without the `sha256:` prefix; the helper adds it).
func manifestWithBuilderDigest(digest64 string) string {
	d := sha256ColonHex()
	if digest64 != "" {
		d = "sha256:" + digest64
	}
	return `schema_version: "1.0.0"
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
postgresql:
  dsn: postgres://faas@127.0.0.1:5432/faas
  database: faas
  migration_max_slot: 10
  policy: on-boot
release:
  id: v1.0.0
  git_sha: abc1234567890abcdef1234567890abcdef12345
  architecture: x86_64
  firecracker_version: 1.10.0
  firecracker_digest: ` + d + `
  kernel_digest: ` + d + `
  builder_base_digest: ` + d + `
  runtime_base_digest: ` + d + `
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
  ca_fingerprint: ` + d + `
  allowed_sans:
    - schedd.faas
`
}

// sha256ColonHex returns `sha256:` + 64 zeros — used in test fixtures
// where any sha256-shaped digest satisfies the manifest validator.
func sha256ColonHex() string {
	return "sha256:" + sha256hex64()
}
