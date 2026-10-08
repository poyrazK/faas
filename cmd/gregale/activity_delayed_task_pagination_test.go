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

func TestActivityAndDelayedTaskPaginationContract(t *testing.T) {
	for _, command := range []string{"orgs", "delayed-task"} {
		for _, machine := range []bool{false, true} {
			for _, mode := range []string{"page", "all", "empty-page", "empty-all", "failure", "repeat"} {
				t.Run(fmt.Sprintf("%s/json=%t/%s", command, machine, mode), func(t *testing.T) {
					setupCLIRegression(t)
					var cursors []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						q := r.URL.Query()
						if r.Method != http.MethodGet || r.URL.Path != map[string]string{"orgs": "/v1/orgs/acme/activity", "delayed-task": "/v1/apps/demo/delayed-tasks"}[command] || q.Get("limit") != "2" || (command == "orgs" && (q.Get("kind_prefix") != "deploy." || q.Get("actor_type") != "github" || q.Get("app_id") != "11111111-1111-4111-8111-111111111111")) {
							t.Errorf("request=%s %s", r.Method, r.URL)
						}
						cursors = append(cursors, q.Get("before"))
						n := len(cursors)
						if mode == "failure" && n == 2 {
							w.WriteHeader(503)
							_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "unavailable", Title: "Unavailable"})
							return
						}
						next := ""
						if n == 1 {
							next = "next + opaque|cursor'quoted"
						}
						if mode == "repeat" {
							next = q.Get("before")
						}
						rows := []map[string]string{{"id": fmt.Sprintf("row-%d", n), "summary": fmt.Sprintf("row-%d", n), "state": "pending"}}
						if mode == "empty-page" || mode == "empty-all" {
							rows = nil
						}
						field := "items"
						if command == "delayed-task" {
							field = "tasks"
						}
						_ = json.NewEncoder(w).Encode(map[string]any{field: rows, "next_before": next})
					}))
					defer server.Close()
					t.Setenv("FAAS_API", server.URL)
					t.Setenv("FAAS_TOKEN", testAPIKey('a'))
					args := []string{command, "list", "--app", "demo", "--limit", "2", "--before", "ignored", "--cursor", "start + opaque|cursor"}
					if command == "orgs" {
						args = []string{"orgs", "activity", "--org", "acme", "--limit", "2", "--before", "ignored", "--cursor", "start + opaque|cursor", "--kind-prefix", "deploy.", "--actor-type", "github", "--app-id", "11111111-1111-4111-8111-111111111111"}
					}
					all := mode != "page" && mode != "empty-page"
					if all {
						args = append(args, "--all")
					}
					if machine {
						args = append(args, "--json")
					}
					code, out, errOut := capturePreviewRun(t, args)
					wantCursors := []string{"start + opaque|cursor"}
					if mode == "all" || mode == "empty-all" || mode == "failure" {
						wantCursors = append(wantCursors, "next + opaque|cursor'quoted")
					}
					if !reflect.DeepEqual(cursors, wantCursors) {
						t.Fatalf("cursors=%v", cursors)
					}
					if mode == "failure" || mode == "repeat" {
						wantCode := 1
						if mode == "failure" {
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
					if mode == "empty-page" || mode == "empty-all" {
						count = 0
					}
					if machine {
						var page struct {
							Items []struct {
								ID string `json:"id"`
							} `json:"items"`
							Tasks []struct {
								ID string `json:"id"`
							} `json:"tasks"`
							NextBefore string `json:"next_before"`
							NextCursor string `json:"next_cursor"`
						}
						if err := json.Unmarshal([]byte(out), &page); err != nil {
							t.Fatal(err)
						}
						if command == "delayed-task" {
							page.Items = page.Tasks
						}
						if page.Items == nil || len(page.Items) != count {
							t.Fatalf("page=%+v", page)
						}
						wantNext := "next + opaque|cursor'quoted"
						if all {
							wantNext = ""
						}
						if page.NextCursor != wantNext || page.NextBefore != wantNext {
							t.Fatalf("page cursors=%s/%s", page.NextCursor, page.NextBefore)
						}
						for i, row := range page.Items {
							id := row.ID

							if id != fmt.Sprintf("row-%d", i+1) {
								t.Fatalf("history=%+v", page.Items)
							}
						}
					} else {
						if !all && !strings.Contains(out, "--cursor 'next + opaque|cursor'\"'\"'quoted' --limit 2") {
							t.Fatalf("missing hint: %s", out)
						}
						if !all && command == "orgs" && !strings.Contains(out, "--kind-prefix 'deploy.' --actor-type 'github' --app-id '11111111-1111-4111-8111-111111111111'") {
							t.Fatalf("missing filters: %s", out)
						}
						if all && strings.Contains(out, "--cursor") {
							t.Fatalf("finished output has continuation: %s", out)
						}
						if count == 2 && (!strings.Contains(out, "row-1") || !strings.Contains(out, "row-2")) {
							t.Fatalf("incomplete history: %s", out)
						}
					}
				})
			}
		}
	}

}

func TestActivityAndDelayedTaskPaginationHelpAndValidation(t *testing.T) {
	for _, command := range []string{"orgs", "delayed-task"} {
		t.Run(command, func(t *testing.T) {
			setupCLIRegression(t)
			subcommand := "list"
			if command == "orgs" {
				subcommand = "activity"
			}
			code, out, errOut := capturePreviewRun(t, []string{command, subcommand, "--help"})
			if code != 0 || errOut != "" {
				t.Fatalf("exit=%d stderr=%s", code, errOut)
			}
			for _, flag := range []string{"--all", "--cursor", "--before", "--limit"} {
				if !strings.Contains(out, flag) {
					t.Errorf("missing %s", flag)
				}
			}
			for _, limit := range []string{"0", "201"} {
				args := []string{command, subcommand, "--all", "--app", "demo", "--limit", limit, "--json"}
				if command == "orgs" {
					args[3], args[4] = "--org", "acme"
					if limit == "201" {
						args[6] = "101"
					}
				}
				code, out, errOut = capturePreviewRun(t, args)
				if code != 1 || out != "" {
					t.Fatalf("limit=%s exit=%d stdout=%s", limit, code, out)
				}
				assertOneProblem(t, errOut)
			}
		})
	}
}
