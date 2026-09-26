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

func TestPythonFunctionAdapterStructuredLogReachesStderr(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	dir := t.TempDir()
	impl := `async def handler(event, ctx):
    ctx.log.info("function invoked", extra={"invocation_id": ctx.invocation_id})
    return {"statusCode": 200, "body": {"ok": True}}
`
	if err := os.WriteFile(filepath.Join(dir, ".faas-handler.py"), []byte(impl), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "handler.py")
	if err := os.WriteFile(adapter, []byte(pythonFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, adapter)
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
	if !scan.Scan() || !strings.Contains(scan.Text(), `"__faas_ready": true`) {
		t.Fatalf("ready frame = %q, error = %v", scan.Text(), scan.Err())
	}
	envelope, _ := json.Marshal(map[string]any{
		"method": "POST", "path": "/", "body_b64": "",
		"headers": map[string]string{"x-faas-invocation-id": "inv-log"},
	})
	if _, err := stdin.Write(append(envelope, '\n')); err != nil {
		t.Fatal(err)
	}
	if !scan.Scan() {
		t.Fatalf("read response: %v; stderr=%s", scan.Err(), stderr.String())
	}
	var response struct {
		Status  int    `json:"status"`
		BodyB64 string `json:"body_b64"`
	}
	if err := json.Unmarshal(scan.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	body, _ := base64.StdEncoding.DecodeString(response.BodyB64)
	if response.Status != 200 || !strings.Contains(string(body), `"ok": true`) {
		t.Fatalf("response = %d %s", response.Status, body)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("adapter exit: %v; stderr=%s", err, stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, "function invoked") || !strings.Contains(got, `"invocation_id": "inv-log"`) {
		t.Fatalf("stderr = %q", got)
	}
}

// TestPythonFunctionAdapterHandlerExceptionIsTerminalAndWorkerSurvives — the
// persistent Python adapter had no exception handling: a raising handler (or a
// non-UTF-8 body) escaped the request loop and killed the worker. The runner
// then answered a plain-text 500 that gatewayd-internal classifies as a
// retryable infrastructure failure, so the durable queue re-ran the failing
// handler. Like the Node adapter, it must answer a terminal handler_error
// envelope and keep serving. The event also carries the spec's body_b64.
func TestPythonFunctionAdapterHandlerExceptionIsTerminalAndWorkerSurvives(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	dir := t.TempDir()
	impl := `def handler(event, ctx):
    if event["path"] == "/boom":
        raise ValueError("customer bug")
    return {"statusCode": 200, "body": {"body_b64": event["body_b64"], "body_type": type(event["body"]).__name__}}
`
	if err := os.WriteFile(filepath.Join(dir, ".faas-handler.py"), []byte(impl), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "handler.py")
	if err := os.WriteFile(adapter, []byte(pythonFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, adapter)
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
	if !scan.Scan() {
		t.Fatalf("no ready frame: %v", scan.Err())
	}
	type response struct {
		Status  int    `json:"status"`
		BodyB64 string `json:"body_b64"`
	}
	call := func(path string, body []byte) (int, string) {
		t.Helper()
		envelope, _ := json.Marshal(map[string]any{
			"method": "POST", "path": path, "body_b64": base64.StdEncoding.EncodeToString(body),
			"headers": map[string]string{"x-faas-invocation-id": "inv-1"},
		})
		if _, err := stdin.Write(append(envelope, '\n')); err != nil {
			t.Fatalf("write %s: %v (worker gone?) stderr=%s", path, err, stderr.String())
		}
		if !scan.Scan() {
			t.Fatalf("no response to %s: %v; stderr=%s", path, scan.Err(), stderr.String())
		}
		var r response
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		decoded, _ := base64.StdEncoding.DecodeString(r.BodyB64)
		return r.Status, string(decoded)
	}

	status, body := call("/boom", []byte(`{"a":1}`))
	var failure struct {
		Error        string `json:"error"`
		Message      string `json:"message"`
		InvocationID string `json:"invocation_id"`
	}
	if err := json.Unmarshal([]byte(body), &failure); err != nil || status != 500 ||
		failure.Error != "handler_error" || failure.Message != "customer bug" || failure.InvocationID != "inv-1" {
		t.Fatalf("raising handler -> %d %s, want 500 handler_error envelope", status, body)
	}
	binary := []byte{0xff, 0xfe, 0x00, 0x80}
	status, body = call("/ok", binary)
	if status != 200 || !strings.Contains(body, base64.StdEncoding.EncodeToString(binary)) {
		t.Fatalf("worker did not survive / body_b64 missing: %d %s", status, body)
	}
	status, body = call("/ok", []byte(`{"a":1}`))
	if status != 200 || !strings.Contains(body, `"body_type": "dict"`) {
		t.Fatalf("JSON body -> %d %s, want parsed dict", status, body)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("adapter exit: %v; stderr=%s", err, stderr.String())
	}
}
