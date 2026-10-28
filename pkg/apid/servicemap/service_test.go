// adr: 1131
package servicemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store IDs are 32-hex; the gateway labels edges with dashed UUIDs.
const (
	publicAPIID = "11111111111111111111111111111111"
	paymentsID  = "22222222222222222222222222222222"
	ledgerID    = "33333333333333333333333333333333"
	quietID     = "44444444444444444444444444444444"
	foreignID   = "99999999999999999999999999999999"
)

type fakeHost struct {
	apps      []state.App
	appsErr   error
	prom      *promql.Client
	wakeAhead map[string]bool
	wakeErr   map[string]bool
}

func (h *fakeHost) WakeAheadEnabled(_ context.Context, appID string) (bool, error) {
	if h.wakeErr[appID] {
		return false, errors.New("store down")
	}
	return h.wakeAhead[appID], nil
}

func (h *fakeHost) Apps(context.Context, string) ([]state.App, error) { return h.apps, h.appsErr }
func (h *fakeHost) PromQL() *promql.Client                            { return h.prom }
func (h *fakeHost) Logger() *slog.Logger                              { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func accountApps() []state.App {
	return []state.App{
		{ID: publicAPIID, Slug: "public-api"},
		{ID: paymentsID, Slug: "payments"},
		{ID: ledgerID, Slug: "ledger"},
		{ID: quietID, Slug: "quiet"},
	}
}

// promFixture answers instant queries with answer(query) and counts them.
func promFixture(t *testing.T, answer func(query string) string) (*promql.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		body := answer(r.Form.Get("query"))
		if strings.HasPrefix(body, "HTTP500:") {
			w.WriteHeader(http.StatusInternalServerError)
			body = strings.TrimPrefix(body, "HTTP500:")
		}
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return promql.NewClient(srv.URL, nil), &calls
}

func serve(t *testing.T, s *Service, plan api.Plan, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/v1/service-map"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	rec := httptest.NewRecorder()
	s.Get(rec, httptest.NewRequest(http.MethodGet, target, nil), state.Account{ID: "acct", Plan: plan})
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) api.ServiceMapResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.ServiceMapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return p.Code
}

func TestGetServiceMapGates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		plan     api.Plan
		query    string
		wantCode int
		wantProb string
	}{
		{name: "disabled", enabled: false, plan: api.PlanHobby, wantCode: http.StatusServiceUnavailable, wantProb: "service_map_unavailable"},
		{name: "free plan", enabled: true, plan: api.PlanFree, wantCode: http.StatusPaymentRequired, wantProb: api.CodePlanPerAppMetricsNotAllowed},
		{name: "invalid range", enabled: true, plan: api.PlanHobby, query: "range=99y", wantCode: http.StatusBadRequest, wantProb: api.CodeValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{Enabled: tc.enabled, Host: &fakeHost{apps: accountApps()}}
			rec := serve(t, s, tc.plan, tc.query)
			if rec.Code != tc.wantCode || problemCode(t, rec) != tc.wantProb {
				t.Fatalf("got %d %q, want %d %q: %s", rec.Code, problemCode(t, rec), tc.wantCode, tc.wantProb, rec.Body)
			}
		})
	}
}

func TestGetServiceMapDegraded(t *testing.T) {
	t.Run("no prometheus", func(t *testing.T) {
		s := &Service{Enabled: true, Host: &fakeHost{apps: accountApps()}}
		out := decode(t, serve(t, s, api.PlanHobby, ""))
		if out.Range != api.ServiceMapDefaultRange {
			t.Fatalf("range = %q, want default %q", out.Range, api.ServiceMapDefaultRange)
		}
		if want := appmetrics.SourceDegradedPrefix + "prometheus not configured"; out.Source != want {
			t.Fatalf("source = %q, want %q", out.Source, want)
		}
		if out.Nodes != nil || out.Edges != nil {
			t.Fatalf("degraded map must not carry rows: %+v", out)
		}
	})
	t.Run("query fails", func(t *testing.T) {
		prom, _ := promFixture(t, func(string) string {
			return `HTTP500:{"status":"error","errorType":"bad_data","error":"parse error"}`
		})
		s := &Service{Enabled: true, Host: &fakeHost{apps: accountApps(), prom: prom}}
		out := decode(t, serve(t, s, api.PlanHobby, "range=5m"))
		if !strings.HasPrefix(out.Source, appmetrics.SourceDegradedPrefix) || strings.Contains(out.Source, "parse error") {
			t.Fatalf("source = %q, want degraded without the Prometheus error text", out.Source)
		}
		if out.Nodes != nil || out.Edges != nil {
			t.Fatalf("degraded map must not carry rows: %+v", out)
		}
	})
	t.Run("app listing fails", func(t *testing.T) {
		s := &Service{Enabled: true, Host: &fakeHost{appsErr: errors.New("db down")}}
		if rec := serve(t, s, api.PlanHobby, ""); rec.Code == http.StatusOK {
			t.Fatalf("status = %d, want a capacity problem when apps cannot be listed", rec.Code)
		}
	})
}

// TestGetServiceMap_HappyPath_WithProm is the capability acceptance test
// (pkg/productcap/catalog.json, ADR-1131). It pins the closed-set selectors,
// the second ownership boundary on returned labels, ranking, error rate, and
// success-only latency percentiles.
func TestGetServiceMap_HappyPath_WithProm(t *testing.T) {
	gw := CanonicalAppID
	prom, queries := promFixture(t, func(query string) string {
		for _, id := range []string{publicAPIID, paymentsID, ledgerID} {
			if strings.Count(query, gw(id)) != 2 {
				t.Errorf("query must constrain caller and target to owned app %s: %s", gw(id), query)
			}
		}
		if strings.Contains(query, gw(foreignID)) {
			t.Errorf("query includes a foreign app ID: %s", query)
		}
		switch {
		case strings.Contains(query, "gateway_service_dependency_edge_calls_total"):
			return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"success"},"value":[1,"96"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"error"},"value":[1,"4"]},`+
				`{"metric":{"caller_app":"%[2]s","target_app":"%[3]s","outcome":"success"},"value":[1,"10"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[3]s","outcome":"success"},"value":[1,"0.2"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[4]s","outcome":"success"},"value":[1,"50"]}]}}`,
				gw(publicAPIID), gw(paymentsID), gw(ledgerID), gw(foreignID))
		case strings.Contains(query, "gateway_service_dependency_duration_seconds_bucket"):
			if !strings.Contains(query, `outcome="success"`) {
				t.Errorf("latency query must be success-only: %s", query)
			}
			return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","le":"0.01"},"value":[1,"48"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","le":"0.05"},"value":[1,"96"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","le":"+Inf"},"value":[1,"96"]}]}}`,
				gw(publicAPIID), gw(paymentsID))
		case strings.Contains(query, "gateway_service_wake_ahead_edge_total"):
			return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"started"},"value":[1,"10"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"used"},"value":[1,"8"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"unused"},"value":[1,"2"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[3]s","outcome":"started"},"value":[1,"9"]}]}}`,
				gw(publicAPIID), gw(paymentsID), gw(foreignID))
		case strings.Contains(query, "gateway_service_dependency_edge_wakes_total"):
			return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s"},"value":[1,"60"]},`+
				`{"metric":{"caller_app":"%[2]s","target_app":"%[3]s"},"value":[1,"2"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[4]s"},"value":[1,"50"]}]}}`,
				gw(publicAPIID), gw(paymentsID), gw(ledgerID), gw(foreignID))
		case strings.Contains(query, "gateway_service_tcp_edge_sessions_total"):
			return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"success"},"value":[1,"28"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"error"},"value":[1,"2"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[3]s","outcome":"success"},"value":[1,"7"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[4]s","outcome":"success"},"value":[1,"500"]}]}}`,
				gw(publicAPIID), gw(paymentsID), gw(ledgerID), gw(foreignID))
		case strings.Contains(query, "gateway_service_tcp_edge_wakes_total"):
			return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s"},"value":[1,"5"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[4]s"},"value":[1,"10"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[3]s"},"value":[1,"9"]}]}}`,
				gw(publicAPIID), gw(ledgerID), gw(foreignID), gw(paymentsID))
		default:
			t.Errorf("unexpected query: %s", query)
			return `{"status":"success","data":{"resultType":"vector","result":[]}}`
		}
	})
	s := &Service{Enabled: true, Host: &fakeHost{apps: accountApps(), prom: prom}}

	out := decode(t, serve(t, s, api.PlanHobby, ""))
	if out.Source != appmetrics.SourcePrometheus || out.Range != "1h" || out.Truncated {
		t.Fatalf("envelope = source %q range %q truncated %v", out.Source, out.Range, out.Truncated)
	}
	if got := queries.Load(); got != 6 {
		t.Fatalf("Prometheus queries = %d, want 6", got)
	}
	if len(out.Edges) != 3 {
		t.Fatalf("edges = %+v, want public-api→payments, payments→ledger and the TCP-only public-api→ledger", out.Edges)
	}
	top := out.Edges[0]
	if top.CallerAppSlug != "public-api" || top.TargetAppSlug != "payments" || top.Calls != 100 || top.Errors != 4 {
		t.Fatalf("top edge = %+v", top)
	}
	if math.Abs(top.ErrorRatePct-4) > 1e-9 {
		t.Fatalf("error_rate_pct = %v, want 4", top.ErrorRatePct)
	}
	if math.Abs(top.LatencyP50MS-10) > 1e-9 || top.LatencyP95MS <= top.LatencyP50MS {
		t.Fatalf("latency p50/p95 = %v/%v, want 10 and above", top.LatencyP50MS, top.LatencyP95MS)
	}
	if second := out.Edges[1]; second.CallerAppSlug != "payments" || second.TargetAppSlug != "ledger" || second.Calls != 10 {
		t.Fatalf("second edge = %+v", second)
	}
	// 60 of 100 calls and 10 of 30 TCP sessions woke payments (70 of 130),
	// and public-api has not opted in: suggest.
	if top.ColdCalls != 60 || math.Abs(top.ColdCallPct-60) > 1e-9 || !top.SuggestWakeAhead {
		t.Fatalf("top edge cold = %d (%v%%) suggest=%v, want 60, 60%%, true", top.ColdCalls, top.ColdCallPct, top.SuggestWakeAhead)
	}
	if top.WakeAheadStarted != 10 || top.WakeAheadUsed != 8 {
		t.Fatalf("top edge wake-ahead = %d started / %d used, want 10 / 8", top.WakeAheadStarted, top.WakeAheadUsed)
	}
	if top.TCPSessions != 30 || top.TCPErrors != 2 || top.TCPColdSessions != 10 {
		t.Fatalf("top edge tcp = %d sessions / %d errors / %d cold, want 30 / 2 / 10", top.TCPSessions, top.TCPErrors, top.TCPColdSessions)
	}
	// A pair with only private TCP traffic is still an edge, ranked by its
	// sessions; its sub-unit HTTP sample rounds away.
	if tcpOnly := out.Edges[2]; tcpOnly.CallerAppSlug != "public-api" || tcpOnly.TargetAppSlug != "ledger" ||
		tcpOnly.Calls != 0 || tcpOnly.TCPSessions != 7 || tcpOnly.TCPColdSessions != 5 || tcpOnly.LatencyP50MS != 0 || tcpOnly.SuggestWakeAhead {
		t.Fatalf("tcp-only edge = %+v", tcpOnly)
	}
	// 2 cold calls are below the evidence bar even at 20%.
	if second := out.Edges[1]; second.ColdCalls != 2 || second.SuggestWakeAhead {
		t.Fatalf("second edge cold = %d suggest=%v, want 2 and no suggestion", second.ColdCalls, second.SuggestWakeAhead)
	}
	slugs := make([]string, 0, len(out.Nodes))
	for _, node := range out.Nodes {
		slugs = append(slugs, node.AppSlug)
	}
	if got := strings.Join(slugs, ","); got != "ledger,payments,public-api" {
		t.Fatalf("nodes = %s, want ledger,payments,public-api", got)
	}
}

func TestBuildTruncatesLowestVolumeEdges(t *testing.T) {
	owned := map[string]state.App{
		"a": {ID: "a", Slug: "a"},
		"b": {ID: "b", Slug: "b"},
		"c": {ID: "c", Slug: "c"},
	}
	in := samples{calls: map[edgeKey]float64{
		{caller: "a", target: "b"}: 5,
		{caller: "b", target: "c"}: 50,
		{caller: "a", target: "c"}: 20,
	}}
	nodes, edges, truncated := build(in, owned, 2)
	if !truncated || len(edges) != 2 {
		t.Fatalf("truncated=%v edges=%+v, want 2 edges and truncated", truncated, edges)
	}
	if edges[0].Calls != 50 || edges[1].Calls != 20 {
		t.Fatalf("edges not ranked by volume: %+v", edges)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %+v, want the three apps on surviving edges", nodes)
	}
}

// HTTP calls and TCP sessions rank together, and each part is clamped to the
// counter it belongs to (the counters are scraped independently).
func TestBuildRanksHTTPAndTCPTogether(t *testing.T) {
	owned := map[string]state.App{
		"a": {ID: "a", Slug: "a"},
		"b": {ID: "b", Slug: "b"},
		"c": {ID: "c", Slug: "c"},
	}
	in := samples{
		calls:       map[edgeKey]float64{{caller: "a", target: "b"}: 20},
		wakes:       map[edgeKey]float64{{caller: "a", target: "b"}: 25},
		tcpSessions: map[edgeKey]float64{{caller: "b", target: "c"}: 30, {caller: "a", target: "b"}: 5},
		tcpErrors:   map[edgeKey]float64{{caller: "b", target: "c"}: 31},
		tcpWakes:    map[edgeKey]float64{{caller: "b", target: "c"}: 40},
	}
	_, edges, truncated := build(in, owned, 10)
	if truncated || len(edges) != 2 {
		t.Fatalf("truncated=%v edges=%+v, want 2 edges", truncated, edges)
	}
	if edges[0].CallerAppSlug != "b" || edges[0].Calls != 0 || edges[0].TCPSessions != 30 {
		t.Fatalf("top edge = %+v, want the 30-session TCP edge b→c", edges[0])
	}
	if edges[0].TCPErrors != 30 || edges[0].TCPColdSessions != 30 {
		t.Fatalf("tcp parts not clamped to sessions: %+v", edges[0])
	}
	if edges[1].Calls != 20 || edges[1].ColdCalls != 20 || edges[1].TCPSessions != 5 {
		t.Fatalf("second edge = %+v, want 20 calls (20 cold) and 5 sessions", edges[1])
	}
}

func renderDashboard(t *testing.T, s *Service, plan api.Plan, query string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/service-map?"+query, nil)
	data, err := s.DashboardData(r, state.Account{ID: "acct", Plan: plan})
	if err != nil {
		t.Fatalf("DashboardData: %v", err)
	}
	rec := httptest.NewRecorder()
	page := dashboard.Page{Title: "Service map", Body: "service_map", Data: data}
	if err := dashboard.Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	return rec.Body.String()
}

func TestDashboardStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plan    api.Plan
		enabled bool
		want    string
	}{
		{name: "disabled", plan: api.PlanHobby, enabled: false, want: "not enabled for this deployment"},
		{name: "free plan", plan: api.PlanFree, enabled: true, want: "start on the Hobby plan"},
		{name: "no prometheus", plan: api.PlanHobby, enabled: true, want: "Metrics are unavailable right now (prometheus not configured)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{Enabled: tc.enabled, Host: &fakeHost{apps: accountApps()}}
			if body := renderDashboard(t, s, tc.plan, ""); !strings.Contains(body, tc.want) {
				t.Fatalf("page missing %q:\n%s", tc.want, body)
			}
		})
	}
}

func TestDashboardRendersEdgesAndKeepsValidRange(t *testing.T) {
	var gotRange string
	prom, _ := promFixture(t, func(query string) string {
		if strings.Contains(query, "gateway_service_dependency_edge_wakes_total") {
			return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s"},"value":[1,"60"]}]}}`,
				CanonicalAppID(publicAPIID), CanonicalAppID(paymentsID))
		}
		if strings.Contains(query, "gateway_service_tcp_edge_sessions_total") {
			return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"success"},"value":[1,"7"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"error"},"value":[1,"1"]}]}}`,
				CanonicalAppID(paymentsID), CanonicalAppID(ledgerID))
		}
		if !strings.Contains(query, "gateway_service_dependency_edge_calls_total") {
			return `{"status":"success","data":{"resultType":"vector","result":[]}}`
		}
		gotRange = query[strings.LastIndex(query, "[")+1 : strings.LastIndex(query, "]")]
		return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[`+
			`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"success"},"value":[1,"90"]},`+
			`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"error"},"value":[1,"10"]}]}}`,
			CanonicalAppID(publicAPIID), CanonicalAppID(paymentsID))
	})
	s := &Service{Enabled: true, Host: &fakeHost{apps: accountApps(), prom: prom}}
	body := renderDashboard(t, s, api.PlanHobby, url.Values{"range": {"24h"}}.Encode())
	if gotRange != "24h" {
		t.Fatalf("query range = %q, want 24h", gotRange)
	}
	for _, want := range []string{
		"public-api", "payments", `class="num bad"`, "10.00%", "60 (60%)", "<code>gregale app wake-ahead public-api on</code>",
		// The TCP-only payments → ledger edge: sessions, no HTTP latency.
		`8 <span class="bad">(1 failed, 0 woke)</span>`, `<td class="num"><span class="muted">—</span></td>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q:\n%s", want, body)
		}
	}
	// An invalid range falls back to the default instead of failing.
	if body := renderDashboard(t, s, api.PlanHobby, "range=99y"); !strings.Contains(body, "<strong>1h</strong>") {
		t.Fatalf("invalid range did not fall back to the default:\n%s", body)
	}
}
