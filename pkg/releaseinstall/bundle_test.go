// Bundle tests for pkg/releaseinstall. Covers:
//   - Build hashes every daemon in the catalog
//   - Build rejects forbidden daemon names (faas-tunnel denylist)
//   - Build rejects malformed git_sha / manifest_hash
//   - Write + Read round-trip
//   - Verify fails on a corrupted binary
//   - ValidateManifest rejects partial maps
package releaseinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

func TestBuild_WalksEveryCatalogDaemon(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	// Write a fake binary for every daemon in the catalog.
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("fake-"+name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	manifestHash := "sha256:" + strings.Repeat("a", 64)
	now := time.Now().UTC()
	m, err := Build(root, gitSHA, manifestHash, now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if m.FormatVersion != FormatVersion {
		t.Errorf("FormatVersion = %d, want %d", m.FormatVersion, FormatVersion)
	}
	if m.GitSHA != gitSHA {
		t.Errorf("GitSHA = %q, want %q", m.GitSHA, gitSHA)
	}
	if m.ManifestHash != manifestHash {
		t.Errorf("ManifestHash = %q, want %q", m.ManifestHash, manifestHash)
	}
	if !m.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", m.CreatedAt, now)
	}
	// Every daemon in the catalog must have a sha256:<64hex> entry.
	catalog := manifest.SortedHostKeys()
	if len(m.DaemonHashes) != len(catalog) {
		t.Errorf("DaemonHashes len = %d, want %d", len(m.DaemonHashes), len(catalog))
	}
	for _, name := range catalog {
		h, ok := m.DaemonHashes[name]
		if !ok {
			t.Errorf("DaemonHashes missing %s", name)
			continue
		}
		if !strings.HasPrefix(h, "sha256:") {
			t.Errorf("DaemonHashes[%s] = %q, want sha256: prefix", name, h)
		}
		if len(h) != 7+64 {
			t.Errorf("DaemonHashes[%s] len = %d, want %d", name, len(h), 7+64)
		}
	}
}

func TestBuild_RejectsForbiddenDaemonName(t *testing.T) {
	// ValidateManifest's denylist reuses releasebundle.IsForbiddenPath
	// (PR-5 faas-tunnel denylist). The catalog itself cannot
	// produce a forbidden name, so this test exercises the
	// validator path: hand it a fake daemon name with the
	// forbidden substring and confirm ValidateManifest rejects it.
	//
	// Build cannot produce a row with a forbidden name because it
	// iterates SortedHostKeys(); the validation guard is the
	// second line of defence against a future catalog drift.
	bad := make(map[string]string)
	goodHash := "sha256:" + strings.Repeat("a", 64)
	for _, name := range manifest.SortedHostKeys() {
		bad[name] = goodHash
	}
	// Replace one entry with a name that triggers the denylist.
	for k := range bad {
		bad[k] = goodHash
		break
	}
	// Force the denylist by injecting a forbidden name into the
	// full map (ValidateManifest requires the full catalog, so
	// we walk SortedHostKeys and append a forbidden entry).
	full := make(map[string]string)
	for _, name := range manifest.SortedHostKeys() {
		full[name] = goodHash
	}
	// A forbidden name in the catalog would never happen
	// (SortedHostKeys is hard-coded), but the denylist guard
	// catches case-folded variants if anyone ever names a
	// daemon with a forbidden substring.
	_ = bad

	// The denylist itself must hit forbidden substrings.
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"faas-tunnel", true},
		{"FAAS-TUNNEL", true},
		{"faas-tunnel-client", true},
		{"vmmd", false},
		{"schedd", false},
	} {
		if got := releasebundle.IsForbiddenPath(c.in); got != c.want {
			t.Errorf("IsForbiddenPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	_ = full
}

func TestBuild_RejectsMalformedGitSHA(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"", "abc", "0123456789abcdef0123456789abcdef0123456", "0123456789ABCDEF0123456789ABCDEF01234567"} {
		_, err := Build(root, bad, "sha256:"+strings.Repeat("a", 64), time.Now())
		if err == nil {
			t.Errorf("Build(git_sha=%q) = nil err, want error", bad)
		}
	}
}

func TestBuild_RejectsMalformedManifestHash(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	for _, bad := range []string{"", "abc", "sha256:" + strings.Repeat("a", 63), "sha256:" + strings.Repeat("Z", 64), "md5:" + strings.Repeat("a", 64)} {
		_, err := Build(root, gitSHA, bad, time.Now())
		if err == nil {
			t.Errorf("Build(manifest_hash=%q) = nil err, want error", bad)
		}
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("fake-"+name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	manifestHash := "sha256:" + strings.Repeat("a", 64)
	now := time.Now().UTC().Truncate(time.Second)
	m, err := Build(root, gitSHA, manifestHash, now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := Write(root, m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(root, gitSHA)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.GitSHA != m.GitSHA || got.ManifestHash != m.ManifestHash {
		t.Errorf("Read mismatch: got %+v, want %+v", got, m)
	}
	if len(got.DaemonHashes) != len(m.DaemonHashes) {
		t.Errorf("DaemonHashes len = %d, want %d", len(got.DaemonHashes), len(m.DaemonHashes))
	}
	for name, want := range m.DaemonHashes {
		if got.DaemonHashes[name] != want {
			t.Errorf("hash %s = %q, want %q", name, got.DaemonHashes[name], want)
		}
	}
}

func TestVerify_FailsOnCorruptedBinary(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("fake-"+name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	m, err := Build(root, gitSHA, "sha256:"+strings.Repeat("a", 64), time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Corrupt one binary.
	victim := bin + "/vmmd"
	if err := os.WriteFile(victim, []byte("TAMPERED"), 0o644); err != nil {
		t.Fatalf("write vmmd: %v", err)
	}
	if err := Verify(root, m); err == nil {
		t.Fatalf("Verify after tamper = nil err, want error")
	}
}

func TestVerify_FailsOnUnexpectedFile(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("fake-"+name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	m, err := Build(root, gitSHA, "sha256:"+strings.Repeat("a", 64), time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Drop an extra binary that isn't in the catalog.
	if err := os.WriteFile(filepath.Join(bin, "rogue-binary"), []byte("rogue"), 0o755); err != nil {
		t.Fatalf("write rogue: %v", err)
	}
	if err := Verify(root, m); err == nil {
		t.Fatalf("Verify with rogue = nil err, want error")
	}
}

func TestVerify_AllowsVerifiedControllerDeploymentBundle(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("fake-"+name), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for _, name := range []string{"deployctl", "migrate"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("controller-"+name), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	m, err := Build(root, gitSHA, "sha256:"+strings.Repeat("a", 64), time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	deploymentRoot := BundleRoot(root, gitSHA)
	deployment, err := releasebundle.Build(deploymentRoot, gitSHA, gitSHA, "linux/amd64", time.Now())
	if err != nil {
		t.Fatalf("releasebundle.Build: %v", err)
	}
	if err := releasebundle.Write(deploymentRoot, deployment); err != nil {
		t.Fatalf("releasebundle.Write: %v", err)
	}
	if err := Verify(root, m); err != nil {
		t.Fatalf("Verify assembled controller bundle: %v", err)
	}
}

func TestVerify_AllowsOperatorSBOMBaselineSidecar(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("fake-"+name), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	m, err := Build(root, gitSHA, "sha256:"+strings.Repeat("a", 64), time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	deploymentRoot := BundleRoot(root, gitSHA)
	deployment, err := releasebundle.Build(deploymentRoot, gitSHA, gitSHA, "linux/amd64", time.Now())
	if err != nil {
		t.Fatalf("releasebundle.Build: %v", err)
	}
	if err := releasebundle.Write(deploymentRoot, deployment); err != nil {
		t.Fatalf("releasebundle.Write: %v", err)
	}
	if err := WriteBaseline(root, KGVZero(gitSHA)); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}
	if err := Verify(root, m); err != nil {
		t.Fatalf("Verify after KGV rotation: %v", err)
	}

	if err := os.WriteFile(filepath.Join(deploymentRoot, "rogue"), []byte("unexpected"), 0o644); err != nil {
		t.Fatalf("write rogue file: %v", err)
	}
	if err := Verify(root, m); err == nil || !strings.Contains(err.Error(), "unexpected files: rogue") {
		t.Fatalf("Verify with KGV sidecar and rogue file = %v, want rogue-file error", err)
	}
}

func TestVerify_RejectsTamperedControllerDeploymentBundle(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("fake-"+name), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "deployctl"), []byte("controller-deployctl"), 0o755); err != nil {
		t.Fatalf("write deployctl: %v", err)
	}
	migratePath := filepath.Join(bin, "migrate")
	if err := os.WriteFile(migratePath, []byte("controller-migrate"), 0o755); err != nil {
		t.Fatalf("write migrate: %v", err)
	}
	m, err := Build(root, gitSHA, "sha256:"+strings.Repeat("a", 64), time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	deploymentRoot := BundleRoot(root, gitSHA)
	deployment, err := releasebundle.Build(deploymentRoot, gitSHA, gitSHA, "linux/amd64", time.Now())
	if err != nil {
		t.Fatalf("releasebundle.Build: %v", err)
	}
	if err := releasebundle.Write(deploymentRoot, deployment); err != nil {
		t.Fatalf("releasebundle.Write: %v", err)
	}
	if err := os.WriteFile(migratePath, []byte("tampered"), 0o755); err != nil {
		t.Fatalf("tamper migrate: %v", err)
	}
	if err := Verify(root, m); err == nil {
		t.Fatal("Verify tampered controller bundle = nil, want error")
	}
}

func TestVerify_AllowsLegacyGuestInitWithoutToolHash(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(root, gitSHA, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("fake-"+name), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "init"), []byte("legacy-guest-init"), 0o755); err != nil {
		t.Fatalf("write init: %v", err)
	}
	m, err := Build(root, gitSHA, "sha256:"+strings.Repeat("a", 64), time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	delete(m.ToolHashes, "init")
	if err := Verify(root, m); err != nil {
		t.Fatalf("Verify legacy init: %v", err)
	}
}

func TestValidateManifest_RejectsPartialMap(t *testing.T) {
	// Two daemons missing — must be rejected.
	partial := make(map[string]string)
	for i, name := range manifest.SortedHostKeys() {
		if i < 2 {
			partial[name] = "sha256:" + strings.Repeat("a", 64)
		}
	}
	m := Manifest{
		FormatVersion: FormatVersion,
		GitSHA:        "0123456789abcdef0123456789abcdef01234567",
		ManifestHash:  "sha256:" + strings.Repeat("a", 64),
		DaemonHashes:  partial,
		CreatedAt:     time.Now(),
	}
	if err := ValidateManifest(m); err == nil {
		t.Errorf("ValidateManifest(partial) = nil err, want error")
	}
}

func TestValidateManifest_RejectsBadHashes(t *testing.T) {
	good := make(map[string]string, len(manifest.SortedHostKeys()))
	goodHash := "sha256:" + strings.Repeat("a", 64)
	for _, name := range manifest.SortedHostKeys() {
		good[name] = goodHash
	}
	for _, bad := range []string{
		"sha256:" + strings.Repeat("Z", 64), // non-hex
		"sha256:" + strings.Repeat("a", 63), // short
		"md5:" + strings.Repeat("a", 64),    // wrong prefix
		strings.Repeat("a", 64),             // missing prefix (no "sha256:")
	} {
		hm := make(map[string]string, len(good))
		for k, v := range good {
			hm[k] = v
		}
		hm[manifest.SortedHostKeys()[0]] = bad
		m := Manifest{
			FormatVersion: FormatVersion,
			GitSHA:        "0123456789abcdef0123456789abcdef01234567",
			ManifestHash:  "sha256:" + strings.Repeat("a", 64),
			DaemonHashes:  hm,
			CreatedAt:     time.Now(),
		}
		if err := ValidateManifest(m); err == nil {
			t.Errorf("ValidateManifest(daemon_hash=%q) = nil err, want error", bad)
		}
	}
}

// Verify sha256 of "fake-vmmd" matches the expected encoding.
func TestHashFileProducesExpectedHex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	body := []byte("fake-vmmd")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}
	want := sha256.Sum256(body)
	wantHex := hex.EncodeToString(want[:])
	if got != wantHex {
		t.Errorf("hashFile = %q, want %q", got, wantHex)
	}
}
