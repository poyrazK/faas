package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestLogExplanationStreamOpenProblemIsStructured(t *testing.T) {
	explanationServer(t, "live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusTooManyRequests)
		if err := json.NewEncoder(w).Encode(api.NewProblem(http.StatusTooManyRequests, "log_streams_limit", "Log stream limit", "try again later")); err != nil {
			t.Error(err)
		}
	})
	code, output, _ := runExplanationCLI(t, true, "api", "--deployment", explanationDeploymentID, "--explain")
	var receipt struct {
		Complete bool   `json:"complete"`
		Reason   string `json:"end_reason"`
		Code     string `json:"read_error_code"`
		Status   int    `json:"read_status"`
	}
	if err := json.Unmarshal([]byte(output), &receipt); err != nil {
		t.Fatal(err)
	}
	if code == 0 || receipt.Complete || receipt.Reason != "open_failed" || receipt.Code != "log_streams_limit" || receipt.Status != http.StatusTooManyRequests {
		t.Fatalf("stream-open failure lost structure: %d, %+v", code, receipt)
	}
}

func TestLogExplanationReleaseAndCanonicalIdentity(t *testing.T) {
	for _, ref := range []string{"v12", strings.ToUpper(explanationDeploymentID)} {
		t.Run(ref, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.EqualFold(r.URL.Path, "/v1/deployments/"+explanationDeploymentID):
					main.WriteJSONTest(w, api.DeploymentResponse{ID: explanationDeploymentID, AppID: "app-demo", Status: "failed"})
				case r.URL.Path == "/v1/apps/api":
					main.WriteJSONTest(w, api.AppResponse{ID: "app-demo", Slug: "api"})
				case r.URL.Path == "/v1/apps/api/deployments":
					main.WriteJSONTest(w, map[string]any{"items": []api.DeploymentResponse{{ID: explanationDeploymentID, Revision: 12}}, "next_cursor": ""})
				case r.URL.Path == "/v1/deployments/"+explanationDeploymentID+"/logs":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
				default:
					t.Errorf("unexpected read: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_fixture")
			t.Setenv("FAAS_JSON", "0")
			code, output, stderr := runExplanationCLI(t, true, "api", "--release", ref, "--explain")
			if code != 0 {
				t.Fatalf("release resolution failed: %d, %s", code, stderr)
			}
			if receipt := readExplanationReceipt(t, output); receipt.Deployment != explanationDeploymentID || !receipt.Complete {
				t.Fatalf("identity was not canonical: %+v", receipt)
			}
		})
	}
}

func TestLogExplanationReadErrorPreservesObservedRows(t *testing.T) {
	explanationServer(t, "live", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, explanationLogFrames)
		w.(http.Flusher).Flush()
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close() // Chunked response ends without its final chunk.
	})
	code, output, _ := runExplanationCLI(t, true, "api", "--deployment", explanationDeploymentID, "--explain")
	receipt := readExplanationReceipt(t, output)
	if code == 0 || receipt.Complete || receipt.EndReason != "read_error" || receipt.ReadError == "" || receipt.Observed != 4 || receipt.ErrorCount != 2 {
		t.Fatalf("read error discarded rows or looked complete: %d, %+v", code, receipt)
	}
}

func TestLogExplanationMalformedFramesHaveAnEntryBudget(t *testing.T) {
	explanationServer(t, "live", func(w http.ResponseWriter, _ *http.Request) {
		for range api.CLILogExplanationEntries + 1 {
			_, _ = fmt.Fprint(w, "event: log\ndata: {bad}\n\n")
		}
		_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	})
	code, output, _ := runExplanationCLI(t, true, "api", "--deployment", explanationDeploymentID, "--explain")
	receipt := readExplanationReceipt(t, output)
	if code == 0 || receipt.Complete || receipt.EndReason != "entry_limit" || receipt.Observed != 0 || receipt.Skipped != api.CLILogExplanationEntries || len(receipt.Warnings) > 3 {
		t.Fatalf("malformed records escaped the read budget: %d, %+v", code, receipt)
	}
}

func TestLogExplanationRedactsPersistedFailure(t *testing.T) {
	secret := "sk_live_" + strings.Repeat("a", 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/" + explanationDeploymentID:
			main.WriteJSONTest(w, api.DeploymentResponse{ID: explanationDeploymentID, AppID: "app-demo", Status: "failed", Error: "failed with " + secret, ErrorHint: "Authorization: Bearer fp_live_fixture"})
		case "/v1/apps/api":
			main.WriteJSONTest(w, api.AppResponse{ID: "app-demo", Slug: "api"})
		case "/v1/deployments/" + explanationDeploymentID + "/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
	t.Setenv("FAAS_JSON", "0")
	code, output, _ := runExplanationCLI(t, true, "api", "--deployment", explanationDeploymentID, "--explain")
	if code != 0 || strings.Contains(output, secret) || strings.Contains(output, "fp_live_fixture") || !strings.Contains(output, "REDACTED") {
		t.Fatal("persisted failure metadata was not redacted")
	}
}
