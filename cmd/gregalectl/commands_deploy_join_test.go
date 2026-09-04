package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"github.com/onebox-faas/faas/pkg/state"
)

func splitboxJoinManifest(t *testing.T) string {
	t.Helper()
	body := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: fsn-1.gregale.dev:9091\n    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n", 1)
	return writeSplitboxManifest(t, body)
}

func TestDeployJoinValidate_DryRunNeedsOnlyManifestAndSSH(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := deployJoinValidate(deployJoinOptions{
		ManifestFile: manifestPath,
		Node:         "fsn-2",
		SSHHost:      "198.51.100.20",
		RepoRoot:     repo,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("deployJoinValidate: %v", err)
	}
	if report.DatabaseNode != "fsn-2.faas" {
		t.Errorf("DatabaseNode = %q, want fsn-2.faas", report.DatabaseNode)
	}
	if report.ReleaseGitSHA != "abc1234567890abcdef1234567890abcdef12345" {
		t.Errorf("ReleaseGitSHA = %q", report.ReleaseGitSHA)
	}
	if len(report.Steps) < 8 {
		t.Fatalf("steps = %d, want lifecycle plan", len(report.Steps))
	}
}

func TestHasComputeDatabaseEnvRequiresBothDaemonVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compute-db.env")

	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "both entries",
			body: "DATABASE_URL=postgres://faas@example/faas\nFAAS_VMMD_DBURL=postgres://faas@example/faas\n",
			want: true,
		},
		{
			name: "vmmd entry missing",
			body: "DATABASE_URL=postgres://faas@example/faas\n",
			want: false,
		},
		{
			name: "empty vmmd entry",
			body: "DATABASE_URL=postgres://faas@example/faas\nFAAS_VMMD_DBURL=\n",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := hasComputeDatabaseEnv(path); got != tt.want {
				t.Fatalf("hasComputeDatabaseEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateRuntimeBasesEnvRequiresAllPinnedRefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-bases.env")
	valid := strings.Join([]string{
		"FAAS_DEPLOY_BASE_REF_MINIMAL=ghcr.io/example/base-minimal@sha256:" + strings.Repeat("0", 64),
		"FAAS_DEPLOY_BASE_REF_NODE22=ghcr.io/example/runner-node22@sha256:" + strings.Repeat("a", 64),
		"FAAS_DEPLOY_BASE_REF_PYTHON312=ghcr.io/example/runner-python312@sha256:" + strings.Repeat("b", 64),
		"FAAS_DEPLOY_BASE_REF_GO124=ghcr.io/example/runner-go124@sha256:" + strings.Repeat("c", 64),
		"FAAS_DEPLOY_BASE_REF_GO124_ALPINE=ghcr.io/example/runner-go124-alpine@sha256:" + strings.Repeat("d", 64),
		"FAAS_DEPLOY_BASE_REF_NODE24=ghcr.io/example/runner-node24@sha256:" + strings.Repeat("e", 64),
		"FAAS_DEPLOY_BASE_REF_PYTHON313=ghcr.io/example/runner-python313@sha256:" + strings.Repeat("f", 64),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeBasesEnv(path, nil); err != nil {
		t.Fatalf("valid runtime contract rejected: %v", err)
	}

	if err := os.WriteFile(path, []byte(strings.Replace(valid, "FAAS_DEPLOY_BASE_REF_NODE24=", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeBasesEnv(path, nil); err == nil || !strings.Contains(err.Error(), "NODE24") {
		t.Fatalf("missing runtime ref error = %v", err)
	}
}

func TestValidateRuntimeBasesEnvMatchesSignedManifestRefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-bases.env")
	ref := "ghcr.io/example/runner-node22@sha256:" + strings.Repeat("a", 64)
	body := "FAAS_DEPLOY_BASE_REF_NODE22=" + ref + "\n"
	for _, line := range []string{
		"FAAS_DEPLOY_BASE_REF_MINIMAL=ghcr.io/example/base-minimal@sha256:" + strings.Repeat("0", 64),
		"FAAS_DEPLOY_BASE_REF_PYTHON312=ghcr.io/example/runner-python312@sha256:" + strings.Repeat("b", 64),
		"FAAS_DEPLOY_BASE_REF_GO124=ghcr.io/example/runner-go124@sha256:" + strings.Repeat("c", 64),
		"FAAS_DEPLOY_BASE_REF_GO124_ALPINE=ghcr.io/example/runner-go124-alpine@sha256:" + strings.Repeat("d", 64),
		"FAAS_DEPLOY_BASE_REF_NODE24=ghcr.io/example/runner-node24@sha256:" + strings.Repeat("e", 64),
		"FAAS_DEPLOY_BASE_REF_PYTHON313=ghcr.io/example/runner-python313@sha256:" + strings.Repeat("f", 64),
	} {
		body += line + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"minimal":      "ghcr.io/example/base-minimal@sha256:" + strings.Repeat("0", 64),
		"node22":       ref,
		"python312":    "ghcr.io/example/runner-python312@sha256:" + strings.Repeat("b", 64),
		"go124":        "ghcr.io/example/runner-go124@sha256:" + strings.Repeat("c", 64),
		"go124_alpine": "ghcr.io/example/runner-go124-alpine@sha256:" + strings.Repeat("d", 64),
		"node24":       "ghcr.io/example/runner-node24@sha256:" + strings.Repeat("e", 64),
		"python313":    "ghcr.io/example/runner-python313@sha256:" + strings.Repeat("f", 64),
	}
	if err := validateRuntimeBasesEnv(path, expected); err != nil {
		t.Fatalf("signed runtime contract rejected: %v", err)
	}
	expected["node22"] = strings.Replace(ref, "runner-node22", "runner-node24", 1)
	if err := validateRuntimeBasesEnv(path, expected); err == nil || !strings.Contains(err.Error(), "NODE22") {
		t.Fatalf("manifest mismatch error = %v", err)
	}
}

func TestDeployJoinValidate_RejectsControlPlane(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	_, err := deployJoinValidate(deployJoinOptions{
		ManifestFile: manifestPath,
		Node:         "fsn-1",
		SSHHost:      "198.51.100.10",
		RepoRoot:     t.TempDir(),
		DryRun:       true,
	})
	if err == nil || !strings.Contains(err.Error(), "requires compute-only") {
		t.Fatalf("error = %v, want compute-only guard", err)
	}
}

func TestDeployJoinValidate_AllowsSignedReleaseOverride(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const release = "0123456789abcdef0123456789abcdef01234567"
	report, err := deployJoinValidate(deployJoinOptions{
		ManifestFile:  manifestPath,
		Node:          "fsn-2",
		SSHHost:       "198.51.100.20",
		ReleaseGitSHA: release,
		RepoRoot:      repo,
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("deployJoinValidate: %v", err)
	}
	if report.ReleaseGitSHA != release {
		t.Fatalf("ReleaseGitSHA = %q, want %q", report.ReleaseGitSHA, release)
	}
}

func TestDeployJoinValidate_RejectsInvalidReleaseOverride(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := deployJoinValidate(deployJoinOptions{
		ManifestFile:  manifestPath,
		Node:          "fsn-2",
		SSHHost:       "198.51.100.20",
		ReleaseGitSHA: "not-a-sha",
		RepoRoot:      repo,
		DryRun:        true,
	})
	if err == nil || !strings.Contains(err.Error(), "--release-git-sha") {
		t.Fatalf("error = %v, want invalid release override", err)
	}
}

func TestDeployJoinValidate_StorageContract(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := deployJoinOptions{
		ManifestFile: manifestPath,
		Node:         "fsn-2",
		SSHHost:      "198.51.100.20",
		RepoRoot:     repo,
		DryRun:       true,
	}
	base.StorageDevice = "nvme0n1"
	if _, err := deployJoinValidate(base); err == nil || !strings.Contains(err.Error(), "absolute device path") {
		t.Fatalf("relative storage device error = %v, want absolute-path guard", err)
	}
	base.StorageDevice = ""
	base.FormatStorage = true
	if _, err := deployJoinValidate(base); err == nil || !strings.Contains(err.Error(), "requires --storage-device") {
		t.Fatalf("format-without-device error = %v, want explicit device guard", err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	withManifestDevice := strings.Replace(
		string(manifestBytes),
		"      address: fsn-2.gregale.dev:50051\n",
		"      address: fsn-2.gregale.dev:50051\n      storage_device: /dev/disk/by-id/google-local-ssd-0\n", 1,
	)
	deviceManifest := writeSplitboxManifest(t, withManifestDevice)
	base.ManifestFile = deviceManifest
	base.FormatStorage = true
	if _, err := deployJoinValidate(base); err != nil {
		t.Fatalf("manifest storage device should satisfy --format-storage: %v", err)
	}
}

func TestOverrideJoinHostVars_UsesProviderSSHOnlyForConnection(t *testing.T) {
	body := []byte("ansible_host: \"fsn-2.gregale.dev\"\nfaas_box_role: compute-only\n")
	got := string(overrideJoinHostVars(body, &deployJoinOptions{
		SSHHost: "203.0.113.8",
		SSHUser: "gregale",
		SSHPort: 2222,
		SSHKey:  "/tmp/id_ed25519",
	}))
	for _, want := range []string{
		`ansible_host: "203.0.113.8"`,
		`ansible_user: "gregale"`,
		"ansible_port: 2222",
		`ansible_ssh_private_key_file: "/tmp/id_ed25519"`,
		"faas_box_role: compute-only",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("host vars missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ansible_host: \"fsn-2.gregale.dev\"") {
		t.Errorf("provider SSH override left manifest endpoint in place:\n%s", got)
	}
}

func TestOverrideJoinHostVars_PinsKnownHostFile(t *testing.T) {
	got := string(overrideJoinHostVars([]byte("ansible_host: \"fsn-2.gregale.dev\"\n"), &deployJoinOptions{
		SSHHost:           "203.0.113.8",
		SSHUser:           "root",
		SSHPort:           22,
		SSHKnownHostsFile: "/tmp/gregale-known-hosts",
	}))
	want := `ansible_ssh_common_args: "-o UserKnownHostsFile=/tmp/gregale-known-hosts -o StrictHostKeyChecking=yes"`
	if !strings.Contains(got, want) {
		t.Fatalf("known_hosts pinning missing %q:\n%s", want, got)
	}
}

func TestFingerprintMatches(t *testing.T) {
	output := "256 SHA256:other host-a (ED25519)\n256 SHA256:expected host-b (ED25519)\n"
	if !fingerprintMatches(output, "SHA256:expected") {
		t.Fatal("expected fingerprint was not found")
	}
	if fingerprintMatches(output, "SHA256:missing") {
		t.Fatal("missing fingerprint was reported as present")
	}
}

func TestOverrideJoinHostVars_PreservesStorageContract(t *testing.T) {
	got := string(overrideJoinHostVars([]byte("faas_box_role: compute-only\n"), &deployJoinOptions{
		SSHHost:       "203.0.113.8",
		SSHUser:       "root",
		SSHPort:       22,
		StorageDevice: "/dev/disk/by-id/scsi-0Google_PersistentDisk_data",
		FormatStorage: true,
	}))
	for _, want := range []string{
		`faas_storage_device: "/dev/disk/by-id/scsi-0Google_PersistentDisk_data"`,
		"faas_storage_format: true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("host vars missing %q:\n%s", want, got)
		}
	}
}

func TestValidateSharedStorageEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.env")
	valid := "FAAS_STORAGE_BACKEND=oci\n" +
		"FAAS_STORAGE_LOCAL_PREFIXES=none\n" +
		"FAAS_REQUIRE_SHARED_ARTIFACTS=1\n" +
		"FAAS_STORAGE_CACHE_SERVE_STALE=0\n" +
		"FAAS_OCI_REGISTRY=https://registry.example\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err != nil {
		t.Fatalf("valid storage env rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=local\nFAAS_OCI_REGISTRY=https://registry.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "BACKEND=oci") {
		t.Fatalf("local storage env error = %v", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=snap/,base/\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "snap/") {
		t.Fatalf("snap prefix error = %v", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=none\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\nFAAS_STORAGE_CACHE_DIR=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "CACHE_DIR") {
		t.Fatalf("empty cache dir error = %v", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=none\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\nFAAS_STORAGE_CACHE_DIR=/srv/custom-cache\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "managed systemd units") {
		t.Fatalf("non-canonical cache path error = %v, want managed-unit guard", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_OCI_REGISTRY=https://registry.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "LOCAL_PREFIXES=none") {
		t.Fatalf("incomplete strict contract error = %v", err)
	}
}

func TestScopeDoctorNodes_IsNodeLocal(t *testing.T) {
	rows := []releaseinstall.ComputeNodeRow{
		{Name: "fsn-2.faas"},
		{Name: "fsn-3.faas"},
	}
	scoped, found := scopeDoctorNodes(rows, "fsn-3.faas")
	if !found || len(scoped) != 1 || scoped[0].Name != "fsn-3.faas" {
		t.Fatalf("scopeDoctorNodes = %#v, found=%v", scoped, found)
	}
	if scoped, found := scopeDoctorNodes(rows, "missing.faas"); found || len(scoped) != 0 {
		t.Fatalf("missing node scope = %#v, found=%v; want not found", scoped, found)
	}
}

func TestReleaseAssetPath_FindsCanonicalSibling(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, releaseTarballName)
	if err := os.WriteFile(filepath.Join(dir, releaseSigName), []byte("sig"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := releaseAssetPath(tarball, releaseSigName)
	if err != nil {
		t.Fatalf("releaseAssetPath: %v", err)
	}
	if got != filepath.Join(dir, releaseSigName) {
		t.Errorf("path = %q", got)
	}
}

func TestCopyTrustBundleNeverCopiesCAKey(t *testing.T) {
	source := t.TempDir()
	caCert, caKey, err := pki.EnsureCA(source, false)
	if err != nil {
		t.Fatal(err)
	}
	extra := pki.AltNames{DNSNames: []string{"fsn-2.gregale.dev"}}
	for _, role := range pki.RolesForBox(roleComputeOnly) {
		var err error
		if role.Directory == "vmmd" {
			err = pki.EnsureLeafWithCNAndSANs(source, role, "fsn-2.faas", caCert, caKey, false, extra)
		} else {
			err = pki.EnsureLeafWithSANs(source, role, caCert, caKey, false, extra)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "trust")
	if err := copyTrustBundle(source, destination, roleComputeOnly, extra, "fsn-2.faas"); err != nil {
		t.Fatalf("copyTrustBundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "ca", "ca.key")); !os.IsNotExist(err) {
		t.Fatalf("destination CA key stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "ca", "ca.crt")); err != nil {
		t.Fatalf("destination CA cert: %v", err)
	}
}

func TestCopyTrustBundleIssuesMissingEndpointSANLocally(t *testing.T) {
	source := t.TempDir()
	caCert, caKey, err := pki.EnsureCA(source, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range pki.RolesForBox(roleComputeOnly) {
		if err := pki.EnsureLeaf(source, role, caCert, caKey, false); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "trust")
	extra := pki.AltNames{DNSNames: []string{"fsn-3.gregale.dev"}}
	if err := copyTrustBundle(source, destination, roleComputeOnly, extra, "fsn-2.faas"); err != nil {
		t.Fatalf("copyTrustBundle: %v", err)
	}
	if err := pki.ValidateTrustBundleForNode(destination, roleComputeOnly, extra, "fsn-2.faas"); err != nil {
		t.Fatalf("destination trust bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "ca", "ca.key")); !os.IsNotExist(err) {
		t.Fatalf("destination CA key stat = %v, want not exist", err)
	}
}

func TestVerifyAndActivateJoinedNodeUsesControlPlaneRow(t *testing.T) {
	st := state.NewMemStore()
	role := roleComputeOnly
	release := "abcdef"
	hash := "sha256:" + strings.Repeat("a", 64)
	certificate := "-----BEGIN CERTIFICATE-----\njoined-node\n-----END CERTIFICATE-----"
	fingerprint := strings.Repeat("b", 64)
	row, err := st.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:            "fsn-2.faas",
		TargetURL:       "tcp://fsn-2.gregale.dev:50051",
		Role:            &role,
		ReleaseID:       &release,
		ManifestHash:    &hash,
		HostCertificate: &certificate,
		CertFingerprint: &fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetComputeNodeActive(context.Background(), row.ID, false); err != nil {
		t.Fatal(err)
	}
	old := computeNodesStoreOpener
	t.Cleanup(func() { computeNodesStoreOpener = old })
	computeNodesStoreOpener = func() (state.Store, func(), error) { return st, func() {}, nil }
	report := &deployJoinReport{DatabaseNode: "fsn-2.faas", ReleaseGitSHA: release}
	if err := verifyAndActivateJoinedNode(context.Background(), report, hash); err != nil {
		t.Fatalf("verifyAndActivateJoinedNode: %v", err)
	}
	got, err := st.ComputeNodeByName(context.Background(), "fsn-2.faas")
	if err != nil || !got.Active {
		t.Fatalf("row after activation = %#v, err=%v", got, err)
	}
}

func TestVerifyAndActivateJoinedNodeRejectsUnstampedIdentity(t *testing.T) {
	st := state.NewMemStore()
	role := roleComputeOnly
	release := "abcdef"
	hash := "sha256:" + strings.Repeat("a", 64)
	row, err := st.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:         "fsn-2.faas",
		TargetURL:    "tcp://fsn-2.gregale.dev:50051",
		Role:         &role,
		ReleaseID:    &release,
		ManifestHash: &hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetComputeNodeActive(context.Background(), row.ID, false); err != nil {
		t.Fatal(err)
	}
	old := computeNodesStoreOpener
	t.Cleanup(func() { computeNodesStoreOpener = old })
	computeNodesStoreOpener = func() (state.Store, func(), error) { return st, func() {}, nil }
	report := &deployJoinReport{DatabaseNode: "fsn-2.faas", ReleaseGitSHA: release}
	if err := verifyAndActivateJoinedNode(context.Background(), report, hash); err == nil || !strings.Contains(err.Error(), "host_certificate is empty") {
		t.Fatalf("verifyAndActivateJoinedNode error = %v, want unstamped identity guard", err)
	}
}

func TestDeployJoinApply_RendersProviderConnectionOverride(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	ansibleDir := filepath.Join(repo, "deploy", "ansible")
	if err := os.MkdirAll(ansibleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ansibleDir, "node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	artifactDir := t.TempDir()
	tarball := filepath.Join(artifactDir, releaseTarballName)
	if err := os.WriteFile(tarball, []byte("tarball"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{releaseSigName, releaseSBOMName} {
		if err := os.WriteFile(filepath.Join(artifactDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bootstrap := filepath.Join(artifactDir, "gregalectl")
	cosign := filepath.Join(artifactDir, "cosign")
	for _, path := range []string{bootstrap, cosign} {
		if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	computeDBEnv := filepath.Join(artifactDir, "compute-db.env")
	if err := os.WriteFile(computeDBEnv, []byte("DATABASE_URL=postgres://faas@example/faas\nFAAS_VMMD_DBURL=postgres://faas@example/faas\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	storageEnv := filepath.Join(artifactDir, "storage.env")
	if err := os.WriteFile(storageEnv, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=none\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeBasesEnv := filepath.Join(artifactDir, "runtime-bases.env")
	if err := os.WriteFile(runtimeBasesEnv, []byte(
		"FAAS_DEPLOY_BASE_REF_MINIMAL=ghcr.io/example/base-minimal@sha256:0000000000000000000000000000000000000000000000000000000000000000\n"+
			"FAAS_DEPLOY_BASE_REF_NODE22=ghcr.io/example/runner-node22@sha256:1111111111111111111111111111111111111111111111111111111111111111\n"+"FAAS_DEPLOY_BASE_REF_PYTHON312=ghcr.io/example/runner-python312@sha256:2222222222222222222222222222222222222222222222222222222222222222\n"+"FAAS_DEPLOY_BASE_REF_GO124=ghcr.io/example/runner-go124@sha256:3333333333333333333333333333333333333333333333333333333333333333\n"+"FAAS_DEPLOY_BASE_REF_GO124_ALPINE=ghcr.io/example/runner-go124-alpine@sha256:4444444444444444444444444444444444444444444444444444444444444444\n"+"FAAS_DEPLOY_BASE_REF_NODE24=ghcr.io/example/runner-node24@sha256:5555555555555555555555555555555555555555555555555555555555555555\n"+"FAAS_DEPLOY_BASE_REF_PYTHON313=ghcr.io/example/runner-python313@sha256:6666666666666666666666666666666666666666666666666666666666666666\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	signKey := filepath.Join(artifactDir, "sign.key")
	verifyKey := filepath.Join(artifactDir, "sign-pub.pem")
	for _, path := range []string{signKey, verifyKey} {
		if err := os.WriteFile(path, []byte("key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pkiDir := filepath.Join(artifactDir, "pki")
	caCertObj, caKeyObj, err := pki.EnsureCA(pkiDir, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range pki.RolesForBox(roleComputeOnly) {
		var err error
		if role.Directory == "vmmd" {
			err = pki.EnsureLeafWithCNAndSANs(pkiDir, role, "fsn-2.faas", caCertObj, caKeyObj, false, pki.AltNames{DNSNames: []string{"fsn-2.gregale.dev"}})
		} else {
			err = pki.EnsureLeafWithSANs(pkiDir, role, caCertObj, caKeyObj, false, pki.AltNames{DNSNames: []string{"fsn-2.gregale.dev"}})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_, caKey := pki.CARoot(pkiDir)
	if err := os.Remove(caKey); err != nil {
		t.Fatal(err)
	}

	oldRunner := ansiblePlaybookRunner
	oldVerifier := joinControlPlaneVerifier
	oldRegistrar := joinReleaseBundleRegistrar
	t.Cleanup(func() {
		ansiblePlaybookRunner = oldRunner
		joinControlPlaneVerifier = oldVerifier
		joinReleaseBundleRegistrar = oldRegistrar
	})
	joinControlPlaneVerifier = func(context.Context, *deployJoinReport, string) error { return nil }
	joinReleaseBundleRegistrar = func(context.Context, string, string, string) error { return nil }
	var calls [][]string
	ansiblePlaybookRunner = func(_ context.Context, _ string, args []string) error {
		calls = append(calls, append([]string(nil), args...))
		inventory := ""
		for i := range args {
			if args[i] == "-i" && i+1 < len(args) {
				inventory = args[i+1]
			}
		}
		if inventory == "" {
			return fmt.Errorf("fake runner: missing inventory")
		}
		hostVars, err := os.ReadFile(filepath.Join(filepath.Dir(inventory), "host_vars", "fsn-2.yml"))
		if err != nil {
			return err
		}
		body := string(hostVars)
		if !strings.Contains(body, `ansible_host: "203.0.113.27"`) {
			return fmt.Errorf("provider SSH address missing from generated host vars:\n%s", body)
		}
		if !strings.Contains(body, `faas_vmmd_target_url: "tcp://fsn-2.gregale.dev:50051"`) {
			return fmt.Errorf("stable runtime endpoint was overwritten:\n%s", body)
		}
		return nil
	}

	report, err := deployJoinValidate(deployJoinOptions{
		ManifestFile:          manifestPath,
		Node:                  "fsn-2",
		SSHHost:               "203.0.113.27",
		ReleaseTarball:        tarball,
		BootstrapBinary:       bootstrap,
		CosignBinary:          cosign,
		PKISource:             pkiDir,
		SignKeySource:         signKey,
		VerifyKeySource:       verifyKey,
		ComputeDBEnvSource:    computeDBEnv,
		StorageEnvSource:      storageEnv,
		RuntimeBasesEnvSource: runtimeBasesEnv,
		RepoRoot:              repo,
		SkipFleetPreflight:    true,
	})
	if err != nil {
		t.Fatalf("deployJoinValidate: %v", err)
	}
	if code, err := deployJoinApply(&deployJoinOptions{
		ManifestFile:          manifestPath,
		Node:                  "fsn-2",
		SSHHost:               "203.0.113.27",
		SSHUser:               "root",
		SSHPort:               22,
		ReleaseTarball:        tarball,
		BootstrapBinary:       bootstrap,
		CosignBinary:          cosign,
		PKISource:             pkiDir,
		SignKeySource:         signKey,
		VerifyKeySource:       verifyKey,
		ComputeDBEnvSource:    computeDBEnv,
		StorageEnvSource:      storageEnv,
		RuntimeBasesEnvSource: runtimeBasesEnv,
		RepoRoot:              repo,
		SkipFleetPreflight:    true,
	}, &report); err != nil || code != 0 {
		t.Fatalf("deployJoinApply: code=%d err=%v", code, err)
	}
	if len(calls) != 2 {
		t.Fatalf("Ansible calls = %d, want control-plane convergence plus limited join", len(calls))
	}
	controlPlane := strings.Join(calls[0], " ")
	if !strings.Contains(controlPlane, "--limit control_plane") || !strings.Contains(controlPlane, "node_join_control_plane.yml") {
		t.Fatalf("Ansible args missing control-plane limit/playbook: %v", calls[0])
	}
	joined := strings.Join(calls[1], " ")
	if !strings.Contains(joined, "--limit fsn-2") || !strings.Contains(joined, "node_join.yml") {
		t.Fatalf("Ansible args missing node limit/playbook: %v", calls[1])
	}
	if !report.Applied {
		t.Fatal("apply report was not marked applied")
	}
}
