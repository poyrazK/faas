package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionControlCLIRequiresMatchingReceipt(t *testing.T) {
	for _, match := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "mismatched"}[match], func(t *testing.T) {
			resetJSONOut(t)
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			var out, errOut bytes.Buffer
			osStdout, osStderr, jsonOutput = &out, &errOut, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/abort") {
					t.Error("unexpected recovery route")
				}
				var request api.ProjectPromotionControlRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if !request.Valid() {
					t.Error("invalid recovery body")
				}
				if !match {
					request.ExpectedGeneration++
				}
				writeJSONTest(w, api.ProjectPromotionControl{Request: request, Action: "abort", Status: "pending", OperatorAccountID: "account", RequestedAt: time.Now(), Deadline: time.Now().Add(time.Minute)})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "token")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			code := cmdProjectsEnvironmentControl("abort", []string{"shop", "00000000-0000-0000-0000-000000000002", "--to", "production", "--generation", "5"})
			if match && code != 0 || !match && code != 1 {
				t.Fatalf("exit=%d output=%s errors=%s", code, &out, &errOut)
			}
		})
	}
}

func TestPromotionControlCLIWaitKeepsOriginalDecision(t *testing.T) {
	resetJSONOut(t)
	oldOut, oldErr, oldJSON, oldInterval := osStdout, osStderr, jsonOutput, projectEnvironmentPromotionPollInterval
	var out, errOut bytes.Buffer
	osStdout, osStderr, jsonOutput, projectEnvironmentPromotionPollInterval = &out, &errOut, true, time.Millisecond
	t.Cleanup(func() {
		osStdout, osStderr, jsonOutput, projectEnvironmentPromotionPollInterval = oldOut, oldErr, oldJSON, oldInterval
	})
	var original api.ProjectPromotionControlRequest
	posts, gets := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req api.ProjectPromotionControlRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			posts++
			status := "pending"
			if posts == 1 {
				original = req
			} else {
				if req != original {
					t.Error("wait changed request identity")
				}
				status = "blocked"
			}
			writeJSONTest(w, api.ProjectPromotionControl{Request: req, Action: "abort", Status: status, OperatorAccountID: "account"})
		} else {
			gets++
			// Another operator has already requested recovery. The CLI must read its
			// original abort receipt instead of reporting this newer decision's outcome.
			writeJSONTest(w, api.ProjectEnvironmentPromotionStatusResponse{Health: &api.ProjectPromotionHealth{Control: &api.ProjectPromotionControl{Request: api.ProjectPromotionControlRequest{RequestID: strings.Repeat("b", 32), ExpectedGeneration: 9}, Action: "recover", Status: "pending", OperatorAccountID: "account"}}})
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	code := cmdProjectsEnvironmentControl("abort", []string{"shop", "promotion", "--to", "production", "--generation", "5", "--wait"})
	if code != 1 || posts != 2 || gets != 1 || !strings.Contains(out.String(), `"status": "blocked"`) {
		t.Fatalf("exit=%d posts=%d gets=%d output=%s errors=%s", code, posts, gets, &out, &errOut)
	}
}
