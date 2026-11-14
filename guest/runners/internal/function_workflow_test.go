package internal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/fnadapter"
)

// nodeWorkflowSDKStub stands in for @gregale/sdk-node/workflows: it runs the
// definition with a step that calls through, so the test sees the adapter's
// wiring (definition, name, input, headers) without building the SDK.
const nodeWorkflowSDKStub = `export function defineWorkflow(name, run) { return { name, run }; }
export function serveWorkflows(defs) {
  return async (request) => {
    if (request.headers.get("x-faas-workflow-mode") !== "code" || !request.headers.get("x-faas-workflow-run-id")) {
      return Response.json({ error: "refused" }, { status: 403 });
    }
    const body = await request.json();
    const def = defs.find((d) => d.name === body.workflow);
    const output = await def.run(body.input, { step: async (_name, fn) => fn({}) });
    return Response.json({ status: "succeeded", output, name: def.name });
  };
}
`

func workflowRequest(t *testing.T, url string, headers map[string]string) (int, string) {
	t.Helper()
	body := `{"protocol":"gregale.workflow.code/v1","run_id":"run-1","workflow":"double","input":{"n":21},"invocation":1,"journal":[]}`
	req, _ := http.NewRequest(http.MethodPost, url+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func serveAdapter(t *testing.T, runtime, bin, adapter string, env []string) string {
	t.Helper()
	spec := workerpool.Spec{Executable: bin, Args: []string{adapter, t.Name()}, Env: env, HandlerPath: adapter}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Serve(w, r, ServeConfig{Runtime: runtime, Spec: spec})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

var engineHeaders = map[string]string{"X-Faas-Workflow-Mode": "code", "X-Faas-Workflow-Run-Id": "run-1"}

// ADR-20261113120824 §1: an engine call to a Node workflow function runs its
// handler(input, wf) under the SDK's protocol; without the SDK the adapter
// answers workflow_sdk_missing; a plain request still reaches the handler.
func TestNodeAdapterServesWorkflowFunctions(t *testing.T) {
	bin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	handler := `export default async function handler(input, wf) {
  if (!wf || typeof wf.step !== "function") return { body: { plain: true } };
  return { doubled: await wf.step("double", () => input.n * 2) };
}
`
	write := func(dir, name, body string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setup := func(withSDK bool) string {
		dir := t.TempDir()
		write(dir, "adapter.mjs", fnadapter.Node)
		write(dir, "handler.js", handler)
		write(dir, "package.json", `{"type":"module"}`)
		if withSDK {
			write(dir, "node_modules/@gregale/sdk-node/package.json",
				`{"name":"@gregale/sdk-node","type":"module","exports":{"./workflows":"./workflows.js"}}`)
			write(dir, "node_modules/@gregale/sdk-node/workflows.js", nodeWorkflowSDKStub)
		}
		return serveAdapter(t, "node24", bin, filepath.Join(dir, "adapter.mjs"), os.Environ())
	}

	url := setup(true)
	status, body := workflowRequest(t, url, engineHeaders)
	var got struct {
		Status string         `json:"status"`
		Name   string         `json:"name"`
		Output map[string]int `json:"output"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil || got.Output["doubled"] != 42 || got.Name != "double" {
		t.Fatalf("workflow call = %d %s, want the handler's output under the definition named by the run", status, body)
	}
	if status, body := workflowRequest(t, url, nil); status != http.StatusOK || !strings.Contains(body, `"plain":true`) {
		t.Fatalf("plain request = %d %s, want the ordinary handler path", status, body)
	}

	missing := setup(false)
	if status, body := workflowRequest(t, missing, engineHeaders); status != http.StatusInternalServerError || !strings.Contains(body, "workflow_sdk_missing") {
		t.Fatalf("without the SDK = %d %s, want 500 workflow_sdk_missing", status, body)
	}
}

// The Python adapter runs handler(input, wf) under faas_sdk's real protocol
// core: the first call runs the new step and reports its result.
func TestPythonAdapterServesWorkflowFunctions(t *testing.T) {
	bin, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	sdk, err := filepath.Abs("../../../sdk/python")
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PYTHONPATH="+sdk)
	check := exec.Command(bin, "-c", "import faas_sdk.workflows")
	check.Env = env
	if check.Run() != nil {
		t.Skip("faas_sdk does not import here (its dependencies are not installed)")
	}
	dir := t.TempDir()
	handler := "async def handler(input, wf):\n" +
		"    if not hasattr(wf, 'step'):\n        return {'plain': True}\n" +
		"    doubled = await wf.step('double', lambda ctx: input['n'] * 2)\n" +
		"    return {'doubled': doubled}\n"
	for name, body := range map[string]string{"adapter.py": fnadapter.Python, ".faas-handler.py": handler} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	url := serveAdapter(t, "python312", bin, filepath.Join(dir, "adapter.py"), env)
	status, body := workflowRequest(t, url, engineHeaders)
	var got struct {
		Ops []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Output int    `json:"output"`
		} `json:"ops"`
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil || len(got.Ops) != 1 ||
		got.Ops[0].Name != "double" || got.Ops[0].Status != "succeeded" || got.Ops[0].Output != 42 {
		t.Fatalf("workflow call = %d %s, want the double step reported with its result", status, body)
	}
	if status, body := workflowRequest(t, url, map[string]string{"X-Faas-Workflow-Mode": "code"}); status != http.StatusForbidden {
		t.Fatalf("call without the run id = %d %s, want 403", status, body)
	}
}
