package gatewaytraffic_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/gatewaytraffic"
	"github.com/onebox-faas/faas/pkg/promql"
)

var now = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func row(v float64, kv ...string) promql.VectorSample {
	l := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		l[kv[i]] = kv[i+1]
	}
	return promql.VectorSample{Labels: l, Value: v}
}

func samples() map[string][]promql.VectorSample {
	return map[string][]promql.VectorSample{
		"requests_by_node_code": {
			row(900.4, "node", "fsn-2", "code", "200"), row(40, "node", "fsn-2", "code", "503"), row(10, "node", "fsn-2", "code", "429"),
			row(48, "node", "fsn-3", "code", "200"), row(2, "node", "fsn-3", "code", "502"),
		},
		"platform_errors_by_node":   {row(30, "node", "fsn-2"), row(2, "node", "fsn-3")},
		"capacity_refusals_by_node": {row(10, "node", "fsn-2")},
		"app_5xx_by_code": {
			row(30, "app", "app-a", "plan", "hobby", "code", "503"), row(10, "app", "app-b", "plan", "pro", "code", "503"),
			row(2, "app", "app-b", "plan", "pro", "code", "502"), row(1, "app", "-", "plan", "-", "code", "503"),
		},
		"app_platform_errors":   {row(30, "app", "app-a", "plan", "hobby"), row(2, "app", "app-b", "plan", "pro")},
		"app_capacity_refusals": {row(10, "app", "app-c")},
		"app_requests":          {row(100, "app", "app-a"), row(400, "app", "app-b"), row(50, "app", "app-c")},
		"wake_admission": {
			row(80, "plan", "hobby", "outcome", "admitted"), row(5, "plan", "hobby", "outcome", "app_queue_full"),
			row(2, "plan", "free", "outcome", "queue_timeout"), row(0, "plan", "pro", "outcome", "gateway_queue_full"),
		},
		"edge_rejections":   {row(3, "kind", "waf", "status", "403"), row(7, "kind", "limit", "status", "429")},
		"edge_refused":      {row(4, "reason", "request_rate")},
		"retry_exhausted":   {row(6, "reason", "non_idempotent_method")},
		"waiters_by_node":   {row(12, "node", "fsn-2"), row(0, "node", "fsn-3")},
		"waiters_by_app":    {row(12, "app", "app-d", "plan", "free")},
		"admission_leaders": {row(3)},
		"overload_by_node":  {row(0.456, "node", "fsn-2")},
	}
}

func TestBuildTotalsNodesAndApps(t *testing.T) {
	out := gatewaytraffic.Build(gatewaytraffic.Input{Now: now, Window: time.Hour,
		Samples: gatewaytraffic.Samples{ByQuery: samples(), Source: gatewaytraffic.SourceObserved},
		Lookup: func(id string) (gatewaytraffic.AppInfo, bool) {
			if id == "app-a" {
				return gatewaytraffic.AppInfo{Slug: "shop", AccountID: "acct-1"}, true
			}
			return gatewaytraffic.AppInfo{}, false
		}})
	tot := out.Totals
	if tot.Requests != 1000 || tot.Status2xx != 948 || tot.Status5xx != 42 || tot.Status429 != 10 || tot.Status503 != 40 || tot.Status502 != 2 {
		t.Fatalf("status totals = %+v", tot)
	}
	if tot.ErrorRatePercent != 4.2 || tot.PlatformErrors != 32 || tot.PlatformErrorRatePercent != 3.2 || tot.CapacityRefusals != 10 {
		t.Fatalf("error totals = %+v", tot)
	}
	if tot.WakesAdmitted != 80 || tot.WakeAppQueueFull != 5 || tot.WakeQueueTimeouts != 2 || tot.WakeGatewayQueueFull != 0 ||
		tot.HeldNow != 12 || tot.AdmissionLeadersNow != 3 || tot.Nodes != 2 {
		t.Fatalf("wake totals = %+v", tot)
	}
	n := out.Nodes[0]
	if n.Node != "fsn-2" || n.Requests != 950 || n.Status5xx != 40 || n.PlatformErrors != 30 || n.HeldNow != 12 ||
		n.OverloadLevel == nil || *n.OverloadLevel != 0.46 || n.ErrorRatePercent != 4.21 {
		t.Fatalf("node = %+v", n)
	}
	if out.Nodes[1].OverloadLevel != nil {
		t.Fatalf("fsn-3 overload = %v", *out.Nodes[1].OverloadLevel)
	}
	if len(out.WakeAdmission) != 3 || len(out.Statuses) != 4 || out.EdgeRejections[0].Kind != "limit" ||
		out.EdgeRefusals[0].Reason != "request_rate" || out.RetryExhausted[0].Count != 6 {
		t.Fatalf("breakdowns = %+v %+v %+v", out.WakeAdmission, out.Statuses, out.EdgeRejections)
	}
	ids := []string{}
	for _, a := range out.Apps {
		ids = append(ids, a.AppID)
	}
	if strings.Join(ids, ",") != "app-a,app-b,-,app-c,app-d" {
		t.Fatalf("app order = %v", ids)
	}
	a, b, unknown, c, d := out.Apps[0], out.Apps[1], out.Apps[2], out.Apps[3], out.Apps[4]
	if a.AppSlug != "shop" || a.AccountID != "acct-1" || a.Plan != "hobby" || a.Requests != 100 || a.ErrorRatePercent != 30 || a.Status503 != 30 {
		t.Fatalf("app a = %+v", a)
	}
	if b.Status5xx != 12 || b.Status502 != 2 || b.ErrorRatePercent != 3 || b.AppSlug != "" {
		t.Fatalf("app b = %+v", b)
	}
	if c.CapacityRefusals != 10 || c.Requests != 50 || d.HeldNow != 12 || d.Plan != "free" || !unknown.UnknownHost || unknown.Plan != "" {
		t.Fatalf("apps c/d/unknown = %+v %+v %+v", c, d, unknown)
	}
}

func TestBuildEmptyShape(t *testing.T) {
	out := gatewaytraffic.Build(gatewaytraffic.Input{Now: now, Window: time.Hour, Samples: gatewaytraffic.Samples{Source: gatewaytraffic.SourceNotConfigured}})
	if out.Source != "not_configured" || out.Nodes == nil || out.Apps == nil || out.Statuses == nil || out.FailedQueries == nil ||
		out.WakeAdmission == nil || out.EdgeRejections == nil || out.EdgeRefusals == nil || out.RetryExhausted == nil || out.Totals.ErrorRatePercent != 0 {
		t.Fatalf("empty = %+v", out)
	}
}

func TestAppsCap(t *testing.T) {
	s := map[string][]promql.VectorSample{}
	for i := range api.ObsGatewayTrafficAppsMax + 5 {
		s["app_platform_errors"] = append(s["app_platform_errors"], row(float64(i+1), "app", string(rune('a'+i))))
	}
	out := gatewaytraffic.Build(gatewaytraffic.Input{Now: now, Window: time.Hour, Samples: gatewaytraffic.Samples{ByQuery: s}})
	if len(out.Apps) != api.ObsGatewayTrafficAppsMax || out.Apps[0].PlatformErrors != int64(api.ObsGatewayTrafficAppsMax+5) {
		t.Fatalf("apps = %d first=%+v", len(out.Apps), out.Apps[0])
	}
}

const (
	appA = "11111111-1111-4111-8111-111111111111"
	appB = "22222222-2222-4222-8222-222222222222"
)

// fakeProm answers every query at one pinned time. Per-app queries get two
// apps with platform errors; the second-phase requests query is recorded.
type fakeProm struct {
	fail     []string
	mu       sync.Mutex
	times    map[time.Time]bool
	requests string
}

func (f *fakeProm) QueryVectorAt(_ context.Context, q string, at time.Time) ([]promql.VectorSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.times == nil {
		f.times = map[time.Time]bool{}
	}
	f.times[at] = true
	for _, frag := range f.fail {
		if strings.Contains(q, frag) {
			return nil, errors.New("boom")
		}
	}
	switch {
	case strings.Contains(q, "app=~"):
		f.requests = q
		return []promql.VectorSample{row(100, "app", appA), row(7, "app", appB)}, nil
	case strings.Contains(q, "gateway_platform_errors_total") && strings.Contains(q, "by (app, plan)"):
		return []promql.VectorSample{row(5, "app", appA, "plan", "pro"), row(1, "app", appB, "plan", "free"), row(1, "app", "bad\"label")}, nil
	}
	return []promql.VectorSample{row(1, "node", "fsn-1")}, nil
}

func TestReadSources(t *testing.T) {
	ctx := context.Background()
	f := &fakeProm{}
	all := gatewaytraffic.ReadWith(ctx, f, time.Hour, now)
	if all.Source != gatewaytraffic.SourceObserved || len(all.Failed) != 0 || len(all.ByQuery) != len(gatewaytraffic.Queries(time.Hour))+1 {
		t.Fatalf("observed = %+v", all)
	}
	if len(f.times) != 1 || !f.times[now] {
		t.Fatalf("queries not pinned to one time: %v", f.times)
	}
	// Requests are read for exactly the listed apps, in report order; a
	// label that is not an app ID never reaches the regex.
	if !strings.Contains(f.requests, `app=~"`+appA+"|"+appB+`"`) || strings.Contains(f.requests, "bad") {
		t.Fatalf("requests query = %s", f.requests)
	}
	out := gatewaytraffic.Build(gatewaytraffic.Input{Now: now, Window: time.Hour, Samples: all})
	if out.Apps[0].AppID != appA || out.Apps[0].Requests != 100 || out.Apps[1].Requests != 7 {
		t.Fatalf("apps = %+v", out.Apps)
	}
	part := gatewaytraffic.ReadWith(ctx, &fakeProm{fail: []string{"gatewayd_public_overload_level"}}, time.Hour, now)
	if part.Source != gatewaytraffic.SourcePartial || len(part.Failed) != 1 || part.Failed[0] != "overload_by_node" {
		t.Fatalf("partial = %+v", part)
	}
	noReq := gatewaytraffic.ReadWith(ctx, &fakeProm{fail: []string{"app=~"}}, time.Hour, now)
	if noReq.Source != gatewaytraffic.SourcePartial || len(noReq.Failed) != 1 || noReq.Failed[0] != "app_requests" {
		t.Fatalf("requests failed = %+v", noReq)
	}
	none := gatewaytraffic.ReadWith(ctx, &fakeProm{fail: []string{"gateway"}}, time.Hour, now)
	if none.Source != gatewaytraffic.SourceUnavailable {
		t.Fatalf("unavailable = %+v", none)
	}
	if q := gatewaytraffic.Queries(90 * time.Minute)["requests_by_node_code"]; !strings.Contains(q, "[5400s]") {
		t.Fatalf("window not in query: %s", q)
	}
	if q := gatewaytraffic.Queries(time.Hour)["app_5xx_by_code"]; !strings.Contains(q, "topk(25,") {
		t.Fatalf("app 5xx query unbounded: %s", q)
	}
	if gatewaytraffic.Budget(24*time.Hour) != api.ObsGatewayTrafficPrometheusBudget || gatewaytraffic.Budget(48*time.Hour) != api.ObsGatewayTrafficLongWindowBudget {
		t.Fatal("budget by window")
	}
	if gatewaytraffic.AppRequestsQuery(time.Hour, []string{"x|y", ""}) != "" {
		t.Fatal("invalid labels must not build a query")
	}
	if gatewaytraffic.Read(ctx, nil, time.Hour, now).Source != gatewaytraffic.SourceNotConfigured {
		t.Fatal("nil client should be not_configured")
	}
}

func TestEdgeRejectionsDropRoundedZeros(t *testing.T) {
	out := gatewaytraffic.Build(gatewaytraffic.Input{Now: now, Window: time.Hour, Samples: gatewaytraffic.Samples{ByQuery: map[string][]promql.VectorSample{
		"edge_rejections": {row(0.4, "kind", "waf", "status", "403"), row(2, "kind", "limit", "status", "429")},
	}}})
	if len(out.EdgeRejections) != 1 || out.EdgeRejections[0].Kind != "limit" {
		t.Fatalf("edge rejections = %+v", out.EdgeRejections)
	}
}
