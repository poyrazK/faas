package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdAccountReleaseWebhooks_AddAndList(t *testing.T) {
	var created api.CreateAccountReleaseWebhookRequest
	var createPath, listPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			createPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.AccountReleaseWebhookResponse{ID: webhookTestID, TargetURL: created.TargetURL})
		case http.MethodGet:
			listPath = r.URL.Path
			_ = json.NewEncoder(w).Encode([]api.AccountReleaseWebhookResponse{{ID: webhookTestID, TargetURL: created.TargetURL}})
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := cmdWebhooks([]string{"account", "add", "--target-url", "https://example.com/releases", "--secret", "shh", "--event", "deployment.live"}); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	if createPath != "/v1/account/release-webhooks" || created.WebhookSecret != "shh" || len(created.EventFilter) != 1 || created.EventFilter[0] != "deployment.live" {
		t.Fatalf("create path=%q body=%+v", createPath, created)
	}
	if code := cmdWebhooks([]string{"account", "list"}); code != 0 || listPath != createPath || !strings.Contains(stdout.String(), webhookTestID) {
		t.Fatalf("list exit=%d path=%q stdout=%q", code, listPath, stdout.String())
	}
}

func TestCmdAccountReleaseWebhooks_RejectsNonReleaseEvent(t *testing.T) {
	if code := cmdAccountReleaseWebhooks([]string{"add", "--target-url", "https://example.com/releases", "--event", "app.parked"}); code == 0 {
		t.Fatal("accepted app-only event for account receiver")
	}
}
