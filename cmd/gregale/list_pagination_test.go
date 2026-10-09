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
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestListPaginationTraversalGuards(t *testing.T) {
	for _, mode := range []string{"error", "repeat", "cycle", "bound", "cancel", "last-page"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			items, next, err := collectListPages(ctx, "start", true, func(_ context.Context, cursor string) ([]int, string, error) {
				calls++
				if mode == "error" && calls == 2 {
					return nil, "", errors.New("page failed")
				}
				if mode == "cancel" {
					cancel()
				}
				if mode == "repeat" {
					return []int{calls}, cursor, nil
				}
				if mode == "cycle" && calls == 3 {
					return []int{calls}, "page-1", nil
				}
				if mode == "last-page" && calls == maxCLIListPages {
					return []int{calls}, "", nil
				}
				return []int{calls}, fmt.Sprintf("page-%d", calls), nil
			})
			if mode == "last-page" {
				if err != nil || next != "" || len(items) != maxCLIListPages {
					t.Fatalf("items=%d next=%s err=%v", len(items), next, err)
				}
				return
			}
			if err == nil || items != nil || next != "" {
				t.Fatalf("partial results: items=%v next=%s err=%v", items, next, err)
			}
			if mode == "bound" && calls != maxCLIListPages {
				t.Fatalf("calls=%d", calls)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestListPaginationCommandContract(t *testing.T) {
	for _, kind := range []string{"account", "app", "invocations"} {
		for _, machine := range []bool{false, true} {
			for _, mode := range []string{"page", "all", "empty-page", "api-error", "repeat"} {
				t.Run(fmt.Sprintf("%s/json=%t/%s", kind, machine, mode), func(t *testing.T) {
					setupCLIRegression(t)
					// Isolate account listing from any ambient linked project.
					t.Chdir(t.TempDir())
					command := []string{"deployments"}
					path := "/v1/deployments"
					if kind == "app" {
						command = append(command, "--app", "demo")
						path = "/v1/apps/demo/deployments"
					}
					if kind == "invocations" {
						command = []string{"invocations", "list"}
						path = "/v1/invocations"
					}
					start, next := "opaque|start + value", "opaque|next + value"
					command = append(command, "--before", "ignored", "--cursor", start, "--limit", "2")
					if mode != "page" && mode != "empty-page" {
						command = append(command, "--all")
					}
					if machine {
						command = append(command, "--json")
					}
					var cursors []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet || r.URL.Path != path || r.URL.Query().Get("limit") != "2" {
							t.Errorf("request=%s %s", r.Method, r.URL.String())
						}
						cursor := r.URL.Query().Get("before")
						cursors = append(cursors, cursor)
						n := len(cursors)
						if mode == "api-error" && n == 2 {
							w.WriteHeader(503)
							_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "unavailable", Title: "Unavailable"})
							return
						}
						continuation := ""
						if n == 1 {
							continuation = next
						}
						if mode == "repeat" {
							continuation = start
						}
						ids := []string{fmt.Sprintf("row-%d", n)}
						if mode == "empty-page" {
							ids = nil
						}
						if kind == "invocations" {
							rows := make([]api.Invocation, 0)
							for _, id := range ids {
								rows = append(rows, api.Invocation{ID: id})
							}
							_ = json.NewEncoder(w).Encode(api.ListInvocationsResponse{Invocations: rows, NextBefore: continuation})
						} else {
							rows := make([]api.DeploymentResponse, 0)
							for _, id := range ids {
								rows = append(rows, api.DeploymentResponse{ID: id})
							}
							_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: rows, NextBefore: continuation})
						}
					}))
					defer server.Close()
					t.Setenv("FAAS_API", server.URL)
					t.Setenv("FAAS_TOKEN", testAPIKey('a'))
					code, out, errOut := capturePreviewRun(t, command)
					if mode == "api-error" || mode == "repeat" {
						if code == 0 || out != "" {
							t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errOut)
						}
						if machine {
							assertOneProblem(t, errOut)
						}
						return
					}
					want := []string{start}
					if mode == "all" {
						want = append(want, next)
					}
					if code != 0 || errOut != "" || !reflect.DeepEqual(cursors, want) {
						t.Fatalf("exit=%d cursors=%v want=%v stderr=%s", code, cursors, want, errOut)
					}
					if machine {
						if mode == "all" && kind != "invocations" {
							decoder := json.NewDecoder(strings.NewReader(out))
							for _, id := range []string{"row-1", "row-2"} {
								var row api.DeploymentResponse
								if err := decoder.Decode(&row); err != nil || row.ID != id {
									t.Fatalf("NDJSON row=%+v err=%v", row, err)
								}
							}
							var extra any
							if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
								t.Fatalf("extra NDJSON: %v", err)
							}
						} else {
							var envelope struct {
								Items       []api.DeploymentResponse `json:"items"`
								Invocations []api.Invocation         `json:"invocations"`
								NextBefore  string                   `json:"next_before"`
								NextCursor  string                   `json:"next_cursor"`
							}
							if err := json.Unmarshal([]byte(out), &envelope); err != nil {
								t.Fatal(err)
							}
							expectedNext := next
							count := 1
							if mode == "all" {
								expectedNext = ""
								count = 2
							}
							if mode == "empty-page" {
								count = 0
							}
							rows := len(envelope.Items)
							if kind == "invocations" {
								rows = len(envelope.Invocations)
							}
							if rows != count || envelope.NextCursor != expectedNext || envelope.NextBefore != expectedNext {
								t.Fatalf("envelope=%+v", envelope)
							}
						}
					} else {
						if mode != "all" && !strings.Contains(out, "--cursor "+next) {
							t.Fatalf("missing continuation: %s", out)
						}
						if mode == "all" && (!strings.Contains(out, "row-1") || !strings.Contains(out, "row-2") || strings.Contains(out, "--cursor")) {
							t.Fatalf("incomplete list: %s", out)
						}
					}
				})
			}
		}
	}
}

func TestListPaginationLimitsAndHelp(t *testing.T) {
	for _, command := range [][]string{{"deployments"}, {"invocations", "list"}} {
		invalidLimits := []string{"0", "-1", "201"}
		if command[0] == "invocations" {
			invalidLimits = append(invalidLimits, "101")
		}
		for _, limit := range invalidLimits {
			t.Run(strings.Join(command, "_")+limit, func(t *testing.T) {
				setupCLIRegression(t)
				t.Setenv("FAAS_API", "http://example.invalid")
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				args := append(append([]string{}, command...), "--limit", limit, "--json")
				code, out, errOut := capturePreviewRun(t, args)
				if code != 1 || out != "" {
					t.Fatalf("exit=%d stdout=%s", code, out)
				}
				assertOneProblem(t, errOut)
			})
		}
		t.Run(strings.Join(command, "_")+"help", func(t *testing.T) {
			setupCLIRegression(t)
			code, out, errOut := capturePreviewRun(t, append(append([]string{}, command...), "--help"))
			if code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, errOut)
			}
			for _, flag := range []string{"--limit", "--cursor", "--before", "--all"} {
				if !strings.Contains(out, flag) {
					t.Errorf("help missing %s: %s", flag, out)
				}
			}
		})
	}
}

func TestListPaginationCompletionMetadata(t *testing.T) {
	for _, command := range customerCliCommands() {
		var flags []cliFlag
		switch command.Name {
		case "deployments":
			flags = command.Flags
		case "invocations", "build":
			for _, sub := range command.Subcommands {
				if sub.Name == "list" {
					flags = sub.Flags
				}
			}
		default:
			continue
		}
		found := map[string]cliFlag{}
		for _, flag := range flags {
			found[flag.Name] = flag
		}
		for _, name := range []string{"limit", "cursor", "before", "all"} {
			flag, ok := found[name]
			if !ok {
				t.Fatalf("%s missing %s", command.Name, name)
			}
			if name != "all" && flag.Value == "" {
				t.Errorf("%s --%s missing value metadata", command.Name, name)
			}
		}
	}
}

func TestListPaginationEmptyAndSinglePage(t *testing.T) {
	calls := 0
	items, next, err := collectListPages(context.Background(), "start", false, func(_ context.Context, cursor string) ([]int, string, error) {
		calls++
		if cursor != "start" {
			t.Errorf("cursor=%s", cursor)
		}
		return nil, "next", nil
	})
	if err != nil || calls != 1 || items == nil || len(items) != 0 || next != "next" {
		t.Fatalf("items=%v next=%s calls=%d err=%v", items, next, calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = collectListPages(ctx, "", true, func(context.Context, string) ([]int, string, error) {
		t.Fatal("cancelled traversal requested a page")
		return nil, "", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestListPaginationAcceptedLimitBoundaries(t *testing.T) {
	for _, kind := range []string{"deployments", "invocations"} {
		maximum := 200
		if kind == "invocations" {
			maximum = 100
		}
		for _, limit := range []int{1, maximum} {
			t.Run(fmt.Sprintf("%s/%d", kind, limit), func(t *testing.T) {
				setupCLIRegression(t)
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					if r.URL.Query().Get("limit") != fmt.Sprint(limit) || r.URL.Query().Get("before") != "legacy-cursor" {
						t.Errorf("query=%s", r.URL.RawQuery)
					}
					if kind == "invocations" {
						_ = json.NewEncoder(w).Encode(api.ListInvocationsResponse{Invocations: []api.Invocation{}})
					} else {
						_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{}})
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				command := []string{"deployments", "--app", "demo"}
				if kind == "invocations" {
					command = []string{"invocations", "list"}
				}
				command = append(command, "--cursor", "ignored", "--before", "legacy-cursor", "--limit", fmt.Sprint(limit), "--all", "--json")
				code, out, errOut := capturePreviewRun(t, command)
				if code != 0 || requests != 1 || errOut != "" {
					t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, requests, out, errOut)
				}
				if kind == "deployments" && out != "" {
					t.Fatalf("empty NDJSON=%s", out)
				}
				if kind == "invocations" && !strings.Contains(out, `"invocations": []`) {
					t.Fatalf("empty envelope=%s", out)
				}
			})
		}
	}
}
