package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261020202035: `gregale ai traces` lists an integration's AI traces
// and shows one trace's calls; `gregale ai policy` sets per-trace limits
// and keeps the ones it is not given.
func TestAITracesAndTraceLimits(t *testing.T) {
	main.ResetJSONOut(t)
	const integrationID = "11111111-2222-3333-4444-555555555555"
	app := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	last := time.Date(2026, 10, 20, 12, 0, 3, 0, time.UTC)
	trace := api.OutboundAITrace{TraceID: "task-1", AppID: &app, LastAt: last, Calls: 3, ErrorCalls: 1, InputTokens: 30, OutputTokens: 9,
		CostMillicents: 2_000, ToolCalls: 2, RepeatedToolCalls: 1, Models: []string{"gpt-5", "gpt-5-mini"}}
	kept := int64(9)
	var listQuery url.Values
	var policy api.PutOutboundAIPolicyRequest
	base := "/v1/outbound/integrations/" + integrationID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET " + base + "/ai-traces":
			listQuery = r.URL.Query()
			main.WriteJSONTest(w, api.OutboundAITraceList{Items: []api.OutboundAITrace{trace}})
		case "GET " + base + "/ai-traces/task-1":
			ttfb := int64(40)
			main.WriteJSONTest(w, api.OutboundAITraceResponse{Trace: trace, Calls: []api.OutboundAITraceCall{
				{CreatedAt: last, Model: "gpt-5", Status: 200, DurationMS: 120, TimeToFirstByteMS: &ttfb, ToolCalls: []string{"lookup", "send"}},
				{CreatedAt: last, Model: "gpt-5-mini", Status: 200, DurationMS: 80, ToolCalls: []string{}},
			}})
		case "GET /v1/outbound/integrations":
			main.WriteJSONTest(w, api.OutboundIntegrationOfferList{Items: []api.OutboundIntegrationOffer{{ID: integrationID,
				AIProvider: api.OutboundAIProviderOpenAI, AITraceMaxCalls: &kept, AITraceMaxRepeatedToolCalls: &kept}}})
		case "PUT " + base + "/ai":
			if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	if code := main.CmdAI([]string{"traces", integrationID, "--app", app, "--user", "u_1", "--limit", "5"}); code != 0 {
		t.Fatalf("ai traces exit = %d", code)
	}
	if listQuery.Get("app_id") != app || listQuery.Get("end_user") != "u_1" || listQuery.Get("limit") != "5" {
		t.Fatalf("list query = %v", listQuery)
	}
	if out := stdout.String(); !strings.Contains(out, "task-1") || !strings.Contains(out, "2026-10-20 12:00:03Z") ||
		!strings.Contains(out, "gpt-5,gpt-5-mini") {
		t.Fatalf("trace list:\n%s", out)
	}
	stdout.Reset()
	if code := main.CmdAI([]string{"traces", integrationID, "task-1"}); code != 0 {
		t.Fatalf("ai traces <trace> exit = %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "Trace: task-1") || !strings.Contains(out, "lookup,send") || !strings.Contains(out, "40ms") {
		t.Fatalf("trace detail:\n%s", out)
	}
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 4 || !strings.HasSuffix(strings.TrimSpace(lines[3]), "-") {
		t.Fatalf("a call without tools must show -:\n%s", out)
	}

	if code := main.CmdAI([]string{"policy", integrationID, "--trace-max-cost", "2", "--trace-max-repeated-tool-calls", "none"}); code != 0 {
		t.Fatalf("ai policy exit = %d", code)
	}
	if policy.TraceMaxCalls == nil || *policy.TraceMaxCalls != 9 || policy.TraceMaxCostMillicents == nil ||
		*policy.TraceMaxCostMillicents != 200_000 || policy.TraceMaxRepeatedToolCalls != nil {
		t.Fatalf("policy = %+v", policy)
	}
	for _, args := range [][]string{
		{"policy", integrationID, "--trace-max-calls", "0"},
		{"policy", integrationID, "--trace-max-repeated-tool-calls", "1001"},
		{"traces", integrationID, "--limit", "201"},
		{"traces"},
	} {
		if code := main.CmdAI(args); code == 0 {
			t.Errorf("%v exited 0", args)
		}
	}
}
