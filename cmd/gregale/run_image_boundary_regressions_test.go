package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRunRegressionEmptyImageEntrypoint(t *testing.T) {
	opts, ok := main.ParseImageRunArgs([]string{"--image", "alpine", "--entrypoint", "", "echo", "hello"})
	if !ok {
		t.Fatal("empty entrypoint was rejected")
	}
	req := opts.Request()
	argv := api.ResolveImageRunCommand([]string{"/original-entrypoint"}, []string{"original-cmd"}, req.Entrypoint, req.Args)
	if len(argv) == 0 || argv[0] != "echo" {
		t.Errorf("--entrypoint '' did not clear the image ENTRYPOINT: resolved argv=%q", argv)
	}
}

func TestRunRegressionImageCommandBoundaryAcrossPublicSpellings(t *testing.T) {
	for _, prefix := range [][]string{
		{"run", "--detach", "alpine"},
		{"sandbox", "run", "--detach", "--image", "alpine"},
		{"--non-interactive", "--json", "run", "--detach", "--image", "alpine"},
		{"--http-timeout", "90s", "--json", "run", "--detach", "--image", "alpine"},
		{"--http-timeout=2m", "sandbox", "run", "--detach", "alpine"},
		{"--http-timeout=2m", "sandbox", "run", "--detach", "--image=alpine", "--"},
		{"run", "--detach", "--image=alpine", "--"},
	} {
		t.Run(prefix[0]+"_"+prefix[len(prefix)-1], func(t *testing.T) {
			requests := make(chan api.CreateImageRunRequest, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/image-runs" {
					http.NotFound(w, r)
					return
				}
				var req api.CreateImageRunRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				requests <- req
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"status":"queued","job":"ir-test","run_id":"run-1"}`))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			command := []string{"tool", "--help", "--json=false", "--follow", "--file", "input"}
			args := append(append([]string{}, prefix...), command...)
			code, _, stderr := runCLI(t, args...)
			if code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr)
			}
			select {
			case req := <-requests:
				if req.Args == nil || !reflect.DeepEqual(*req.Args, command) {
					t.Fatalf("forwarded command=%v, want %q", req.Args, command)
				}
			default:
				t.Fatal("command was intercepted before submission")
			}
		})
	}
}
