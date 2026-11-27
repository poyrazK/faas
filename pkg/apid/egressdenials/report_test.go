package egressdenials_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/egressdenials"
	"github.com/onebox-faas/faas/pkg/promql"
)

var now = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

const (
	appA = "11111111-1111-4111-8111-111111111111"
	appB = "22222222-2222-4222-8222-222222222222"
	appC = "33333333-3333-4333-8333-333333333333"
)

func row(v float64, kv ...string) promql.VectorSample {
	l := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		l[kv[i]] = kv[i+1]
	}
	return promql.VectorSample{Labels: l, Value: v}
}

func samples() map[string][]promql.VectorSample {
	return map[string][]promql.VectorSample{
		"denied_by_class":  {row(80.4, "class", "unresolved"), row(15, "class", "rfc1918"), row(5, "class", "smtp"), row(0.2, "class", "flood")},
		"denied_by_node":   {row(60, "node", "bhs-2.faas"), row(40, "node", "bhs-3.faas")},
		"denied_last_hour": {row(7.6)},
		"denied_apps":      {row(80, "app", appA), row(20, "app", appB), row(3, "app", "not-a-uuid")},
		"app_denials": {
			row(78, "app", appA, "class", "unresolved"), row(2, "app", appA, "class", "rfc1918"),
			row(13, "app", appB, "class", "rfc1918"), row(5, "app", appB, "class", "smtp"), row(9, "app", appC, "class", "smtp"),
		},
		"apps_denied":            {row(31)},
		"new_destinations_total": {row(42)},
		"new_destinations_apps":  {row(30, "app", appB), row(12, "app", appC)},
		"abuse_recycles":         {row(2, "app", appC, "reason", "fanout"), row(1, "app", appC, "reason", "flood"), row(1, "app", "")},
		"abuse_holds":            {row(1, "reason", "egress_abuse")},
		"cpu_abuse":              {row(3, "plan", "free", "action", "throttle")},
		"dns_blocked":            {row(9, "category", "malware"), row(4, "category", "mining")},
		"scan_findings":          {row(2, "category", "miner", "action", "block"), row(1, "category", "phishing", "action", "flag")},
		"denylist_firewall":      {row(5, "cidr", "203.0.113.0/24")},
		"denylist_image_pull":    {row(8, "cidr", "198.51.100.0/24")},
		"flow_log_rows":          {},
	}
}

func TestBuild(t *testing.T) {
	out := egressdenials.Build(egressdenials.Input{Now: now, Window: 24 * time.Hour,
		Samples: egressdenials.Samples{ByQuery: samples(), Source: egressdenials.SourceObserved},
		Lookup: func(id string) (egressdenials.AppInfo, bool) {
			if id == appA {
				return egressdenials.AppInfo{Slug: "shop", AccountID: "acct-1"}, true
			}
			return egressdenials.AppInfo{}, false
		}})
	tot := out.Totals
	if tot.Denied != 100 || tot.DeniedLastHour != 8 || tot.AppsDenied != 31 || tot.NewDestinations != 42 ||
		tot.AbuseRecycles != 3 || tot.AbuseHolds != 1 || tot.CPUAbuseSignals != 3 || tot.DNSBlocked != 13 ||
		tot.ScanBlocked != 2 || tot.ScanFlagged != 1 || tot.FlowLogRows == nil || *tot.FlowLogRows != 0 {
		t.Fatalf("totals = %+v", tot)
	}
	if len(out.Classes) != 3 || out.Classes[0].Key != "unresolved" || out.Classes[0].Count != 80 {
		t.Fatalf("classes = %+v", out.Classes)
	}
	if out.WindowSeconds != 86400 || out.FailedQueries == nil || len(out.FailedQueries) != 0 {
		t.Fatalf("header = %+v", out)
	}
	// Recycled app first, then by denials; the bad app label is dropped. appC
	// is outside the top denied but the second-phase read still gives it its
	// denials and classes.
	c := out.Apps[0]
	if len(out.Apps) != 3 || c.AppID != appC || c.AbuseRecycles != 3 || c.NewDestinations != 12 || c.Denied != 9 || len(c.Classes) != 1 || c.Classes[0].Key != "smtp" {
		t.Fatalf("apps = %+v", out.Apps)
	}
	a := out.Apps[1]
	if a.AppID != appA || a.AppSlug != "shop" || a.AccountID != "acct-1" || a.Denied != 80 || len(a.Classes) != 2 || a.Classes[0].Key != "unresolved" {
		t.Fatalf("apps[1] = %+v", a)
	}
	if out.Apps[2].AppID != appB || out.Apps[2].Denied != 18 || out.Apps[2].NewDestinations != 30 || out.Apps[2].Classes[0].Key != "rfc1918" {
		t.Fatalf("apps[2] = %+v", out.Apps[2])
	}
	if len(out.Recycles) != 2 || out.Recycles[0].Reason != "fanout" {
		t.Fatalf("recycles = %+v", out.Recycles)
	}
	if len(out.Denylist) != 2 || out.Denylist[0].Source != egressdenials.DenylistImagePull || out.Denylist[1].Source != egressdenials.DenylistFirewall {
		t.Fatalf("denylist = %+v", out.Denylist)
	}
	if len(out.ScanFindings) != 2 || out.ScanFindings[0].Action != "block" || len(out.CPUSignals) != 1 {
		t.Fatalf("scan/cpu = %+v %+v", out.ScanFindings, out.CPUSignals)
	}
}

// When the second-phase read failed, the topk denials stand and the classes
// are empty rather than invented.
func TestBuildWithoutAppDenials(t *testing.T) {
	s := samples()
	delete(s, "app_denials")
	out := egressdenials.Build(egressdenials.Input{Now: now, Window: time.Hour, Samples: egressdenials.Samples{ByQuery: s}})
	if len(out.Apps) != 3 || out.Apps[0].AppID != appC || out.Apps[0].Denied != 0 || out.Apps[2].Denied != 20 || len(out.Apps[1].Classes) != 0 {
		t.Fatalf("apps = %+v", out.Apps)
	}
}

func TestSortTiesCompareFieldByField(t *testing.T) {
	s := map[string][]promql.VectorSample{"cpu_abuse": {row(1, "plan", "prof", "action", "lagged"), row(1, "plan", "pro", "action", "flagged")}}
	out := egressdenials.Build(egressdenials.Input{Now: now, Window: time.Hour, Samples: egressdenials.Samples{ByQuery: s}})
	if out.CPUSignals[0].Plan != "pro" {
		t.Fatalf("cpu = %+v", out.CPUSignals)
	}
}

func TestBuildEmptyHasEverySection(t *testing.T) {
	out := egressdenials.Build(egressdenials.Input{Now: now, Window: time.Hour, Samples: egressdenials.Samples{Source: egressdenials.SourceNotConfigured}})
	if out.Classes == nil || out.Nodes == nil || out.Apps == nil || out.Recycles == nil || out.Holds == nil || out.CPUSignals == nil ||
		out.DNSBlocked == nil || out.ScanFindings == nil || out.Denylist == nil || out.FailedQueries == nil || out.Totals.FlowLogRows != nil {
		t.Fatalf("empty report = %+v", out)
	}
}

func TestBuildCapsApps(t *testing.T) {
	s := map[string][]promql.VectorSample{}
	for i := 0; i < api.ObsEgressAppsMax+10; i++ {
		s["denied_apps"] = append(s["denied_apps"], row(float64(i+1), "app", fmt.Sprintf("%08d-1111-4111-8111-111111111111", i)))
	}
	out := egressdenials.Build(egressdenials.Input{Now: now, Window: time.Hour, Samples: egressdenials.Samples{ByQuery: s}})
	if len(out.Apps) != api.ObsEgressAppsMax || out.Apps[0].Denied != int64(api.ObsEgressAppsMax+10) {
		t.Fatalf("apps = %d first=%+v", len(out.Apps), out.Apps[0])
	}
}

type fakeQuerier struct {
	mu   sync.Mutex
	seen []string
	fail string
	rows map[string][]promql.VectorSample // by query substring
}

func (f *fakeQuerier) QueryVectorAt(_ context.Context, q string, at time.Time) ([]promql.VectorSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !at.Equal(now) {
		return nil, errors.New("wrong instant")
	}
	f.seen = append(f.seen, q)
	if f.fail == "*" || (f.fail != "" && strings.Contains(q, f.fail)) {
		return nil, errors.New("boom")
	}
	for part, rows := range f.rows {
		if strings.Contains(q, part) {
			return rows, nil
		}
	}
	return []promql.VectorSample{row(1)}, nil
}

// The listed apps' denials are read in a second phase, for exactly the
// listed IDs; its failure is named and makes the report partial.
func TestReadSecondPhase(t *testing.T) {
	rows := map[string][]promql.VectorSample{
		"topk(25, sum by (app) (increase(vmmd_egress_denied_total": {row(5, "app", appA)},
		"schedd_egress_abuse_recycles_total":                       {row(1, "app", appC, "reason", "flood")},
	}
	f := &fakeQuerier{rows: rows}
	got := egressdenials.ReadWith(context.Background(), f, time.Hour, now)
	want := egressdenials.AppDenialsQuery(time.Hour, []string{appC, appA})
	last := f.seen[len(f.seen)-1]
	if got.Source != egressdenials.SourceObserved || len(f.seen) != len(egressdenials.Queries(time.Hour))+1 || last != want {
		t.Fatalf("source=%s seen=%d last=%q want %q", got.Source, len(f.seen), last, want)
	}
	f = &fakeQuerier{fail: appC + "|" + appA, rows: rows}
	got = egressdenials.ReadWith(context.Background(), f, time.Hour, now)
	if got.Source != egressdenials.SourcePartial || len(got.Failed) != 1 || got.Failed[0] != "app_denials" {
		t.Fatalf("source=%s failed=%v", got.Source, got.Failed)
	}
	if egressdenials.AppDenialsQuery(time.Hour, nil) != "" {
		t.Fatal("empty list must not query")
	}
}

func TestReadSources(t *testing.T) {
	for _, tc := range []struct{ fail, want string }{
		{"", egressdenials.SourceObserved},
		{"gatewayd_dns_blocked_total", egressdenials.SourcePartial},
		{"*", egressdenials.SourceUnavailable},
	} {
		f := &fakeQuerier{fail: tc.fail}
		got := egressdenials.ReadWith(context.Background(), f, 6*time.Hour, now)
		if got.Source != tc.want || len(f.seen) != len(egressdenials.Queries(6*time.Hour)) {
			t.Fatalf("fail=%q source=%s seen=%d failed=%v", tc.fail, got.Source, len(f.seen), got.Failed)
		}
		if tc.fail == "gatewayd_dns_blocked_total" && (len(got.Failed) != 1 || got.Failed[0] != "dns_blocked") {
			t.Fatalf("failed = %v", got.Failed)
		}
	}
	if got := egressdenials.Read(context.Background(), nil, time.Hour, now); got.Source != egressdenials.SourceNotConfigured {
		t.Fatalf("nil client source = %s", got.Source)
	}
}

func TestQueriesUseWindowAndBudget(t *testing.T) {
	q := egressdenials.Queries(6 * time.Hour)
	if !strings.Contains(q["denied_by_class"], "[21600s]") || !strings.Contains(q["denied_last_hour"], "[3600s]") ||
		!strings.Contains(egressdenials.Queries(5 * time.Minute)["denied_last_hour"], "[300s]") || !strings.Contains(q["apps_denied"], `app=~"[0-9a-fA-F]{8}-`) ||
		!strings.Contains(q["new_destinations_apps"], `app!=""`) {
		t.Fatalf("queries = %v", q)
	}
	if egressdenials.Budget(24*time.Hour) != api.ObsEgressPrometheusBudget || egressdenials.Budget(48*time.Hour) != api.ObsEgressLongWindowBudget {
		t.Fatal("budget")
	}
}
