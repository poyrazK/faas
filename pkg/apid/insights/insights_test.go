package insights_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/state"
)

type fixture struct {
	store *state.MemStore
	scope state.FeatureFlagScope
	now   time.Time
	ctx   context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{store: s, ctx: ctx, now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC),
		scope: state.FeatureFlagScope{AccountID: acct.ID, ProjectID: "project", EnvironmentID: "11111111-1111-1111-1111-111111111111"}}
}

func (f fixture) customer(t *testing.T, name string) string {
	t.Helper()
	tn, _, err := f.store.CreatePlatformTenant(f.ctx, f.scope.AccountID, name, name, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return tn.ID
}

func (f fixture) track(t *testing.T, events ...state.FlagConversionEvent) {
	t.Helper()
	res, err := f.store.IngestFlagEvents(f.ctx, state.FlagEventIngest{Scope: f.scope, Quota: 1_000_000, MaxNames: 100, Now: f.now,
		Events: events, Analytics: true, MaxPropertyKeys: api.AnalyticsPropertyKeysMaxPerEnvironment})
	if err != nil || res.Accepted != len(events) {
		t.Fatalf("ingest = %+v, %v", res, err)
	}
}

func (f fixture) ev(customer, name string, at time.Duration, flags map[string]string) state.FlagConversionEvent {
	return state.FlagConversionEvent{CustomerID: customer, Name: name, At: f.now.Add(-at), Flags: flags}
}

func (f fixture) week() insights.Range {
	return insights.Range{From: f.now.Add(-7 * 24 * time.Hour), Until: f.now}
}

// ADR 20261117084505 §3: a unit completes a step only after its previous one
// and within the window of its first; a flag breakdown takes the unit's
// first step event, then its earliest event carrying the flag.
func TestFunnelOrderWindowAndFlagBreakdown(t *testing.T) {
	f := newFixture(t)
	a, b, c, d := f.customer(t, "a"), f.customer(t, "b"), f.customer(t, "c"), f.customer(t, "d")
	h := time.Hour
	f.track(t,
		// a: signup -> checkout -> paid, variant new on the signup event.
		f.ev(a, "signup", 50*h, map[string]string{"checkout": "new"}), f.ev(a, "checkout", 49*h, nil), f.ev(a, "paid", 48*h, nil),
		// b: checkout before signup does not count; paid after it does not either.
		f.ev(b, "checkout", 60*h, nil), f.ev(b, "signup", 40*h, nil), f.ev(b, "paid", 39*h, nil),
		// b's variant comes from a page view in the range.
		f.ev(b, "page_view", 70*h, map[string]string{"checkout": "legacy"}),
		// c: completes checkout after the 24h window.
		f.ev(c, "signup", 100*h, map[string]string{"checkout": "new"}), f.ev(c, "checkout", 70*h, nil),
		// d: signup only, no flag anywhere.
		f.ev(d, "signup", 10*h, nil),
	)
	got, err := insights.Funnel(f.ctx, f.store, insights.FunnelQuery{EnvironmentID: f.scope.EnvironmentID, Range: f.week(),
		Request: api.InsightFunnelRequest{Steps: []api.InsightStepRequest{{Event: "signup"}, {Event: "checkout"}, {Event: "paid"}}, Window: "24h", By: "flag:checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[0].Units != 4 || got.Steps[1].Units != 1 || got.Steps[2].Units != 1 {
		t.Fatalf("steps = %+v; want 4, 1, 1", got.Steps)
	}
	if got.Steps[1].ConversionFromFirst != 0.25 || got.Steps[2].ConversionFromPrev != 1 {
		t.Fatalf("conversion = %+v", got.Steps)
	}
	want := map[string][3]int64{"new": {2, 1, 1}, "legacy": {1, 0, 0}, api.InsightsNone: {1, 0, 0}}
	if len(got.Breakdowns) != len(want) {
		t.Fatalf("breakdowns = %+v", got.Breakdowns)
	}
	for _, br := range got.Breakdowns {
		w := want[br.Value]
		if br.Steps[0].Units != w[0] || br.Steps[1].Units != w[1] || br.Steps[2].Units != w[2] {
			t.Errorf("breakdown %s = %+v; want %v", br.Value, br.Steps, w)
		}
	}
}

// ADR 20261117084505 §1, §3: subject units count end users; events without a
// subject are left out.
func TestFunnelBySubject(t *testing.T) {
	f := newFixture(t)
	a := f.customer(t, "a")
	h := time.Hour
	withSubject := func(e state.FlagConversionEvent, s string) state.FlagConversionEvent { e.Subject = s; return e }
	f.track(t,
		withSubject(f.ev(a, "signup", 5*h, nil), "user-1"), withSubject(f.ev(a, "checkout", 4*h, nil), "user-1"),
		withSubject(f.ev(a, "signup", 5*h, nil), "user-2"),
		f.ev(a, "signup", 5*h, nil),
	)
	got, err := insights.Funnel(f.ctx, f.store, insights.FunnelQuery{EnvironmentID: f.scope.EnvironmentID, Range: f.week(),
		Request: api.InsightFunnelRequest{Steps: []api.InsightStepRequest{{Event: "signup"}, {Event: "checkout"}}, Unit: "subject"}})
	if err != nil || got.Steps[0].Units != 2 || got.Steps[1].Units != 1 {
		t.Fatalf("subject funnel = %+v, %v; want 2 then 1", got.Steps, err)
	}
}

// ADR 20261117084505 §3: retention counts each cohort's units that came back
// in each later period, only for periods that have ended.
func TestRetentionCohortsAndEligibility(t *testing.T) {
	f := newFixture(t)
	a, b, c := f.customer(t, "a"), f.customer(t, "b"), f.customer(t, "c")
	day := 24 * time.Hour
	// Cohort day -3 (2026-11-14): a returns on day 1 and 2, b on day 2.
	start := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	at := func(c, name string, t time.Time) state.FlagConversionEvent {
		return state.FlagConversionEvent{CustomerID: c, Name: name, At: t}
	}
	f.track(t,
		at(a, "signup", start), at(a, "open", start.Add(day)), at(a, "open", start.Add(2*day)),
		at(b, "signup", start.Add(time.Hour)), at(b, "open", start.Add(2*day)),
		// c joins on 2026-11-16: only period 1 might have ended, and it has not (now is 11-17 12:00).
		at(c, "signup", start.Add(2*day)), at(c, "open", start.Add(3*day)),
	)
	got, err := insights.Retention(f.ctx, f.store, insights.RetentionQuery{EnvironmentID: f.scope.EnvironmentID, Range: f.week(), Now: f.now,
		Request: api.InsightRetentionRequest{Start: api.InsightStepRequest{Event: "signup"}, Return: api.InsightStepRequest{Event: "open"}, Periods: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cohorts) != 2 {
		t.Fatalf("cohorts = %+v", got.Cohorts)
	}
	first := got.Cohorts[0]
	// Day 1 and 2 after 11-14 have ended; day 3 (11-17) has not.
	if first.Units != 2 || len(first.Returned) != 2 || first.Returned[0] != 1 || first.Returned[1] != 2 || first.Rates[1] != 1 {
		t.Fatalf("first cohort = %+v", first)
	}
	second := got.Cohorts[1]
	if second.Units != 1 || len(second.Returned) != 0 {
		t.Fatalf("second cohort = %+v; want no ended period", second)
	}
	if got.Overall.Units != 3 || got.Overall.Eligible[0] != 2 {
		t.Fatalf("overall = %+v", got.Overall)
	}
}

func TestSeriesByPropertyAndValidation(t *testing.T) {
	f := newFixture(t)
	a, b := f.customer(t, "a"), f.customer(t, "b")
	withProps := func(e state.FlagConversionEvent, p map[string]string, v int64) state.FlagConversionEvent {
		e.Properties, e.Value = p, v
		return e
	}
	f.track(t,
		withProps(f.ev(a, "order", 30*time.Hour, nil), map[string]string{"plan": "pro"}, 100),
		withProps(f.ev(a, "order", 29*time.Hour, nil), map[string]string{"plan": "pro"}, 50),
		withProps(f.ev(b, "order", 2*time.Hour, nil), map[string]string{"plan": "free"}, 10),
	)
	got, err := insights.Series(f.ctx, f.store, insights.SeriesQuery{EnvironmentID: f.scope.EnvironmentID, Event: "order", By: "property:plan", Range: f.week()})
	if err != nil {
		t.Fatal(err)
	}
	var pro, free api.InsightEventPoint
	for _, p := range got.Points {
		switch p.Breakdown {
		case "pro":
			pro = p
		case "free":
			free = p
		}
	}
	if pro.Events != 2 || pro.Units != 1 || pro.Value != 150 || free.Events != 1 {
		t.Fatalf("points = %+v", got.Points)
	}
	for _, q := range []insights.SeriesQuery{
		{EnvironmentID: f.scope.EnvironmentID, Event: "Bad Name", Range: f.week()},
		{EnvironmentID: f.scope.EnvironmentID, Event: "order", By: "country", Range: f.week()},
		{EnvironmentID: f.scope.EnvironmentID, Event: "order", Interval: "hour", Range: insights.Range{From: f.now.AddDate(0, 0, -8), Until: f.now}},
		{EnvironmentID: f.scope.EnvironmentID, Event: "order", Unit: "visitor", Range: f.week()},
	} {
		if _, err := insights.Series(f.ctx, f.store, q); !errors.Is(err, insights.ErrInvalid) {
			t.Errorf("Series(%+v) = %v; want ErrInvalid", q, err)
		}
	}
}

func TestResolveRange(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	r, err := insights.ResolveRange(nil, nil, now, 30, 7)
	if err != nil || !r.Until.Equal(now) || !r.From.Equal(now.AddDate(0, 0, -7)) {
		t.Fatalf("default range = %+v, %v", r, err)
	}
	old := now.AddDate(0, 0, -60)
	if r, err = insights.ResolveRange(&old, nil, now, 30, 7); err != nil || !r.From.Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("clamped to retention = %+v, %v", r, err)
	}
	if _, err := insights.ResolveRange(nil, nil, now, 0, 7); !errors.Is(err, insights.ErrInvalid) {
		t.Fatalf("no retention = %v; want ErrInvalid", err)
	}
	far := now.AddDate(0, 0, -200)
	if _, err := insights.ResolveRange(&far, nil, now, 365, 7); !errors.Is(err, insights.ErrInvalid) {
		t.Fatalf("range over %d days = %v; want ErrInvalid", api.AnalyticsQueryMaxDays, err)
	}
}
