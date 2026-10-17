package cisetup

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedChecksumStepVerifiesPreparedBinaryWithoutExecutingIt(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("generated Ubuntu step requires python3")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("generated Ubuntu step requires bash")
	}
	_, trust, _, req := setupFixture()
	script := buildWorkflow(trust, req.Config).Jobs["frontend"].Steps[2].Run
	binary := filepath.Join(t.TempDir(), "gregale")
	body := []byte("#!/bin/sh\nexit 99\n")
	if err := os.WriteFile(binary, body, 0700); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	for _, test := range []struct {
		name, digest string
		pass         bool
	}{{"match", digest, true}, {"mismatch", strings.Repeat("0", 64), false}} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "GREGALE_CLI_PATH="+binary, "GREGALE_CLI_SHA256="+test.digest)
			out, err := cmd.CombinedOutput()
			if (err == nil) != test.pass {
				t.Fatalf("checksum outcome: %v %s", err, out)
			}
		})
	}
	if err := os.Chmod(binary, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "GREGALE_CLI_PATH="+binary, "GREGALE_CLI_SHA256="+digest)
	if err := cmd.Run(); err == nil {
		t.Fatal("non-executable prepared CLI accepted")
	}
}
