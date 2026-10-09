package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const previewTestDeploymentID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func capturePreviewRun(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	stderr, restore := captureStderr(t)
	var stdout bytes.Buffer
	code := captureStdoutSwap(t, &stdout, func() int { return run(args) })
	restore()
	return code, stdout.String(), stderr.String()
}

func TestDestructivePreviewDryRunsOnlyRead(t *testing.T) {
	for _, command := range [][]string{
		{"apps", "--dry-run", "demo"},
		{"apps", "--dry-run", "--yes", "demo"},
		{"apps", "--dry-run", "--quiet", "demo"},
		{"deploys", "clear", previewTestDeploymentID, "--dry-run"},
		{"deploys", "clear", previewTestDeploymentID, "--force", "--dry-run"},
		{"deploys", "clear-obsolete", "--app", "demo", "--dry-run"},
		{"deploys", "clear-obsolete", "--app", "demo", "--force", "--dry-run"},
	} {
		t.Run(strings.Join(command, "_"), func(t *testing.T) {
			setupCLIRegression(t)
			osStdin = forbiddenAutomationInput{t}
			var reads, writes atomic.Int32
			old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
					t.Errorf("dry run mutated: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				reads.Add(1)
				switch r.URL.Path {
				case "/v1/apps/demo":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "demo", Status: "active"})
				case "/v1/apps/demo/deployments":
					before := r.URL.Query().Get("before")
					if before == "" {
						_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: previewTestDeploymentID, Status: "failed", CreatedAt: old}}, NextBefore: "page-two"})
					} else if before == "page-two" {
						_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: "live-deployment", Status: "live", CreatedAt: old}}})
					} else {
						t.Errorf("unexpected cursor %s", before)
						w.WriteHeader(500)
					}
				case "/v1/deployments/" + previewTestDeploymentID:
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: previewTestDeploymentID, AppID: "app-1", Status: "failed", CreatedAt: old})
				default:
					t.Errorf("unexpected GET %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", testAPIKey('a'))
			args := append([]string{"--non-interactive", "--profile", "staging", "--json"}, command...)
			code, out, errOut := capturePreviewRun(t, args)
			var p destructivePreview
			if err := json.Unmarshal([]byte(out), &p); err != nil {
				t.Fatalf("exit=%d invalid preview: %v stdout=%s stderr=%s", code, err, out, errOut)
			}
			if code != 0 || errOut != "" || writes.Load() != 0 || reads.Load() == 0 || !p.DryRun || p.Count != len(p.Resources) || p.Effect == "" || p.Recovery == "" || p.Note == "" {
				t.Fatalf("exit=%d preview=%+v reads=%d writes=%d stderr=%s", code, p, reads.Load(), writes.Load(), errOut)
			}
			if command[0] == "apps" {
				if p.Count != 3 || p.Resources[0].Kind != "app" || !strings.Contains(p.Recovery, "gregale --profile staging apps restore demo") {
					t.Fatalf("app preview=%+v", p)
				}
			} else if command[1] == "clear-obsolete" {
				if p.Exact || p.Count != 1 || p.Resources[0].ID != previewTestDeploymentID || p.Cutoff == "" || !strings.Contains(p.Note, "retention") {
					t.Fatalf("candidate selection=%+v", p)
				}
			} else if !p.Exact || p.Count != 1 {
				t.Fatalf("single selection=%+v", p)
			}
			var human bytes.Buffer
			renderDestructivePreview(&human, p)
			for _, value := range []string{p.Effect, p.Recovery, p.Note, p.Resources[0].ID} {
				if !strings.Contains(human.String(), value) {
					t.Fatalf("human preview omitted %q", value)
				}
			}
		})
	}
}

func TestDestructivePreviewMissingConfirmationNeverWrites(t *testing.T) {
	for _, automation := range []bool{false, true} {
		for _, command := range [][]string{{"apps", "--yes=false", "demo"}, {"deploys", "clear", previewTestDeploymentID}, {"deploys", "clear-obsolete", "--app", "demo"}} {
			t.Run(fmt.Sprintf("automation_%t_%s", automation, strings.Join(command, "_")), func(t *testing.T) {
				setupCLIRegression(t)
				osStdin = forbiddenAutomationInput{t}
				var writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes.Add(1)
						t.Error("unconfirmed mutation")
						w.WriteHeader(500)
						return
					}
					switch r.URL.Path {
					case "/v1/apps/demo":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "demo"})
					case "/v1/apps/demo/deployments":
						_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{}})
					default:
						_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: previewTestDeploymentID, Status: "failed"})
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				prefix := []string{"--json"}
				if automation {
					prefix = append(prefix, "--non-interactive")
				}
				code, out, errOut := capturePreviewRun(t, append(prefix, command...))
				p := assertOneProblem(t, errOut)
				if code != 1 || out != "" || writes.Load() != 0 || !strings.Contains(p.Title, "Confirmation required") {
					t.Fatalf("exit=%d stdout=%s Problem=%+v writes=%d", code, out, p, writes.Load())
				}
			})
		}
	}
}

func TestDestructivePreviewPaginationAndTimestampFailuresAreClosed(t *testing.T) {
	for _, mode := range []string{"api-error", "repeated-cursor", "page-limit", "bad-timestamp"} {
		for _, command := range [][]string{{"apps", "--dry-run", "demo"}, {"deploys", "clear-obsolete", "--app", "demo", "--dry-run"}} {
			if mode == "bad-timestamp" && command[0] == "apps" {
				continue
			}
			t.Run(mode+"_"+command[0], func(t *testing.T) {
				setupCLIRegression(t)
				osStdin = forbiddenAutomationInput{t}
				var pages, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes.Add(1)
						t.Error("preview failure mutated resources")
						w.WriteHeader(500)
						return
					}
					if r.URL.Path == "/v1/apps/demo" {
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "demo"})
						return
					}
					page := pages.Add(1)
					if mode == "api-error" && page > 1 {
						w.WriteHeader(503)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "unavailable", Title: "Page unavailable"})
						return
					}
					cursor := "next"
					if mode == "page-limit" {
						cursor = fmt.Sprint(page)
					}
					if mode == "bad-timestamp" {
						cursor = ""
					}
					created := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
					if mode == "bad-timestamp" {
						created = "invalid"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: previewTestDeploymentID, Status: "failed", CreatedAt: created}}, NextBefore: cursor})
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				code, out, errOut := capturePreviewRun(t, append([]string{"--non-interactive", "--json"}, command...))
				assertOneProblem(t, errOut)
				if code == 0 || out != "" || writes.Load() != 0 {
					t.Fatalf("partial preview escaped: exit=%d stdout=%s stderr=%s", code, out, errOut)
				}
				if mode == "page-limit" && pages.Load() != 100 {
					t.Fatalf("page bound=%d", pages.Load())
				}
			})
		}
	}
}

func TestDestructivePreviewDeclinedHumanConfirmationNeverWrites(t *testing.T) {
	for _, command := range [][]string{{"apps", "--yes=false", "demo"}, {"deploys", "clear", previewTestDeploymentID}, {"deploys", "clear-obsolete", "--app", "demo"}} {
		t.Run(strings.Join(command, "_"), func(t *testing.T) {
			setupCLIRegression(t)
			osStdin = strings.NewReader("no\n")
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
					t.Error("declined confirmation mutated resources")
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/v1/apps/demo":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "demo", Status: "active"})
				case "/v1/apps/demo/deployments":
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{}})
				default:
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: previewTestDeploymentID, Status: "failed"})
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", testAPIKey('a'))
			code, out, errOut := capturePreviewRun(t, command)
			if code != 130 || out != "" || writes.Load() != 0 {
				t.Fatalf("exit=%d stdout=%s writes=%d stderr=%s", code, out, writes.Load(), errOut)
			}
			prompt := "[y/N]"
			if command[0] == "apps" {
				prompt = "Type"
			}
			effectIndex, promptIndex := strings.Index(errOut, "Effect:"), strings.Index(errOut, prompt)
			if effectIndex < 0 || promptIndex <= effectIndex || !strings.Contains(errOut, "Recovery:") {
				t.Fatalf("preview did not precede confirmation: %s", errOut)
			}
		})
	}
}
