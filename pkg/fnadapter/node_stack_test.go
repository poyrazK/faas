package fnadapter

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Prod hunt r31: a throwing Node handler logged the adapter's own frames
// (invoke, handleOne, inGregaleRequestContext) and node internals under the
// customer's one frame; the log keeps the customer's frames.
func TestNodeHandlerErrorLogsOnlyCustomerFrames(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"node22.js":    Node,
		"handler.js":   "export async function handler() {\n  throw new Error(\"boom from handler\");\n}\n",
		"package.json": `{"type":"module"}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join(dir, "node22.js"))
	cmd.Stdin = strings.NewReader(`{"method":"POST","path":"/","headers":{},"query":"","body_b64":""}` + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("adapter: %v\nstderr:\n%s", err, stderr.String())
	}
	log := stderr.String()
	if !strings.Contains(log, "boom from handler") || !strings.Contains(log, "handler.js:2") {
		t.Fatalf("customer error or frame missing:\n%s", log)
	}
	for _, platform := range []string{"node22.js", "handleOne", "inGregaleRequestContext", "node:internal"} {
		if strings.Contains(log, platform) {
			t.Fatalf("log names platform frame %q:\n%s", platform, log)
		}
	}
	if !strings.Contains(stdout.String(), `"status":500`) {
		t.Fatalf("response = %s, want status 500", stdout.String())
	}
}
