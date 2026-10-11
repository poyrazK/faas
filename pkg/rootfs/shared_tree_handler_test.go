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

func TestFunctionAdapterVariants(t *testing.T) {
	if !strings.Contains(nodeFunctionAdapter, nodeAdapterHandlerImport) {
		t.Fatal("node adapter no longer contains the handler import line the variant rewrites")
	}
	if !strings.Contains(pythonFunctionAdapter, pythonAdapterHandlerImport) {
		t.Fatal("python adapter no longer contains the handler path line the variant rewrites")
	}
	if nodeFunctionAdapterFor("handler.js") != nodeFunctionAdapter {
		t.Fatal("the default Node handler must keep the historical adapter byte for byte")
	}
	node := nodeFunctionAdapterFor("api/resize.js")
	if !strings.Contains(node, `"api/resize.js")`) || strings.Contains(node, nodeAdapterHandlerImport) {
		t.Fatal("node adapter variant does not import the configured handler")
	}
	python := pythonFunctionAdapterFor("api/digest.py")
	if !strings.Contains(python, `"api/digest.py")`) || !strings.Contains(python, "sys.path.insert(0, os.path.dirname(handler_path))") {
		t.Fatal("python adapter variant does not load the configured handler in place")
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// invokeAdapter starts a persistent adapter, sends one envelope and returns
// the response status and decoded body.
func invokeAdapter(t *testing.T, interpreter, adapter, path string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, interpreter, adapter)
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
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() || !strings.Contains(scan.Text(), "__faas_ready") {
		t.Fatalf("ready frame = %q, error = %v, stderr = %s", scan.Text(), scan.Err(), stderr.String())
	}
	envelope, _ := json.Marshal(map[string]any{"method": "GET", "path": path, "body_b64": "", "headers": map[string]string{}})
	if _, err := stdin.Write(append(envelope, '\n')); err != nil {
		t.Fatal(err)
	}
	if !scan.Scan() {
		t.Fatalf("read response: %v; stderr = %s", scan.Err(), stderr.String())
	}
	var response struct {
		Status  int    `json:"status"`
		BodyB64 string `json:"body_b64"`
	}
	if err := json.Unmarshal(scan.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", scan.Text(), err)
	}
	body, _ := base64.StdEncoding.DecodeString(response.BodyB64)
	return response.Status, string(body)
}

// ADR-985: a Node function whose handler lives in a shared tree is served
// through the adapter, keeps its relative imports, and does not run another
// function's default handler.js.
func TestNormalizeFunctionHandlerFromSharedTreeNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	staging := t.TempDir()
	writeTree(t, staging, map[string]string{
		"app/package.json":  `{"type":"module"}`,
		"app/lib/greet.js":  `export const greet = (p) => "hi " + p;`,
		"app/api/resize.js": `import { greet } from "../lib/greet.js";` + "\nexport async function handler(event) { return {statusCode: 200, body: {msg: greet(event.path)}}; }\n",
		"app/handler.js":    "export async function handler() { return {statusCode: 500, body: 'wrong handler'}; }\n",
	})
	if err := NormalizeFunctionHandlerFrom(staging, "/app/api/resize.js", "/app/node24.js"); err != nil {
		t.Fatal(err)
	}
	status, body := invokeAdapter(t, node, filepath.Join(staging, "app", "node24.js"), "/x")
	if status != 200 || !strings.Contains(body, "hi /x") {
		t.Fatalf("response = %d %s", status, body)
	}
}

// ADR-985: a Python function in a shared tree is loaded in place, so it can
// import a sibling module.
func TestNormalizeFunctionHandlerFromSharedTreePython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	staging := t.TempDir()
	writeTree(t, staging, map[string]string{
		"app/api/helper.py": "def double(x):\n    return 2 * x\n",
		"app/api/digest.py": "from helper import double\n\ndef handler(event, ctx):\n    return {\"statusCode\": 200, \"body\": {\"n\": double(21)}}\n",
		"app/handler.py":    "def handler(event, ctx):\n    return {\"statusCode\": 500, \"body\": \"wrong handler\"}\n",
	})
	if err := NormalizeFunctionHandlerFrom(staging, "/app/api/digest.py", "/app/handler.py"); err != nil {
		t.Fatal(err)
	}
	status, body := invokeAdapter(t, python, filepath.Join(staging, "app", "handler.py"), "/")
	if status != 200 || !strings.Contains(body, `"n": 42`) {
		t.Fatalf("response = %d %s", status, body)
	}
}
