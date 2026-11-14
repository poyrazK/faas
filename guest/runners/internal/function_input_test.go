package internal

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fnadapter"
)

const testInputSchema = `{"type":"object","required":["email"],"additionalProperties":false,` +
	`"properties":{"email":{"type":"string","pattern":"@"},"age":{"type":"integer","minimum":0},` +
	`"tags":{"type":"array","maxItems":2,"items":{"enum":["a","b"]}}}}`

// ADR-20261111150000 §1: the adapter checks a typed-client call's body
// against FAAS_FUNCTION_INPUT_SCHEMA and answers 422 function_input_invalid
// before the handler runs; requests without the client's marker header reach
// the handler unchecked.
func TestAdaptersCheckTypedCallInput(t *testing.T) {
	for _, rt := range []struct {
		name, runtime, interpreter, adapterFile, adapter, handlerFile, handler string
	}{
		{"node", "node24", "node", "adapter.mjs", fnadapter.Node, "handler.js",
			`export default { async fetch() { return Response.json({ ran: true }); } };`},
		{"python", "python312", "python3", "adapter.py", fnadapter.Python, ".faas-handler.py",
			"def handler(event, ctx):\n    return {\"ran\": True}\n"},
	} {
		t.Run(rt.name, func(t *testing.T) {
			bin, err := exec.LookPath(rt.interpreter)
			if err != nil {
				t.Skipf("%s is not installed", rt.interpreter)
			}
			dir := t.TempDir()
			files := map[string]string{rt.adapterFile: rt.adapter, rt.handlerFile: rt.handler, "package.json": `{"type":"module"}`}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			adapter := filepath.Join(dir, rt.adapterFile)
			env := append(os.Environ(), api.FunctionInputSchemaEnv+"="+testInputSchema)
			spec := workerpool.Spec{Executable: bin, Args: []string{adapter, t.Name()}, Env: env, HandlerPath: adapter}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Serve(w, r, ServeConfig{Runtime: rt.runtime, Spec: spec})
			}))
			t.Cleanup(srv.Close)

			for _, tc := range []struct {
				name   string
				marked bool
				body   string
				status int
				want   string
			}{
				{"valid", true, `{"email":"a@b","age":3,"tags":["a"]}`, http.StatusOK, `"ran"`},
				{"missing", true, `{}`, http.StatusUnprocessableEntity, "input.email is required"},
				{"wrong type", true, `{"email":"a@b","age":1.5}`, http.StatusUnprocessableEntity, "input.age must be integer"},
				{"pattern", true, `{"email":"nope"}`, http.StatusUnprocessableEntity, "input.email must match @"},
				{"extra", true, `{"email":"a@b","x":1}`, http.StatusUnprocessableEntity, "input.x is not allowed"},
				{"item", true, `{"email":"a@b","tags":["c"]}`, http.StatusUnprocessableEntity, "input.tags[0] must be one of"},
				{"not json", true, `{`, http.StatusUnprocessableEntity, "input must be JSON"},
				{"unmarked", false, `{}`, http.StatusOK, `"ran"`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(tc.body))
					req.Header.Set("Content-Type", "application/json")
					if tc.marked {
						req.Header.Set(api.FunctionCallHeader, "1")
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = resp.Body.Close() }()
					body, _ := io.ReadAll(resp.Body)
					if resp.StatusCode != tc.status || !strings.Contains(string(body), tc.want) {
						t.Fatalf("status %d body %s; want %d containing %q", resp.StatusCode, body, tc.status, tc.want)
					}
					if tc.status == http.StatusUnprocessableEntity && !strings.Contains(string(body), api.CodeFunctionInputInvalid) {
						t.Fatalf("422 body %s lacks code %s", body, api.CodeFunctionInputInvalid)
					}
				})
			}
		})
	}
}
