package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployClaimValidateAllowsPolicyAuthorizedDynamicNode(t *testing.T) {
	claimPath := filepath.Join(t.TempDir(), "fsn-4.yaml")
	claim := `api_version: gregale.dev/v1alpha1
kind: ComputeNodeClaim
metadata:
  name: fsn-4
spec:
  ssh:
    host: 10.42.0.4
    host_key_sha256: SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
`
	if err := os.WriteFile(claimPath, []byte(claim), 0o600); err != nil {
		t.Fatal(err)
	}
	_, restore := captureRealStdout(t)
	code := cmdDeployClaim([]string{
		"validate", "--file", claimPath,
		"--manifest-file", dynamicSplitboxJoinManifest(t),
		"--json",
	})
	output := restore()
	if code != 0 {
		t.Fatalf("dynamic policy-authorized claim exit code = %d, want 0", code)
	}
	if !strings.Contains(output, `"valid": true`) || !strings.Contains(output, `"manifest_node": true`) {
		t.Fatalf("dynamic policy claim report = %s", output)
	}
}
