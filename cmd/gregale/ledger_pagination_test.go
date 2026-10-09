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

func ledgerPaginationCommand(kind string) []string {
	if kind == "webhooks" {
		return []string{"webhooks", "deliveries", webhookTestID, "--app", "demo", "--status", "failed"}
	}
	return []string{"queue", kind, "demo"}
}

func TestLedgerPaginationContract(t *testing.T) {
	for _, kind := range []string{"webhooks", "peek", "dead-letter"} {
		for _, machine := range []bool{false, true} {
			for _, mode := range []string{"page", "all", "empty-page", "empty", "api-error", "repeat", "cycle"} {
				t.Run(fmt.Sprintf("%s/json=%t/%s", kind, machine, mode), func(t *testing.T) {
					setupCLIRegression(t)
					start, next := "start| + opaque", "next| + opaque"
					cursorKey, limitKey, path := "before", "limit", "/v1/apps/demo/queues/"+kind
					if kind == "dead-letter" {
						path = "/v1/apps/demo/queues/dead_letter"
					}
					if kind == "webhooks" {
						cursorKey, limitKey, path = "page_token", "page_size", "/v1/apps/demo/webhooks/"+webhookTestID+"/deliveries"
					}
					var cursors []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet {
							t.Errorf("inspection mutated resources: %s %s", r.Method, r.URL)
							w.WriteHeader(500)
							return
						}
						q := r.URL.Query()
						if r.URL.Path != path || q.Get(limitKey) != "2" || kind == "webhooks" && q.Get("status") != "failed" {
							t.Errorf("request=%s", r.URL)
						}
						cursor := q.Get(cursorKey)
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
						id := fmt.Sprintf("row-%d", n)
						empty := mode == "empty" || mode == "empty-page"
						switch kind {
						case "webhooks":
							rows := []api.AppWebhookDeliveryResponse{{ID: id, Status: "failed"}}
							if empty {
								rows = nil
							}
							_ = json.NewEncoder(w).Encode(api.AppWebhookDeliveryListResponse{Deliveries: rows, NextToken: continuation})
						case "peek":
							rows := []api.QueuePeekMessage{{ID: id}}
							if empty {
								rows = nil
							}
							_ = json.NewEncoder(w).Encode(api.QueuePeekResponse{AppSlug: "demo", Messages: rows, NextBefore: continuation})
						default:
							rows := []api.QueueDeadLetterMessage{{ID: id}}
							if empty {
								rows = nil
							}
							_ = json.NewEncoder(w).Encode(api.QueueDeadLetterResponse{AppSlug: "demo", Messages: rows, NextBefore: continuation})
						}
					}))
					defer server.Close()
					t.Setenv("FAAS_API", server.URL)
					t.Setenv("FAAS_TOKEN", testAPIKey('a'))
					command := ledgerPaginationCommand(kind)
					legacy := "--before"
					if kind == "webhooks" {
						legacy = "--page-token"
						command = append(command, "--page-size", "3")
					}
					command = append(command, legacy, "ignored", "--cursor", start, "--limit", "2")
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
						want := 1
						if mode == "api-error" {
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
					if code != 0 {
						t.Fatalf("exit=%d stderr=%s", code, errOut)
					}
					count := 1
					if mode == "all" {
						count = 2
					}
					if mode == "empty" || mode == "empty-page" {
						count = 0
					}
					expectedNext := next
					if all {
						expectedNext = ""
					}
					if machine {
						if errOut != "" {
							t.Fatalf("JSON stderr=%s", errOut)
						}
						var envelope map[string]json.RawMessage
						if err := json.Unmarshal([]byte(out), &envelope); err != nil {
							t.Fatalf("not one JSON envelope: %v: %s", err, out)
						}
						rowKey, legacyKey := "messages", "next_before"
						if kind == "webhooks" {
							rowKey, legacyKey = "deliveries", "next_token"
						} else {
							var slug string
							_ = json.Unmarshal(envelope["app_slug"], &slug)
							if slug != "demo" {
								t.Fatalf("app_slug=%s", slug)
							}
						}
						var rows []struct {
							ID string `json:"id"`
						}
						if err := json.Unmarshal(envelope[rowKey], &rows); err != nil || rows == nil || len(rows) != count {
							t.Fatalf("rows=%v err=%v output=%s", rows, err, out)
						}
						for i, row := range rows {
							if row.ID != fmt.Sprintf("row-%d", i+1) {
								t.Fatalf("rows reordered or duplicated: %v", rows)
							}
						}
						for _, key := range []string{legacyKey, "next_cursor"} {
							raw, exists := envelope[key]
							if expectedNext == "" {
								if exists {
									t.Errorf("finished envelope retains %s", key)
								}
							} else {
								var cursor string
								if err := json.Unmarshal(raw, &cursor); err != nil || cursor != expectedNext {
									t.Errorf("%s=%s error=%v", key, raw, err)
								}
							}
						}
					} else {
						text := out + errOut
						if expectedNext != "" && !strings.Contains(text, "--cursor "+expectedNext) {
							t.Fatalf("missing hint: %s", text)
						}
						if all && strings.Contains(text, "--cursor") {
							t.Fatalf("finished list has cursor: %s", text)
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

func TestLedgerPaginationLimitsAndLegacyAliases(t *testing.T) {
	for _, kind := range []string{"webhooks", "peek", "dead-letter"} {
		for _, limit := range []int{0, 1, 100, 101} {
			t.Run(fmt.Sprintf("%s/%d", kind, limit), func(t *testing.T) {
				setupCLIRegression(t)
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					sizeKey, cursorKey := "limit", "before"
					if kind == "webhooks" {
						sizeKey, cursorKey = "page_size", "page_token"
					}
					if r.Method != http.MethodGet || r.URL.Query().Get(sizeKey) != fmt.Sprint(limit) || r.URL.Query().Get(cursorKey) != "legacy" {
						t.Errorf("request=%s %s", r.Method, r.URL)
					}
					if kind == "webhooks" {
						_ = json.NewEncoder(w).Encode(api.AppWebhookDeliveryListResponse{})
					} else if kind == "peek" {
						_ = json.NewEncoder(w).Encode(api.QueuePeekResponse{AppSlug: "demo"})
					} else {
						_ = json.NewEncoder(w).Encode(api.QueueDeadLetterResponse{AppSlug: "demo"})
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				command := ledgerPaginationCommand(kind)
				legacy := "--before"
				if kind == "webhooks" {
					legacy = "--page-token"
				}
				command = append(command, "--cursor", "ignored", legacy, "legacy", "--limit", fmt.Sprint(limit))
				if kind == "webhooks" {
					command = append(command, "--limit", "2", "--page-size", fmt.Sprint(limit))
				}
				command = append(command, "--all", "--json")
				code, out, errOut := capturePreviewRun(t, command)
				if limit < 1 || limit > 100 {
					if code != 1 || calls != 0 || out != "" {
						t.Fatalf("exit=%d calls=%d output=%s", code, calls, out)
					}
					assertOneProblem(t, errOut)
				} else if code != 0 || calls != 1 || errOut != "" {
					t.Fatalf("exit=%d calls=%d output=%s stderr=%s", code, calls, out, errOut)
				}
			})
		}
	}
}

func TestLedgerPaginationHelp(t *testing.T) {
	for _, kind := range []string{"webhooks", "peek", "dead-letter"} {
		t.Run(kind, func(t *testing.T) {
			setupCLIRegression(t)
			command := []string{"queue", kind, "--help"}
			legacy := []string{"--before"}
			if kind == "webhooks" {
				command = []string{"webhooks", "deliveries", "--help"}
				legacy = []string{"--page-token", "--page-size"}
			}
			code, out, errOut := capturePreviewRun(t, command)
			if code != 0 || errOut != "" {
				t.Fatalf("exit=%d stderr=%s", code, errOut)
			}
			for _, flag := range append([]string{"--cursor", "--limit", "--all"}, legacy...) {
				if !strings.Contains(out, flag) {
					t.Errorf("help missing %s: %s", flag, out)
				}
			}
		})
	}
}
