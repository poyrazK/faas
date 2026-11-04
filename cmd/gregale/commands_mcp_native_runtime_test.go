package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestMCPNativeReleaseChecksWorkerRuntimeBeforeSideEffects(t *testing.T) {
	for _, runtime := range []string{"node", "go", "python"} {
		t.Run(runtime, func(t *testing.T) {
			_, stderr, restore := main.SwapIO(t)
			defer restore()
			root := t.TempDir()
			stack := filepath.Join(root, "stack")
			if code := main.CmdMCPInit([]string{"--path", stack, "--runtime", runtime, "--tasks", "--name", "reports"}); code != 0 {
				t.Fatalf("init: %s", stderr())
			}
			plan := filepath.Join(root, "plan.json")
			body := `{"web_app":"reports-web","worker_app":"reports-next","observer_app":"reports-observer","observer_metric_app":"reports-worker","previous_worker_apps":["reports-worker"],"web_path":"stack/web","worker_path":"stack/worker"}`
			if err := os.WriteFile(plan, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/v1/capabilities" {
					t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, `{"conditional_parking":false}`)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "operator")
			for _, action := range []string{"run", "restore", "quarantine", "retire", "recover"} {
				state := filepath.Join(root, action+".json")
				args := []string{"tasks", "release", action, "--plan", plan, "--state", state}
				if action == "recover" {
					args = append(args, "--resume")
				}
				stderrOffset := len(stderr())
				if code := main.CmdMCP(args); code == 0 {
					t.Fatalf("%s unexpectedly succeeded", action)
				}
				actionError := stderr()[stderrOffset:]
				if runtime == "node" {
					if requests != 1 || !strings.Contains(actionError, "Upgrade") {
						t.Fatalf("Node gate did not reach control-plane preflight: requests=%d error=%s", requests, actionError)
					}
					break
				}
				if requests != 0 || !strings.Contains(actionError, "manual rollout for Go/Python") {
					t.Fatalf("%s failed too late or without runtime guidance: requests=%d error=%s", action, requests, actionError)
				}
				for _, path := range []string{state, state + ".lock", state + ".gate"} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("unsupported runtime created %s: %v", path, err)
					}
				}
			}
			if runtime != "node" && main.CmdMCP([]string{"tasks", "release", "status", "--plan", plan, "--state", filepath.Join(root, "status.json")}) != 0 {
				t.Fatalf("read-only journal status rejected: %s", stderr())
			}
		})
	}
}
