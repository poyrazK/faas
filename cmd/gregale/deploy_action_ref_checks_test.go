package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The deploy Action's ref preflight is shell. Run its regression script under
// go test as well: v0.1.18-rc.215 was the first annotated release tag after
// #3667, and the Action rejected it because an annotated tag's push payload
// names the tag object while GITHUB_SHA names the commit.
func TestDeployActionRefChecks(t *testing.T) {
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	script := filepath.Join("..", "..", "scripts", "ci", "test_deploy_action_head.sh")
	out, err := exec.Command("bash", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
}
