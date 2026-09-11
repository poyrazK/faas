package rootfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNodeFunctionAdapterContainsHandlerFailureAndKeepsWorkerAlive(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := `export async function handler(event, ctx) {
  console.log("handling", event.path);
  if (event.path === "/fail") throw new Error("deliberate boom");
  if (event.path === "/fail-long") throw new Error("x".repeat(1024));
  return {statusCode: 201, body: {ok: true, invocation_id: ctx.invocation_id}};
}`
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.mjs")
	if err := os.WriteFile(adapter, []byte(nodeFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, adapter)
	cmd.Env = append(os.Environ(), "FAAS_PERSISTENT_WORKER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() || !strings.Contains(scan.Text(), `"__faas_ready":true`) {
		t.Fatalf("ready frame = %q, scan error = %v, stderr = %s", scan.Text(), scan.Err(), stderr.String())
	}

	type response struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		BodyB64 string            `json:"body_b64"`
	}
	send := func(path, id string) response {
		t.Helper()
		envelope, _ := json.Marshal(map[string]any{
			"method": "POST", "path": path, "body_b64": "",
			"headers": map[string]string{"x-faas-invocation-id": id},
		})
		if _, err := stdin.Write(append(envelope, '\n')); err != nil {
			t.Fatalf("write invocation: %v", err)
		}
		if !scan.Scan() {
			t.Fatalf("read response: %v; stderr=%s", scan.Err(), stderr.String())
		}
		var got response
		if err := json.Unmarshal(scan.Bytes(), &got); err != nil {
			t.Fatalf("decode response %q: %v", scan.Text(), err)
		}
		return got
	}

	failed := send("/fail", "inv-fail")
	failedBody, _ := base64.StdEncoding.DecodeString(failed.BodyB64)
	if failed.Status != 500 || !strings.Contains(string(failedBody), "deliberate boom") ||
		!strings.Contains(string(failedBody), "inv-fail") {
		t.Fatalf("failed response = status %d body %s", failed.Status, failedBody)
	}
	if got := failed.Headers["content-type"]; got != "application/json; charset=utf-8" {
		t.Fatalf("failed content-type = %q", got)
	}
	longFailure := send("/fail-long", "inv-fail-long")
	longFailureBody, _ := base64.StdEncoding.DecodeString(longFailure.BodyB64)
	if longFailure.Status != 500 || len(longFailureBody) > 400 {
		t.Fatalf("long failure was not bounded: status %d body bytes %d", longFailure.Status, len(longFailureBody))
	}

	// The same persistent worker must accept the next request. Before the fix,
	// the outer async function stopped reading stdin and the caller timed out.
	succeeded := send("/ok", "inv-ok")
	succeededBody, _ := base64.StdEncoding.DecodeString(succeeded.BodyB64)
	if succeeded.Status != 201 || !strings.Contains(string(succeededBody), `"ok":true`) ||
		!strings.Contains(string(succeededBody), "inv-ok") {
		t.Fatalf("success response = status %d body %s", succeeded.Status, succeededBody)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("node adapter exit: %v; stderr=%s", err, stderr.String())
	}
}
