package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// `mcp tools <slug>` addresses the app like the other leaves (H5-3); a stray
// positional after the flags and a conflicting --app are still refused, each
// with its own message.
func TestMCPRemoteLeavesAcceptLeadingSlug(t *testing.T) {
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "test-token")

	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{name: "trailing positional", args: []string{"my-mcp", "--timeout", "1s", "extra"}, want: "unexpected positional arguments: extra"},
		{name: "conflicting app", args: []string{"my-mcp", "--app", "other"}, want: "--app"},
		{name: "nonpositive timeout", args: []string{"my-mcp", "--timeout", "0s"}, want: "--timeout must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			osStdout, osStderr, jsonOutput = io.Discard, &stderr, false
			if code := cmdMCPRemote("tools", tc.args); code == 0 {
				t.Fatal("invalid invocation exited 0")
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want it to mention %q", stderr.String(), tc.want)
			}
		})
	}

	var stderr bytes.Buffer
	osStdout, osStderr, jsonOutput = io.Discard, &stderr, false
	_ = cmdMCPRemote("tools", []string{"my-mcp", "--timeout", "1s"})
	if strings.Contains(stderr.String(), "Invalid MCP flags") {
		t.Fatalf("leading slug was refused: %q", stderr.String())
	}
}
