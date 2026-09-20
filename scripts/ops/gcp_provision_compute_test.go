package ops_test

import (
	"os/exec"
	"testing"
)

func TestGCPProvisionComputeOperatorHandoff(t *testing.T) {
	cmd := exec.Command("bash", "gcp_provision_compute_test.sh")
	cmd.Dir = "."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcp provision compute regression test failed: %v\n%s", err, output)
	}
}
