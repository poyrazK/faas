package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	cronsCancelID    = "0123456789abcdef0123456789abcdef"
	cronsCancelRunID = "abcdef0123456789abcdef0123456789"
)

func cronRunCancelReceipt(status api.AppTaskStatus) api.AppTaskResponse {
	cancelRequestedAt := "2026-09-26T10:00:00Z"
	var cancelAt *string
	if status == api.AppTaskStatusRunning {
		cancelAt = &cancelRequestedAt
	}
	return api.AppTaskResponse{
		ID: cronsCancelRunID, AppID: cronsCancelID, DeploymentID: cronsCancelID,
		Kind: api.AppTaskKindCron, Command: []string{"bin/maintenance"},
		Status: status, CancelRequestedAt: cancelAt,
	}
}

func TestCmdCronsCancelRequestsCancellation(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			t.Errorf("request body = %q, err=%v; want empty body", body, err)
		}
		_ = json.NewEncoder(w).Encode(cronRunCancelReceipt(api.AppTaskStatusRunning))
	}))
	defer srv.Close()
	configureCronCancelClient(t, srv.URL)

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsCancel([]string{cronsCancelID, cronsCancelRunID}); code != 0 {
		t.Fatalf("crons cancel = %d", code)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/crons/"+cronsCancelID+"/runs/"+cronsCancelRunID+"/cancel" {
		t.Fatalf("request = %s %s; want cron run cancellation POST", gotMethod, gotPath)
	}
	if !strings.Contains(stdout.String(), "Cancellation requested for cron run "+cronsCancelRunID) ||
		!strings.Contains(stdout.String(), "status=running") {
		t.Fatalf("output = %q; want cancellation-requested status", stdout.String())
	}
}

func TestCmdCronsCancelJSONReturnsTaskReceipt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(cronRunCancelReceipt(api.AppTaskStatusCancelled))
	}))
	defer srv.Close()
	configureCronCancelClient(t, srv.URL)

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsCancel([]string{cronsCancelID, cronsCancelRunID}); code != 0 {
		t.Fatalf("crons cancel --json = %d", code)
	}
	var got api.AppTaskResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v; output=%s", err, stdout.String())
	}
	if got.ID != cronsCancelRunID || got.Status != api.AppTaskStatusCancelled {
		t.Fatalf("JSON task = %+v", got)
	}
}

func TestCmdCronsCancelRejectsInvalidIDsBeforeRequest(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	configureCronCancelClient(t, srv.URL)

	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	defer func() { osStderr = oldErr }()
	if code := cmdCronsCancel([]string{"bad-cron-id", cronsCancelRunID}); code != 1 {
		t.Fatalf("invalid cron id exit = %d; want 1", code)
	}
	if code := cmdCronsCancel([]string{cronsCancelID, "bad-run-id"}); code != 1 {
		t.Fatalf("invalid run id exit = %d; want 1", code)
	}
	if requests != 0 {
		t.Fatalf("invalid inputs made %d requests; want 0", requests)
	}
}

func TestRunDispatchesCronCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/crons/"+cronsCancelID+"/runs/"+cronsCancelRunID+"/cancel" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(cronRunCancelReceipt(api.AppTaskStatusCancelled))
	}))
	defer srv.Close()
	configureCronCancelClient(t, srv.URL)
	if code := run([]string{"crons", "cancel", cronsCancelID, cronsCancelRunID}); code != 0 {
		t.Fatalf("run crons cancel = %d", code)
	}
}

func configureCronCancelClient(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("FAAS_API", baseURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
}
