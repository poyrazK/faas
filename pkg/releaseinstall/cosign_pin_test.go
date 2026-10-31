package releaseinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCosignBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cosign")
	if err := os.WriteFile(path, []byte("cosign bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("cosign bytes"))
	if err := CheckCosignBinary(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("matching digest refused: %v", err)
	}
	err := CheckCosignBinary(path, PinnedCosignSHA256)
	if err == nil || !strings.Contains(err.Error(), PinnedCosignURL) || !strings.Contains(err.Error(), hex.EncodeToString(sum[:])) {
		t.Fatalf("mismatch error = %v, want the observed digest and the pinned URL", err)
	}
	if err := CheckCosignBinary(filepath.Join(dir, "absent"), PinnedCosignSHA256); err == nil {
		t.Fatal("a missing binary passed")
	}
}

// Every workflow that downloads cosign must fetch the build gregalectl pins,
// so a host never runs a cosign that deploy prepare would have refused.
func TestPinnedCosignMatchesEveryWorkflowDownload(t *testing.T) {
	workflows, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil || len(workflows) == 0 {
		t.Fatalf("workflows: %v %v", workflows, err)
	}
	checked := 0
	for _, path := range workflows {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, "sigstore/cosign/releases/download") {
			continue
		}
		checked++
		if !strings.Contains(text, PinnedCosignSHA256) {
			t.Errorf("%s downloads cosign without the pinned digest %s", filepath.Base(path), PinnedCosignSHA256)
		}
		if !strings.Contains(text, PinnedCosignVersion) {
			t.Errorf("%s downloads a cosign other than %s", filepath.Base(path), PinnedCosignVersion)
		}
	}
	if checked < 2 {
		t.Fatalf("found %d workflows that download cosign, want release.yml and the CD workflows", checked)
	}
}
