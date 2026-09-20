package ops_test

import (
	"os/exec"
	"testing"
)

func TestPrepareComputeClaimScript(t *testing.T) {
	cmd := exec.Command("bash", "prepare_compute_claim_test.sh")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare compute claim regression test failed: %v\n%s", err, output)
	}
}
