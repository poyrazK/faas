//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR 20261117084505 §1–§3: raw events keep their subject hash, known
// properties and flag decisions, and answer every analytics primitive; the
// sweep removes events past their plan's retention.
func TestPg_AnalyticsEventsAndPrimitives(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "insights-pg")
	s, ctx := f.s, f.ctx
	a, b := f.tenant("a"), f.tenant("b")
	now := time.Now().UTC().Truncate(time.Second)
	h := time.Hour
	scope := state.FeatureFlagScope{AccountID: f.acct, ProjectID: f.project, EnvironmentID: f.env}
	ev := func(c, name string, ago time.Duration, subject string, props, flags map[string]string) state.FlagConversionEvent {
		return state.FlagConversionEvent{CustomerID: c, Name: name, At: now.Add(-ago), Subject: subject, Properties: props, Flags: flags, Value: 10}
	}
	res, err := s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 1000, MaxNames: 10, Now: now, Analytics: true, MaxPropertyKeys: 1,
		Events: []state.FlagConversionEvent{
			ev(a, "signup", 5*h, "user-1", map[string]string{"source": "email", "zzz": "dropped"}, map[string]string{"checkout": "new"}),
			ev(a, "checkout", 4*h, "user-1", nil, nil),
			ev(b, "signup", 3*h, "", nil, nil),
			ev(b, "page_view", 6*h, "", nil, map[string]string{"checkout": "legacy"}),
		}})
	if err != nil || res.Accepted != 4 {
		t.Fatalf("ingest = %+v, %v", res, err)
	}
	match := state.AnalyticsMatch{EnvironmentID: f.env, Name: "signup", From: now.Add(-24 * h), Until: now}

	rows, err := s.AnalyticsEventSeries(ctx, state.AnalyticsSeriesQuery{AnalyticsMatch: match, Bucket: 24 * h, ByKind: "property", ByKey: "source", Unit: state.AnalyticsUnitCustomer})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Breakdown] += r.Events
	}
	if got["email"] != 1 || got["(none)"] != 1 {
		t.Fatalf("series by source = %+v", rows)
	}

	entries, err := s.AnalyticsEntries(ctx, match, state.AnalyticsUnitCustomer, 10)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	for _, e := range entries {
		if _, ok := e.Properties["zzz"]; ok {
			t.Fatalf("entry %+v kept a property key past the environment bound", e)
		}
	}
	subjects, err := s.AnalyticsEntries(ctx, match, state.AnalyticsUnitSubject, 10)
	if err != nil || len(subjects) != 1 || string(subjects[0].Unit.SubjectHash) != string(state.AnalyticsSubjectHash(f.env, "user-1")) {
		t.Fatalf("subject entries = %+v, %v", subjects, err)
	}

	steps := []state.AnalyticsStep{}
	for _, e := range entries {
		steps = append(steps, state.AnalyticsStep{Unit: e.Unit, After: e.At, Deadline: e.At.Add(24 * h)})
	}
	next, err := s.AnalyticsNextSteps(ctx, state.AnalyticsMatch{EnvironmentID: f.env, Name: "checkout"}, state.AnalyticsUnitCustomer, steps)
	if err != nil || len(next) != 1 || !next[a].Equal(now.Add(-4*h)) {
		t.Fatalf("next steps = %v, %v; want only a", next, err)
	}
	subjectNext, err := s.AnalyticsNextSteps(ctx, state.AnalyticsMatch{EnvironmentID: f.env, Name: "checkout"}, state.AnalyticsUnitSubject,
		[]state.AnalyticsStep{{Unit: subjects[0].Unit, After: subjects[0].At, Deadline: now}})
	if err != nil || len(subjectNext) != 1 {
		t.Fatalf("subject next steps = %v, %v", subjectNext, err)
	}

	flagsByUnit, err := s.AnalyticsUnitFlags(ctx, f.env, state.AnalyticsUnitCustomer, "checkout",
		[]state.AnalyticsUnitRef{{CustomerID: a}, {CustomerID: b}}, now.Add(-24*h), now)
	if err != nil || flagsByUnit[a] != "new" || flagsByUnit[b] != "legacy" {
		t.Fatalf("unit flags = %v, %v", flagsByUnit, err)
	}

	cohort := now.Add(-5 * h).Truncate(h)
	returns, err := s.AnalyticsReturns(ctx, state.AnalyticsMatch{EnvironmentID: f.env, Name: "checkout"}, state.AnalyticsUnitCustomer,
		[]state.AnalyticsCohortUnit{{Unit: state.AnalyticsUnitRef{CustomerID: a}, FirstAt: now.Add(-5 * h), Cohort: cohort, Breakdown: "x"}}, 30*time.Minute, 6)
	if err != nil || len(returns) != 1 || returns[0].Units != 1 || returns[0].Breakdown != "x" || returns[0].Period < 1 {
		t.Fatalf("returns = %+v, %v", returns, err)
	}

	// Request telemetry by flag value: the fixture's exposures carry the
	// checkout flag's "weighted" rule decision.
	f.exposure(a, "new", now.Add(-time.Minute), 500, 120)
	f.exposure(b, "legacy", now.Add(-time.Minute), 200, 40)
	reqs, err := s.AnalyticsRequestsByFlag(ctx, state.AnalyticsRequestsQuery{AccountID: f.acct, ProjectID: f.project, EnvironmentSlug: "production",
		Flag: "checkout", From: now.Add(-h), Until: now.Add(h)})
	if err != nil || len(reqs) != 2 {
		t.Fatalf("requests by flag = %+v, %v", reqs, err)
	}
	for _, r := range reqs {
		if (r.Value == "new" && r.Errors != 1) || (r.Value == "legacy" && r.Errors != 0) {
			t.Fatalf("requests by flag = %+v", reqs)
		}
	}

	// The fixture account is on a plan with retention; a zero retention for
	// it deletes everything, a long one nothing.
	acct, err := s.AccountByID(ctx, f.acct)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.SweepAnalyticsEvents(ctx, now, map[string]int{string(acct.Plan): 30}, 2, 10); err != nil || n != 0 {
		t.Fatalf("sweep within retention = %d, %v; want 0", n, err)
	}
	if n, err := s.SweepAnalyticsEvents(ctx, now, map[string]int{string(acct.Plan): 0}, 2, 10); err != nil || n != 4 {
		t.Fatalf("sweep at zero retention = %d, %v; want 4 in batches of 2", n, err)
	}
}
