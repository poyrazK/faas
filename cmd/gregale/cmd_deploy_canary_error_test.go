package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestDeployCanaryFlagErrorIsReadable — production-us printed
// `canary preset "--canary-stages requires --canary-preset=custom" is not in
// the closed-set catalog`: the flag validation message was passed where a
// preset name belongs. The message is now the problem detail.
func TestDeployCanaryFlagErrorIsReadable(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &stdout, &stderr
	defer func() { osStdout, osStderr = oldOut, oldErr }()

	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--name", "my-app", "--canary-stages", "1@30s,100@0s"}); code == 0 {
		t.Fatal("deploy with --canary-stages and no preset succeeded")
	}
	got := stderr.String() + stdout.String()
	if !strings.Contains(got, "--canary-stages requires --canary-preset=custom") || strings.Contains(got, "closed-set catalog") {
		t.Fatalf("canary flag error is garbled:\n%s", got)
	}
}
