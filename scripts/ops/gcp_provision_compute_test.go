package ops_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGCPProvisionComputeOperatorHandoff(t *testing.T) {
	cmd := exec.Command("bash", "gcp_provision_compute_test.sh")
	cmd.Dir = "."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcp provision compute regression test failed: %v\n%s", err, output)
	}
}

func TestGCPComputeJoinIgnoresExpectedCacheBindMount(t *testing.T) {
	tasks, err := os.ReadFile("../../deploy/ansible/roles/xfs/tasks/main.yml")
	if err != nil {
		t.Fatalf("read xfs role: %v", err)
	}
	want := `argv: [findmnt, -S, "{{ faas_storage_device }}", -n, -o, TARGET, --first-only]`
	if !strings.Contains(string(tasks), want) {
		t.Fatalf("xfs mount validation must inspect only the filesystem root mount; missing %q", want)
	}
}
