package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRunRegressionSourceRunJSONFailureExit(t *testing.T) {
	for _, status := range []string{"failed", "timed_out", "out_of_memory", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				receiptStatus := status
				if r.Method == http.MethodPost {
					receiptStatus = "queued"
					w.WriteHeader(http.StatusAccepted)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "run-1", "status": receiptStatus, "runtime": "node22"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			code, stdout, stderr := runCLI(t, "--json", "run", "--source", "throw new Error('failure')", "--wait")
			var receipt api.ExecutionResponse
			if err := json.Unmarshal([]byte(stdout), &receipt); err != nil || string(receipt.Status) != status {
				t.Fatalf("did not receive the terminal receipt: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if code == 0 {
				t.Errorf("failed execution reported shell success: status=%s code=%d stdout=%s stderr=%s", status, code, stdout, stderr)
			}
		})
	}
}

func TestRunRegressionPositionalImageCommandFlags(t *testing.T) {
	for _, flag := range []string{"--file", "--input", "--runtime", "--wait", "--output-dir"} {
		args := []string{"alpine", "tool", flag, "example"}
		if !main.RunImageRequested(args) {
			t.Errorf("image command routed to source mode: %q", args)
		}
	}
}

func TestRunRegressionImageCommandPassThrough(t *testing.T) {
	for _, flag := range []string{"--json", "--help", "--follow"} {
		t.Run(flag, func(t *testing.T) {
			received := make(chan api.CreateImageRunRequest, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" && r.URL.Path == "/v1/image-runs" {
					var req api.CreateImageRunRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					received <- req
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"status":"queued","job":"ir-test","run_id":"run-1"}`))
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			code, stdout, stderr := runCLI(t, "run", "--image", "alpine", "--detach", "tool", flag)
			var request api.CreateImageRunRequest
			select {
			case request = <-received:
			default:
				t.Fatalf("command never submitted: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if request.Args == nil || !reflect.DeepEqual(*request.Args, []string{"tool", flag}) {
				t.Errorf("child flag changed: requested=%q received=%v code=%d", flag, request.Args, code)
			}
		})
	}
}

func TestRunRegressionWatchReportsReplayGap(t *testing.T) {
	for _, output := range []string{"human", "json"} {
		t.Run(output, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/events") {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("id: 5\nevent: gap\ndata: {\"execution_id\":\"run-1\",\"through_sequence\":5}\n\nid: 6\nevent: terminal\ndata: {\"status\":\"succeeded\"}\n\n"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				status := "succeeded"
				if r.Method == http.MethodPost {
					status = "queued"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "run-1", "status": status, "runtime": "node22", "stdout": "complete output"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			args := []string{"run", "--source", "return 1", "--watch"}
			if output == "json" {
				args = append([]string{"--json"}, args...)
			}
			code, stdout, stderr := runCLI(t, args...)
			if code != 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if output == "human" {
				if !strings.Contains(stderr, "some run output expired") || !strings.Contains(stderr, "sandbox get run-1") {
					t.Fatalf("missing recovery warning: %s", stderr)
				}
			} else if !strings.Contains(stdout, `"type":"gap"`) || !strings.Contains(stdout, `"through_sequence":5`) || !strings.Contains(stdout, `"stdout":"complete output"`) {
				t.Fatalf("JSON watch lost gap or terminal receipt: %s", stdout)
			}
		})
	}
}
