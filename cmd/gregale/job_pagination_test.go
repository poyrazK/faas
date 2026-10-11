package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestJobPaginationTraversalGuards(t *testing.T) {
	for _, mode := range []string{"repeat", "backward", "invalid", "failure", "bound", "cancel", "last-page"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			rows, next, err := collectOffsetPages(ctx, 5, true, func(_ context.Context, offset int) ([]int, int, error) {
				calls++
				if mode == "failure" && calls == 2 {
					return nil, -1, errors.New("failed page")
				}
				if mode == "cancel" {
					cancel()
				}
				switch mode {
				case "repeat":
					return []int{calls}, offset, nil
				case "backward":
					return []int{calls}, offset - 1, nil
				case "invalid":
					return []int{calls}, -2, nil
				case "last-page":
					if calls == maxCLIListPages {
						return []int{calls}, -1, nil
					}
				}
				return []int{calls}, offset + 1, nil
			})
			if mode == "last-page" {
				if err != nil || next != -1 || len(rows) != maxCLIListPages {
					t.Fatalf("rows=%d next=%d err=%v", len(rows), next, err)
				}
				return
			}
			if err == nil || rows != nil || next != -1 {
				t.Fatalf("partial results: rows=%v next=%d err=%v", rows, next, err)
			}
			if mode == "bound" && calls != maxCLIListPages {
				t.Fatalf("calls=%d", calls)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestJobPaginationCommandContract(t *testing.T) {
	for _, kind := range []string{"list", "runs"} {
		for _, machine := range []bool{false, true} {
			for _, mode := range []string{"page", "all", "empty", "empty-page", "error", "repeat", "backward", "invalid"} {
				t.Run(fmt.Sprintf("%s/json=%t/%s", kind, machine, mode), func(t *testing.T) {
					setupCLIRegression(t)
					var offsets []int
					path := "/v1/jobs"
					if kind == "runs" {
						path += "/nightly/runs"
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
						if err != nil {
							t.Error(err)
						}
						if r.Method != http.MethodGet || r.URL.Path != path || r.URL.Query().Get("limit") != "2" {
							t.Errorf("request=%s %s", r.Method, r.URL)
						}
						offsets = append(offsets, offset)
						n := len(offsets)
						if mode == "error" && n == 2 {
							w.WriteHeader(503)
							_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "unavailable", Title: "Unavailable"})
							return
						}
						next := -1
						if n == 1 && mode != "empty" {
							next = offset + 2
						}
						if mode == "repeat" {
							next = offset
						}
						if mode == "backward" {
							next = offset - 1
						}
						if mode == "invalid" {
							next = -2
						}
						id := fmt.Sprintf("row-%d", n)
						empty := mode == "empty" || mode == "empty-page"
						if kind == "list" {
							rows := []api.JobResponse{{ID: id, Name: id}}
							if empty {
								rows = nil
							}
							_ = json.NewEncoder(w).Encode(api.ListJobsResponse{Jobs: rows, Limit: 2, Offset: offset, NextOffset: next, Total: 10 + n})
						} else {
							rows := []api.JobRunResponse{{ID: id, JobID: "job-1"}}
							if empty {
								rows = nil
							}
							_ = json.NewEncoder(w).Encode(api.ListJobRunsResponse{Runs: rows, Limit: 2, Offset: offset, NextOffset: next, Total: 10 + n})
						}
					}))
					defer server.Close()
					t.Setenv("FAAS_API", server.URL)
					t.Setenv("FAAS_TOKEN", testAPIKey('a'))
					command := []string{"jobs", kind}
					if kind == "runs" {
						command = append(command, "nightly")
					}
					command = append(command, "--limit", "2", "--offset", "5")
					all := mode != "page" && mode != "empty-page"
					if all {
						command = append(command, "--all")
					}
					if machine {
						command = append(command, "--json")
					}
					code, out, errOut := capturePreviewRun(t, command)
					expectedOffsets := []int{5}
					if mode == "all" || mode == "error" {
						expectedOffsets = append(expectedOffsets, 7)
					}
					if !reflect.DeepEqual(offsets, expectedOffsets) {
						t.Fatalf("offsets=%v want=%v", offsets, expectedOffsets)
					}
					if mode == "error" || mode == "repeat" || mode == "backward" || mode == "invalid" {
						want := 1
						if mode == "error" {
							want = 3
						}
						if code != want || out != "" {
							t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errOut)
						}
						if machine {
							assertOneProblem(t, errOut)
						}
						return
					}
					if code != 0 || errOut != "" {
						t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errOut)
					}
					count := 1
					if mode == "all" {
						count = 2
					}
					if mode == "empty" || mode == "empty-page" {
						count = 0
					}
					if machine {
						if kind == "list" {
							var page api.ListJobsResponse
							if err := json.Unmarshal([]byte(out), &page); err != nil {
								t.Fatal(err)
							}
							next := -1
							if !all {
								next = 7
							}
							if page.Jobs == nil || len(page.Jobs) != count || page.Limit != 2 || page.Offset != 5 || page.NextOffset != next || page.Total != 10+len(offsets) {
								t.Fatalf("envelope=%+v", page)
							}
							for i, row := range page.Jobs {
								if row.ID != fmt.Sprintf("row-%d", i+1) {
									t.Fatalf("rows=%+v", page.Jobs)
								}
							}
						} else {
							decoder := json.NewDecoder(strings.NewReader(out))
							for i := 0; i < count; i++ {
								var row api.JobRunResponse
								if err := decoder.Decode(&row); err != nil || row.ID != fmt.Sprintf("row-%d", i+1) {
									t.Fatalf("NDJSON row=%+v err=%v", row, err)
								}
							}
							var extra any
							if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
								t.Fatalf("extra NDJSON record: %v", err)
							}
						}
					} else {
						if !all && !strings.Contains(out, "--offset 7") {
							t.Fatalf("missing continuation: %s", out)
						}
						if all && strings.Contains(out, "--offset") {
							t.Fatalf("finished list has continuation: %s", out)
						}
						if count == 2 && (!strings.Contains(out, "row-1") || !strings.Contains(out, "row-2")) {
							t.Fatalf("incomplete list: %s", out)
						}
					}
				})
			}
		}
	}
}

func TestJobPaginationLimitsAndHelp(t *testing.T) {
	for _, kind := range []string{"list", "runs"} {
		for _, limit := range []int{-1, 0, 1, 200, 201} {
			t.Run(fmt.Sprintf("%s/%d", kind, limit), func(t *testing.T) {
				setupCLIRegression(t)
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodGet || r.URL.Query().Get("limit") != fmt.Sprint(limit) {
						t.Errorf("request=%s %s", r.Method, r.URL)
					}
					if kind == "list" {
						_ = json.NewEncoder(w).Encode(api.ListJobsResponse{NextOffset: -1})
					} else {
						_ = json.NewEncoder(w).Encode(api.ListJobRunsResponse{NextOffset: -1})
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				args := []string{"jobs", kind}
				if kind == "runs" {
					args = append(args, "nightly")
				}
				args = append(args, "--limit", fmt.Sprint(limit), "--all", "--json")
				code, out, errOut := capturePreviewRun(t, args)
				if limit < 1 || limit > 200 {
					if code != 1 || calls != 0 || out != "" {
						t.Fatalf("exit=%d calls=%d stdout=%s", code, calls, out)
					}
					assertOneProblem(t, errOut)
				} else if code != 0 || calls != 1 || errOut != "" {
					t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, errOut)
				}
			})
		}
		t.Run(kind+"help", func(t *testing.T) {
			setupCLIRegression(t)
			code, out, errOut := capturePreviewRun(t, []string{"jobs", kind, "--help"})
			if code != 0 || errOut != "" {
				t.Fatalf("exit=%d stderr=%s", code, errOut)
			}
			for _, flag := range []string{"--limit", "--offset", "--all"} {
				if !strings.Contains(out, flag) {
					t.Errorf("missing %s: %s", flag, out)
				}
			}
		})
		t.Run(kind+"negative-offset", func(t *testing.T) {
			setupCLIRegression(t)
			args := []string{"jobs", kind}
			if kind == "runs" {
				args = append(args, "nightly")
			}
			args = append(args, "--offset=-1", "--json")
			code, out, errOut := capturePreviewRun(t, args)
			if code != 1 || out != "" {
				t.Fatalf("exit=%d stdout=%s", code, out)
			}
			assertOneProblem(t, errOut)
		})
	}
}
