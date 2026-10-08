package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEventDeliveryPaginationUnevenStreams(t *testing.T) {
	for _, short := range []string{"deliveries", "fanout"} {
		for _, machine := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", short, machine), func(t *testing.T) {
				setupCLIRegression(t)
				startD, startF := "delivery|start + token", "fanout|start + token"
				var cursors [][2]string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					q := r.URL.Query()
					if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/event-deliveries" || q.Get("event_source") != "orders" || q.Get("event_id") != "event-1" || q.Get("state") != "failed" || q.Get("limit") != "2" {
						t.Errorf("request=%s %s", r.Method, r.URL)
					}
					cursors = append(cursors, [2]string{q.Get("before"), q.Get("fanout_before")})
					n := len(cursors)
					page := api.EventDeliveryListResponse{AppSlug: "demo", Deliveries: []api.EventDeliveryResponse{{InvocationID: fmt.Sprintf("delivery-%d", n)}}, FanoutFailures: []api.EventFanoutFailureResponse{{SubscriptionID: fmt.Sprintf("fanout-%d", n)}}}
					if short == "deliveries" {
						page.NextFanoutBefore = fmt.Sprintf("f%d", n)
						if n == 3 {
							page.NextFanoutBefore = ""
						}
						if n > 1 {
							page.NextBefore = "restarted-deliveries"
						}
					} else {
						page.NextBefore = fmt.Sprintf("d%d", n)
						if n == 3 {
							page.NextBefore = ""
						}
						if n > 1 {
							page.NextFanoutBefore = "restarted-fanout"
						}
					}
					_ = json.NewEncoder(w).Encode(page)
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				args := []string{"events", "deliveries", "--all", "demo", "--event-source", "orders", "--event-id", "event-1", "--state", "failed", "--limit", "2", "--before", startD, "--fanout-before", startF}
				if machine {
					args = append(args, "--json")
				}
				code, out, errOut := capturePreviewRun(t, args)
				want := [][2]string{{startD, startF}, {startD, "f1"}, {startD, "f2"}}
				if short == "fanout" {
					want = [][2]string{{startD, startF}, {"d1", startF}, {"d2", startF}}
				}
				if code != 0 || errOut != "" || !reflect.DeepEqual(cursors, want) {
					t.Fatalf("exit=%d cursors=%v want=%v stderr=%s", code, cursors, want, errOut)
				}
				dCount, fCount := 1, 3
				if short == "fanout" {
					dCount, fCount = 3, 1
				}
				if machine {
					var page api.EventDeliveryListResponse
					if err := json.Unmarshal([]byte(out), &page); err != nil {
						t.Fatal(err)
					}
					if page.AppSlug != "demo" || len(page.Deliveries) != dCount || len(page.FanoutFailures) != fCount || page.NextBefore != "" || page.NextFanoutBefore != "" {
						t.Fatalf("page=%+v", page)
					}
					for i, row := range page.Deliveries {
						if row.InvocationID != fmt.Sprintf("delivery-%d", i+1) {
							t.Fatal("delivery order changed")
						}
					}
					for i, row := range page.FanoutFailures {
						if row.SubscriptionID != fmt.Sprintf("fanout-%d", i+1) {
							t.Fatal("fanout order changed")
						}
					}
				} else {
					if strings.Contains(out, "--before") || strings.Contains(out, "--fanout-before") {
						t.Fatalf("finished output contains cursor: %s", out)
					}
					if short == "deliveries" && strings.Contains(out, "delivery-2") || short == "fanout" && strings.Contains(out, "fanout-2") {
						t.Fatalf("exhausted stream duplicated: %s", out)
					}
				}
			})
		}
	}
}

func TestEventDeliveryPaginationGuards(t *testing.T) {
	for _, mode := range []string{"delivery-repeat", "fanout-repeat", "delivery-cycle", "fanout-cycle", "failure", "cancel", "bound", "last-page"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			out, err := collectEventDeliveryPages(ctx, "d0", "f0", true, func(_ context.Context, d, f string) (api.EventDeliveryListResponse, error) {
				calls++
				if mode == "failure" && calls == 2 {
					return api.EventDeliveryListResponse{}, errors.New("failed fetch")
				}
				if mode == "cancel" {
					cancel()
				}
				page := api.EventDeliveryListResponse{Deliveries: []api.EventDeliveryResponse{{InvocationID: "row"}}, FanoutFailures: []api.EventFanoutFailureResponse{{SubscriptionID: "row"}}, NextBefore: fmt.Sprintf("d%d", calls), NextFanoutBefore: fmt.Sprintf("f%d", calls)}
				switch mode {
				case "delivery-repeat":
					page.NextBefore = d
				case "fanout-repeat":
					page.NextFanoutBefore = f
				case "delivery-cycle":
					if calls == 3 {
						page.NextBefore = "d1"
					}
				case "fanout-cycle":
					if calls == 3 {
						page.NextFanoutBefore = "f1"
					}
				case "last-page":
					if calls == maxCLIListPages {
						page.NextBefore, page.NextFanoutBefore = "", ""
					}
				}
				return page, nil
			})
			if mode == "last-page" {
				if err != nil || len(out.Deliveries) != maxCLIListPages || len(out.FanoutFailures) != maxCLIListPages {
					t.Fatalf("counts=%d/%d err=%v", len(out.Deliveries), len(out.FanoutFailures), err)
				}
				return
			}
			if err == nil || out.Deliveries != nil || out.FanoutFailures != nil {
				t.Fatalf("partial result=%+v err=%v", out, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v", err)
			}
			if mode == "bound" && calls != maxCLIListPages {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestEventDeliveryPaginationCommandFailuresAndEmptyPages(t *testing.T) {
	for _, mode := range []string{"failure", "delivery-repeat", "fanout-repeat", "empty-page", "empty-all"} {
		for _, machine := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", mode, machine), func(t *testing.T) {
				setupCLIRegression(t)
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodGet {
						t.Error("mutation during listing")
					}
					if mode == "failure" && calls == 2 {
						w.WriteHeader(503)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Code: "unavailable", Title: "Unavailable"})
						return
					}
					page := api.EventDeliveryListResponse{AppSlug: "demo"}
					if mode != "empty-all" {
						page.NextBefore, page.NextFanoutBefore = "d1", "f1"
					}
					if mode == "delivery-repeat" {
						page.NextBefore = "d0"
					}
					if mode == "fanout-repeat" {
						page.NextFanoutBefore = "f0"
					}
					if mode == "failure" {
						page.Deliveries = []api.EventDeliveryResponse{{InvocationID: "partial"}}
					}
					_ = json.NewEncoder(w).Encode(page)
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", testAPIKey('a'))
				args := []string{"events", "deliveries", "demo", "--before", "d0", "--fanout-before", "f0"}
				if mode != "empty-page" {
					args = append(args, "--all")
				}
				if machine {
					args = append(args, "--json")
				}
				code, out, errOut := capturePreviewRun(t, args)
				if mode == "failure" || strings.HasSuffix(mode, "repeat") {
					want := 1
					if mode == "failure" {
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
				if code != 0 || calls != 1 || errOut != "" {
					t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, errOut)
				}
				if machine {
					var page api.EventDeliveryListResponse
					if err := json.Unmarshal([]byte(out), &page); err != nil {
						t.Fatal(err)
					}
					if page.Deliveries == nil || page.FanoutFailures == nil {
						t.Fatal("empty collections must be arrays")
					}
					if mode == "empty-page" && (page.NextBefore != "d1" || page.NextFanoutBefore != "f1") {
						t.Fatal("lost cursors")
					}
				} else if mode == "empty-page" && (!strings.Contains(out, "--before d1") || !strings.Contains(out, "--fanout-before f1")) {
					t.Fatalf("missing continuation hints: %s", out)
				}
			})
		}
	}
}
