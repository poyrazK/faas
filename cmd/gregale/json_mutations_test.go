package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const jsonMutationCronID = "0123456789abcdef0123456789abcdef"

func TestJSONMutations_ParkAndWake(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	resetJSONOut(t)
	jsonOutput = true

	if code := cmdPark([]string{"demo"}); code != 0 {
		t.Fatalf("park = %d, want 0", code)
	}
	var parked map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &parked); err != nil {
		t.Fatalf("park output is not JSON: %v\n%s", err, stdout.String())
	}
	if parked["slug"] != "demo" || parked["status"] != "parked" || parked["state"] != "cold" {
		t.Fatalf("park receipt = %#v", parked)
	}
	stdout.Reset()

	if code := cmdWake([]string{"demo"}); code != 0 {
		t.Fatalf("wake = %d, want 0", code)
	}
	var waking map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &waking); err != nil {
		t.Fatalf("wake output is not JSON: %v\n%s", err, stdout.String())
	}
	if waking["slug"] != "demo" || waking["status"] != "waking" {
		t.Fatalf("wake receipt = %#v", waking)
	}
	if len(paths) != 2 || paths[0] != "POST /v1/apps/demo/park" || paths[1] != "POST /v1/apps/demo/wake" {
		t.Fatalf("mutation paths = %#v", paths)
	}
}

func TestJSONMutations_QuietDelete(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	resetJSONOut(t)
	jsonOutput = true

	if code := cmdAppsRm([]string{"-q", "demo"}); code != 0 {
		t.Fatalf("quiet delete = %d, want 0", code)
	}
	var receipt map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("delete output is not JSON: %v\n%s", err, stdout.String())
	}
	if receipt["slug"] != "demo" || receipt["status"] != "deleted" || receipt["deleted"] != true {
		t.Fatalf("delete receipt = %#v", receipt)
	}
	if method != http.MethodDelete || path != "/v1/apps/demo" {
		t.Fatalf("delete request = %s %s", method, path)
	}
}

func TestJSONMutations_CronAddAndRemove(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/crons":
			writeJSONTest(w, api.CronResponse{
				ID: jsonMutationCronID, AppID: "demo", Schedule: "0 * * * *", Path: "/tick", Enabled: true,
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/crons/"+jsonMutationCronID:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	resetJSONOut(t)
	jsonOutput = true

	if code := cmdCrons([]string{"add", "--app", "demo", "--schedule", "0 * * * *", "--path", "/tick"}); code != 0 {
		t.Fatalf("cron add = %d, want 0", code)
	}
	var created api.CronResponse
	if err := json.Unmarshal(stdout.Bytes(), &created); err != nil {
		t.Fatalf("cron add output is not JSON: %v\n%s", err, stdout.String())
	}
	if created.ID != jsonMutationCronID || created.Schedule != "0 * * * *" || created.Path != "/tick" {
		t.Fatalf("cron add receipt = %+v", created)
	}
	stdout.Reset()

	if code := cmdCrons([]string{"rm", jsonMutationCronID}); code != 0 {
		t.Fatalf("cron remove = %d, want 0", code)
	}
	var removed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &removed); err != nil {
		t.Fatalf("cron remove output is not JSON: %v\n%s", err, stdout.String())
	}
	if removed["id"] != jsonMutationCronID || removed["status"] != "deleted" || removed["deleted"] != true {
		t.Fatalf("cron remove receipt = %#v", removed)
	}
}

func TestJSONMutations_InitReceipt(t *testing.T) {
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	resetJSONOut(t)
	jsonOutput = true

	dest := filepath.Join(t.TempDir(), "hello")
	if code := runCmdInit("hello-node", dest, false, "", osStdout, os.Stderr); code != 0 {
		t.Fatalf("init = %d, want 0", code)
	}
	var receipt initReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("init output is not JSON: %v\n%s", err, stdout.String())
	}
	if receipt.Template != "hello-node" || receipt.Path != dest || receipt.Status != "created" || receipt.Deployed {
		t.Fatalf("init receipt = %+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(dest, "handler.js")); err != nil {
		t.Fatalf("materialized template missing handler.js: %v", err)
	}
}

func TestJSONMutations_HonourFAASJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/demo/park" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("FAAS_JSON", "1")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	resetJSONOut(t)

	if code := run([]string{"park", "demo"}); code != 0 {
		t.Fatalf("park via FAAS_JSON = %d, want 0", code)
	}
	var receipt map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("FAAS_JSON output is not JSON: %v\n%s", err, stdout.String())
	}
	if receipt["slug"] != "demo" || receipt["status"] != "parked" {
		t.Fatalf("FAAS_JSON receipt = %#v", receipt)
	}
}
