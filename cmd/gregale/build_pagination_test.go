package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildPaginationContract(t *testing.T) {
	for _, machine := range []bool{false, true} {
		for _, mode := range []string{"page", "all", "empty", "empty-page", "api-error", "repeat", "cycle"} {
			t.Run(fmt.Sprintf("json=%t/%s", machine, mode), func(t *testing.T) {
				setupCLIRegression(t)
				start, next := "queued|start + token", "timestamp|next + token"
				var cursors []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					query := r.URL.Query()
					if r.Method != http.MethodGet || r.URL.Path != "/v1/builds" || query.Get("app") != "demo" || query.Get("status") != "queued" || query.Get("limit") != "2" {
						t.Errorf("request=%s %s", r.Method, r.URL.String())
					}
					cursor := query.Get("before")
					cursors = append(cursors, cursor)
					n := len(cursors)
					if mode == "api-error" && n == 2 {
						w.WriteHeader(503)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "unavailable", Title: "Unavailable"})
						return
					}
					continuation := ""
					if n == 1 && mode != "empty" {
						continuation = next
					}
					if mode == "repeat" {
						continuation = cursor
					}
					if mode == "cycle" {
						if n == 2 {
							continuation = "third"
						}
						if n == 3 {
							continuation = next
						}
					}
					rows := []api.BuildResponse{{ID: fmt.Sprintf("build-%d", n), Status: "queued"}}
					if mode == "empty" || mode == "empty-page" {
						rows = []api.BuildResponse{}
					}
					_ = json.NewEncoder(w).Encode(api.BuildListResponse{Items: rows, NextBefore: continuation})
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				command := []string{"build", "list", "--app", "demo", "--status", "queued", "--before", "ignored", "--cursor", start, "--limit", "2"}
				all := mode != "page" && mode != "empty-page"
				if all {
					command = append(command, "--all")
				}
				if machine {
					command = append(command, "--json")
				}
				code, out, errOut := capturePreviewRun(t, command)
				wantCursors := []string{start}
				if mode == "all" || mode == "api-error" {
					wantCursors = append(wantCursors, next)
				}
				if mode == "cycle" {
					wantCursors = append(wantCursors, next, "third")
				}
				if !reflect.DeepEqual(cursors, wantCursors) {
					t.Fatalf("cursors=%v want=%v", cursors, wantCursors)
				}
				if mode == "api-error" || mode == "repeat" || mode == "cycle" {
					wantCode := 1
					if mode == "api-error" {
						wantCode = 3
					}
					if code != wantCode || out != "" {
						t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errOut)
					}
					if machine {
						assertOneProblem(t, errOut)
					}
					return
				}
				if code != 0 || errOut != "" {
					t.Fatalf("exit=%d stderr=%s", code, errOut)
				}
				count := 1
				if mode == "all" {
					count = 2
				}
				if mode == "empty" || mode == "empty-page" {
					count = 0
				}
				if machine {
					var rows []api.BuildResponse
					if all {
						if err := json.Unmarshal([]byte(out), &rows); err != nil {
							t.Fatalf("all output is not an array: %v: %s", err, out)
						}
						if rows == nil {
							t.Fatal("empty array must not be null")
						}
					} else {
						var envelope struct {
							Items      []api.BuildResponse `json:"items"`
							NextBefore string              `json:"next_before"`
							NextCursor string              `json:"next_cursor"`
						}
						if err := json.Unmarshal([]byte(out), &envelope); err != nil {
							t.Fatal(err)
						}
						if envelope.NextBefore != next || envelope.NextCursor != next {
							t.Fatalf("envelope=%+v", envelope)
						}
						rows = envelope.Items
					}
					if len(rows) != count {
						t.Fatalf("rows=%v count=%d", rows, count)
					}
					for i, row := range rows {
						if row.ID != fmt.Sprintf("build-%d", i+1) {
							t.Fatalf("rows reordered or duplicated: %+v", rows)
						}
					}
				} else {
					if !all && !strings.Contains(out, "--cursor "+next) {
						t.Fatalf("missing continuation: %s", out)
					}
					if count == 2 && (!strings.Contains(out, "build-1") || !strings.Contains(out, "build-2")) {
						t.Fatalf("incomplete list: %s", out)
					}
					if all && strings.Contains(out, "--cursor") {
						t.Fatalf("finished list has continuation: %s", out)
					}
				}
			})
		}
	}
}

func TestBuildPaginationLimitsAndAliases(t *testing.T) {
	for _, limit := range []int{0, -1, 1, 200, 201} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			setupCLIRegression(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("limit") != fmt.Sprint(limit) || r.URL.Query().Get("before") != "legacy" {
					t.Errorf("query=%s", r.URL.RawQuery)
				}
				_ = json.NewEncoder(w).Encode(api.BuildListResponse{})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", testAPIKey('a'))
			code, out, errOut := capturePreviewRun(t, []string{"build", "list", "--cursor", "ignored", "--before", "legacy", "--limit", fmt.Sprint(limit), "--all", "--json"})
			if limit < 1 || limit > 200 {
				if code != 1 || calls != 0 || out != "" {
					t.Fatalf("exit=%d calls=%d stdout=%s", code, calls, out)
				}
				assertOneProblem(t, errOut)
			} else if code != 0 || calls != 1 || strings.TrimSpace(out) != "[]" || errOut != "" {
				t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, out, errOut)
			}
		})
	}
}

func TestBuildPaginationFinalPageAndHelp(t *testing.T) {
	setupCLIRegression(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.BuildListResponse{Items: []api.BuildResponse{}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", testAPIKey('a'))
	code, out, errOut := capturePreviewRun(t, []string{"build", "list", "--json"})
	if code != 0 || errOut != "" {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if _, ok := envelope["next_cursor"]; ok {
		t.Fatal("final page has next_cursor")
	}
	if _, ok := envelope["next_before"]; ok {
		t.Fatal("final page has next_before")
	}
	code, out, errOut = capturePreviewRun(t, []string{"build", "list", "--help"})
	if code != 0 {
		t.Fatalf("help exit=%d stderr=%s", code, errOut)
	}
	for _, flag := range []string{"--limit", "--cursor", "--before", "--all", "--app", "--status"} {
		if !strings.Contains(out, flag) {
			t.Errorf("help missing %s", flag)
		}
	}
}
