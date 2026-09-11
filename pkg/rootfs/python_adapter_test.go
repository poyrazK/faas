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
