package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

// TestDeployCanaryFlagErrorIsReadable — production-us printed
// `canary preset "--canary-stages requires --canary-preset=custom" is not in
// the closed-set catalog`: the flag validation message was passed where a
// preset name belongs. The message is now the problem detail.
func TestDeployCanaryFlagErrorIsReadable(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
	defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()

	if code := main.CmdDeployTarball([]string{"--image", "registry.x/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--name", "my-app", "--canary-stages", "1@30s,100@0s"}); code == 0 {
		t.Fatal("deploy with --canary-stages and no preset succeeded")
	}
	got := stderr.String() + stdout.String()
	if !strings.Contains(got, "--canary-stages requires --canary-preset=custom") || strings.Contains(got, "closed-set catalog") {
		t.Fatalf("canary flag error is garbled:\n%s", got)
	}
}
