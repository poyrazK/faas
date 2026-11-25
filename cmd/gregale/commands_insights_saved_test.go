package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261122190000 §2–§3: `gregale insight saved ...` builds each kind's
// request, replaces with PUT and exports a run as CSV.
func TestInsightSavedCommands(t *testing.T) {
	var reqs []api.SavedInsightRequest
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		base := "/v1/projects/shop/environments/production/insights/saved"
		value := 0.25
		saved := api.SavedInsight{SavedInsightRequest: api.SavedInsightRequest{Key: "conv", Name: "conv", Kind: "funnel", RangeDays: 14,
			Digest: "weekly", Alert: &api.SavedInsightAlert{Comparison: "lt", Threshold: 0.2}}, AlertState: "ok", LastValue: &value}
		switch r.Method + " " + r.URL.Path {
		case "POST " + base, "PUT " + base + "/conv":
			var req api.SavedInsightRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			reqs, methods = append(reqs, req), append(methods, r.Method)
			_ = json.NewEncoder(w).Encode(api.SavedInsight{SavedInsightRequest: req, AlertState: "ok"})
		case "GET " + base:
			_ = json.NewEncoder(w).Encode(api.SavedInsightList{Insights: []api.SavedInsight{saved}})
		case "GET " + base + "/conv/run":
			at := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
			_ = json.NewEncoder(w).Encode(api.SavedInsightResult{Key: "conv", Kind: "funnel", Value: &value, From: at.AddDate(0, 0, -14), Until: at,
				Funnel: &api.InsightFunnel{Unit: "customer", Window: "168h0m0s", Steps: []api.InsightFunnelStep{
					{Event: "signup", Units: 4, ConversionFromFirst: 1, ConversionFromPrev: 1},
					{Event: "order", Units: 1, ConversionFromFirst: 0.25, ConversionFromPrev: 0.25}}}})
		case "DELETE " + base + "/conv":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	run := func(args ...string) (int, string, string) {
		main.ResetJSONOut(t)
		stdout, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		restore()
		return code, stdout.String(), stderr()
	}

	code, out, errOut := run("insight", "saved", "create", "conv", "--project", "shop", "--kind", "funnel", "--step", "signup", "--step", "order",
		"--days", "14", "--digest", "weekly", "--alert", "lt:0.2", "--channel", "6f1c2c55-6a3f-4d7c-9a51-1d0c1f1b8e01")
	if code != 0 || len(reqs) != 1 || methods[0] != http.MethodPost || reqs[0].Funnel == nil || len(reqs[0].Funnel.Steps) != 2 ||
		reqs[0].RangeDays != 14 || reqs[0].Alert == nil || reqs[0].Alert.Comparison != "lt" || reqs[0].Alert.Threshold != 0.2 ||
		len(reqs[0].ChannelIDs) != 1 || !strings.Contains(out, "insight saved run conv") {
		t.Fatalf("save funnel = %d %q %q; requests %+v", code, out, errOut, reqs)
	}
	code, _, errOut = run("insights", "saved", "save", "conv", "--project", "shop", "--kind", "retention", "--start-event", "signup",
		"--return-event", "visit", "--period", "week", "--replace")
	if code != 0 || len(reqs) != 2 || methods[1] != http.MethodPut || reqs[1].Retention == nil || reqs[1].Retention.Return.Event != "visit" {
		t.Fatalf("replace retention = %d %q; requests %+v", code, errOut, reqs)
	}
	if code, out, _ = run("insight", "saved", "list", "--project", "shop"); code != 0 || !strings.Contains(out, "25.0%") ||
		!strings.Contains(out, "weekly digest, alert lt 20.0% (ok)") {
		t.Fatalf("list = %d %q", code, out)
	}
	if code, out, _ = run("insight", "saved", "run", "conv", "--project", "shop", "--format", "csv"); code != 0 ||
		!strings.HasPrefix(out, "breakdown,step,event,units,conversion_from_first,conversion_from_previous\n") || !strings.Contains(out, ",2,order,1,0.25,0.25") {
		t.Fatalf("run csv = %d %q", code, out)
	}
	if code, out, _ = run("insight", "saved", "run", "conv", "--project", "shop"); code != 0 || !strings.Contains(out, "value 25.0%") {
		t.Fatalf("run = %d %q", code, out)
	}
	if code, out, _ = run("insight", "saved", "delete", "conv", "--project", "shop"); code != 0 || !strings.Contains(out, "Deleted saved insight conv") {
		t.Fatalf("delete = %d %q", code, out)
	}
	for _, args := range [][]string{
		{"insight", "saved", "create", "x", "--project", "shop", "--kind", "table"},
		{"insight", "saved", "create", "x", "--project", "shop", "--kind", "events", "--event", "e", "--alert", "below:3"},
		{"insight", "saved", "run", "conv", "--project", "shop", "--format", "xml"},
	} {
		if code, _, errOut := run(args...); code == 0 || errOut == "" {
			t.Errorf("%v = %d %q; want a usage error", args, code, errOut)
		}
	}
}
