package ci

import (
	"os/exec"
	"testing"
)

func TestNpmPayloadVerifier(t *testing.T) {
	cmd := exec.Command("bash", "check-npm-payload_test.sh")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm payload verifier regression test failed: %v\n%s", err, output)
	}
}
