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

func TestScheduleOccurrencePaginationContract(t *testing.T) {
	for _, command := range []string{"jobs", "crons"} {
		target := "nightly"
		if command == "crons" {
			target = cronsRunsID
		}
		for _, machine := range []bool{false, true} {
			for _, mode := range []string{"page", "all", "empty-page", "failure", "repeat"} {
				t.Run(fmt.Sprintf("%s/json=%t/%s", command, machine, mode), func(t *testing.T) {
					setupCLIRegression(t)
					var cursors []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						q := r.URL.Query()
						if r.Method != http.MethodGet || r.URL.Path != "/v1/"+command+"/"+target+"/occurrences" || q.Get("limit") != "2" {
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
							next = "next + opaque|cursor"
						}
						if mode == "repeat" {
							next = q.Get("before")
						}
						rows := []api.ScheduleOccurrenceResponse{{ID: fmt.Sprintf("row-%d", n), Status: "skipped"}}
						if mode == "empty-page" {
							rows = nil
						}
						_ = json.NewEncoder(w).Encode(api.ListScheduleOccurrencesResponse{Occurrences: rows, Limit: 2, Before: q.Get("before"), NextBefore: next})
					}))
					defer server.Close()
					t.Setenv("FAAS_API", server.URL)
					t.Setenv("FAAS_TOKEN", testAPIKey('a'))
					args := []string{command, "occurrences", "--limit", "2", "--before", "ignored", "--cursor", "start + opaque|cursor", target}
					all := mode != "page" && mode != "empty-page"
					if all {
						args = append(args, "--all")
					}
					if machine {
						args = append(args, "--json")
					}
					code, out, errOut := capturePreviewRun(t, args)
					wantCursors := []string{"start + opaque|cursor"}
					if mode == "all" || mode == "failure" {
						wantCursors = append(wantCursors, "next + opaque|cursor")
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
					if mode == "empty-page" {
						count = 0
					}
					if machine {
						var page struct {
							api.ListScheduleOccurrencesResponse
							NextCursor string `json:"next_cursor"`
						}
						if err := json.Unmarshal([]byte(out), &page); err != nil {
							t.Fatal(err)
						}
						if page.Limit != 2 || page.Before != "start + opaque|cursor" || page.Occurrences == nil || len(page.Occurrences) != count {
							t.Fatalf("page=%+v", page)
						}
						wantNext := "next + opaque|cursor"
						if all {
							wantNext = ""
						}
						if page.NextCursor != wantNext || page.NextBefore != wantNext {
							t.Fatalf("page cursors=%s/%s", page.NextCursor, page.NextBefore)
						}
						for i, row := range page.Occurrences {
							if row.ID != fmt.Sprintf("row-%d", i+1) {
								t.Fatalf("history=%+v", page.Occurrences)
							}
						}
					} else {
						if !all && !strings.Contains(out, "--cursor next + opaque|cursor") {
							t.Fatalf("missing hint: %s", out)
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

func TestScheduleOccurrencePaginationHelpAndValidation(t *testing.T) {
	for _, command := range []string{"jobs", "crons"} {
		t.Run(command, func(t *testing.T) {
			setupCLIRegression(t)
			code, out, errOut := capturePreviewRun(t, []string{command, "occurrences", "--help"})
			if code != 0 || errOut != "" {
				t.Fatalf("exit=%d stderr=%s", code, errOut)
			}
			for _, flag := range []string{"--all", "--cursor", "--before", "--limit"} {
				if !strings.Contains(out, flag) {
					t.Errorf("missing %s", flag)
				}
			}
			target := "nightly"
			if command == "crons" {
				target = cronsRunsID
			}
			for _, limit := range []string{"0", "201"} {
				code, out, errOut = capturePreviewRun(t, []string{command, "occurrences", "--all", target, "--limit", limit, "--json"})
				if code != 1 || out != "" {
					t.Fatalf("limit=%s exit=%d stdout=%s", limit, code, out)
				}
				assertOneProblem(t, errOut)
			}
		})
	}
}
