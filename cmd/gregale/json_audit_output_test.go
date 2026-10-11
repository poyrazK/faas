package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDashboardJSONDoesNotLaunchBrowser(t *testing.T) {
	for _, stateless := range []bool{false, true} {
		t.Run(map[bool]string{false: "account", true: "stateless"}[stateless], func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			t.Setenv("FAAS_API", "https://api.example.test")
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			rec := withRecorder(t)
			out, restore := captureStdout(t)
			defer restore()
			var args []string
			if stateless {
				args = append(args, "--stateless")
			}
			if code := cmdDashboard(args); code != 0 {
				t.Fatalf("dashboard exit=%d", code)
			}
			var receipt struct {
				URL       string `json:"url"`
				Stateless bool   `json:"stateless"`
			}
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatalf("stdout must be one JSON document: %v", err)
			}
			want := dashboardAccountURL(apiBase())
			if stateless {
				want = dashboardStatelessURL(apiBase())
			}
			if receipt.URL != want || receipt.Stateless != stateless || len(rec.urls) != 0 {
				t.Fatalf("receipt=%+v browser launches=%v", receipt, rec.urls)
			}
		})
	}
}

func TestOpenJSONDoesNotWakeOrLaunchBrowser(t *testing.T) {
	for _, dashboard := range []bool{false, true} {
		t.Run(map[bool]string{false: "app", true: "dashboard"}[dashboard], func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			requests := 0
			live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(http.StatusOK)
			}))
			defer live.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo" {
					t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
				}
				writeJSONTest(w, api.AppResponse{Slug: "demo", URL: live.URL})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			rec := withRecorder(t)
			out, restore := captureStdout(t)
			defer restore()
			args := []string{"demo"}
			if dashboard {
				args = append(args, "--dashboard")
			}
			if code := cmdOpen(args); code != 0 {
				t.Fatalf("open exit=%d", code)
			}
			var receipt struct {
				Slug      string `json:"slug"`
				URL       string `json:"url"`
				Dashboard bool   `json:"dashboard"`
			}
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatalf("stdout must be one JSON document: %v", err)
			}
			want := live.URL
			if dashboard {
				want = dashboardAppURL(srv.URL, "demo")
			}
			if receipt.Slug != "demo" || receipt.URL != want || receipt.Dashboard != dashboard {
				t.Fatalf("receipt=%+v", receipt)
			}
			if requests != 0 || len(rec.urls) != 0 {
				t.Fatalf("JSON URL lookup woke app %d times, browser=%v", requests, rec.urls)
			}
		})
	}
}

func TestEnvPullJSONUsesScopedKeysAndPreservesLocalValues(t *testing.T) {
	for _, existing := range []string{"", "A=local-secret\nLOCAL=preserved\n"} {
		t.Run(map[bool]string{false: "new", true: "preserve"}[existing != ""], func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			t.Chdir(t.TempDir())
			path := filepath.Join(t.TempDir(), ".env")
			if existing != "" {
				if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("scope") != "staging" {
					t.Errorf("scope=%s", r.URL.RawQuery)
				}
				// Count is the account-wide quota count, not the selected scope.
				writeJSONTest(w, api.AppSecretListResponse{Count: 9, Quota: 25,
					Secrets: []api.AppSecretResponse{{Key: "A"}, {Key: "B"}}})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			out, restore := captureStdout(t)
			defer restore()
			if code := envPull([]string{"--app", "demo", "--scope", "staging", "-o", path}); code != 0 {
				t.Fatalf("pull exit=%d", code)
			}
			var receipt struct {
				AppSlug    string `json:"app_slug"`
				Scope      string `json:"scope"`
				OutputPath string `json:"output_path"`
				Remote     int    `json:"remote_key_count"`
				Added      int    `json:"added_key_count"`
				Preserved  int    `json:"preserved_key_count"`
				KeyOnly    bool   `json:"key_only"`
			}
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			added, preserved := 2, 0
			if existing != "" {
				added, preserved = 1, 2
			}
			if receipt.AppSlug != "demo" || receipt.Scope != "staging" || receipt.OutputPath != path ||
				receipt.Remote != 2 || receipt.Added != added || receipt.Preserved != preserved || !receipt.KeyOnly {
				t.Fatalf("receipt=%+v", receipt)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(data), existing) || !strings.Contains(string(data), "B=\n") {
				t.Fatalf("file changed local values or lacks key skeleton: %q", data)
			}
			if strings.Contains(out.String(), "local-secret") || strings.Contains(out.String(), `"preserved"`) {
				t.Fatal("receipt leaked local values")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("mode info=%v err=%v", info, err)
			}
		})
	}
}

func TestEnvPushJSONReportsActualWritesAndFreshRestart(t *testing.T) {
	cases := []struct {
		name                                 string
		stdin, restart, failSet, failRestart bool
		code, count                          int
		result, mode                         string
	}{
		{name: "file", count: 2, result: "applied", mode: "next_cold_wake"},
		{name: "stdin", stdin: true, count: 2, result: "applied", mode: "next_cold_wake"},
		{name: "restart", restart: true, count: 2, result: "applied", mode: "fresh_restart_requested"},
		{name: "partial", restart: true, failSet: true, code: 5, count: 1, result: "partial", mode: "next_cold_wake"},
		{name: "restart-failed", restart: true, failRestart: true, code: 5, count: 2, result: "restart_failed", mode: "next_cold_wake"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			t.Chdir(t.TempDir())
			sets, restarts := []string{}, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/secrets/") {
					if r.Method != http.MethodPut || r.URL.Query().Get("scope") != "staging" {
						t.Errorf("secret request=%s %s", r.Method, r.URL)
					}
					key := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					if tc.failSet && key == "B" {
						api.WriteProblem(w, api.NewProblem(409, api.CodeValidation, "Rejected", "synthetic set rejection"))
						return
					}
					sets = append(sets, key)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/restart") {
					restarts++
					if r.URL.Query().Get("fresh") != "true" {
						t.Error("restart must be fresh")
					}
					if tc.failRestart {
						api.WriteProblem(w, api.NewProblem(409, api.CodeValidation, "Rejected", "synthetic restart rejection"))
						return
					}
					writeJSONTest(w, api.AppRestartResponse{WakeID: "wake-json"})
					return
				}
				t.Errorf("unexpected request %s", r.URL)
				http.NotFound(w, r)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			args := []string{"--app", "demo", "--scope", "staging", "--secret-scan", "off"}
			if tc.stdin {
				pipeStdin(t, "A=value-one\nB=value-two\n")
				args = append(args, "--from-stdin")
			} else {
				path := filepath.Join(t.TempDir(), ".env")
				if err := os.WriteFile(path, []byte("A=value-one\nB=value-two\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-f", path)
			}
			if tc.restart {
				args = append(args, "--restart")
			}
			out, restore := captureStdout(t)
			defer restore()
			errOut, restoreErr := captureStderr(t)
			defer restoreErr()
			if code := envPush(args); code != tc.code {
				t.Fatalf("exit=%d want=%d", code, tc.code)
			}
			var receipt envPushReceipt
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatalf("stdout must be one JSON document: %v", err)
			}
			if receipt.AppSlug != "demo" || receipt.Scope != "staging" || receipt.Result != tc.result ||
				receipt.UpdatedKeyCount != tc.count || len(receipt.UpdatedKeys) != tc.count || len(sets) != tc.count || receipt.ApplyMode != tc.mode {
				t.Fatalf("receipt=%+v actual writes=%v", receipt, sets)
			}
			if strings.Contains(out.String()+errOut.String(), "value-one") || strings.Contains(out.String()+errOut.String(), "value-two") {
				t.Fatal("output leaked pushed values")
			}
			if tc.failSet && (receipt.FailedKey != "B" || restarts != 0) {
				t.Fatal("partial push lost failure key or restarted")
			}
			if receipt.RestartRequested != (tc.restart && !tc.failSet && !tc.failRestart) {
				t.Fatal("restart request misreported")
			}
			if receipt.RestartRequested && receipt.WakeID != "wake-json" {
				t.Fatal("missing wake correlation")
			}
		})
	}
}
