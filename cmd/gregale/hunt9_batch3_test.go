package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Hunt #9 H9-62: `gregale mcp init --path ./mcp` printed "cd <dest> && npm ci"
// instead of the directory it had just created.
func TestRunCmdInitNextStepsShowDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "h9-starter")
	var stdout, stderr bytes.Buffer
	if code := runCmdInit("mcp-node", dest, false, "", &stdout, &stderr); code != 0 {
		t.Fatalf("runCmdInit = %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "<dest>") {
		t.Fatalf("next steps still contain the <dest> placeholder:\n%s", out)
	}
	if !strings.Contains(out, "cd "+dest) {
		t.Fatalf("next steps do not name %s:\n%s", dest, out)
	}
}

// Hunt #9 H9-63: a failed `mcp deploy` submission told the user to retry with
// --idempotency-key, which mcp deploy rejected as an unknown flag.
func TestMCPDeployArgsCarryIdempotencyKey(t *testing.T) {
	args := mcpDeployArgs(mcpDeployOptions{slug: "h9-mcp", timeout: 60, idempotencyKey: "gregale-deploy-abc"}, "/src")
	i := slices.Index(args, "--idempotency-key")
	if i < 0 || i+1 >= len(args) || args[i+1] != "gregale-deploy-abc" {
		t.Fatalf("deploy args = %v, want --idempotency-key gregale-deploy-abc", args)
	}
	if slices.Contains(mcpDeployArgs(mcpDeployOptions{slug: "h9-mcp", timeout: 60}, "/src"), "--idempotency-key") {
		t.Fatal("an unset key must not be passed")
	}
}
