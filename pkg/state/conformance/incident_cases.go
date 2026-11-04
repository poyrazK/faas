package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testAlertEpisodesFollowRuleState pins ADR-20261022193512's alert episodes:
// firing opens one, degraded keeps it, ok resolves it, a later breach opens
// the next, reads are account-scoped, and a rule keeps its newest
// AlertEpisodesPerRule.
func testAlertEpisodesFollowRuleState(t *testing.T, fx *Fixture) {
	t.Helper()
	rule := createEpisodeRule(t, fx)
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	set := func(to state.AlertState, at time.Time) {
		t.Helper()
		if _, err := fx.Store.SetAlertRuleState(fx.Ctx, rule.ID, to, at); err != nil {
			t.Fatalf("SetAlertRuleState(%s): %v", to, err)
		}
	}
	list := func(since time.Time) []state.AlertEpisode {
		t.Helper()
		eps, err := fx.Store.ListAlertEpisodes(fx.Ctx, fx.Account.ID, "", since, 1000)
		if err != nil {
			t.Fatalf("ListAlertEpisodes: %v", err)
		}
		var mine []state.AlertEpisode
		for _, ep := range eps {
			if ep.RuleID == rule.ID {
				mine = append(mine, ep)
			}
		}
		return mine
	}

	set(state.AlertStateFiring, t0)
	set(state.AlertStateDegraded, t0.Add(time.Minute))
	set(state.AlertStateFiring, t0.Add(2*time.Minute))
	eps := list(t0.Add(-time.Hour))
	if len(eps) != 1 || eps[0].ResolvedAt != nil || !eps[0].OpenedAt.Equal(t0) {
		t.Fatalf("episodes after firing, degraded, firing = %+v; want one open since %s", eps, t0)
	}
	if ep := eps[0]; ep.RuleName != rule.Name || ep.AppID != fx.App.ID || ep.Metric != rule.Metric || ep.RuleState != state.AlertStateFiring {
		t.Fatalf("episode rule columns = %+v", ep)
	}
	set(state.AlertStateOk, t0.Add(5*time.Minute))
	set(state.AlertStateFiring, t0.Add(10*time.Minute))
	eps = list(t0.Add(-time.Hour))
	if len(eps) != 2 || eps[0].ResolvedAt != nil || !eps[0].OpenedAt.Equal(t0.Add(10*time.Minute)) ||
		eps[1].ResolvedAt == nil || !eps[1].ResolvedAt.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("episodes after recovery and a new breach = %+v", eps)
	}
	if eps := list(t0.Add(6 * time.Minute)); len(eps) != 1 || eps[0].ResolvedAt != nil {
		t.Fatalf("resolved before the window must drop out, open must stay: %+v", eps)
	}
	if other, err := fx.Store.ListAlertEpisodes(fx.Ctx, fx.Account.ID, uuid.NewString(), t0.Add(-time.Hour), 10); err != nil || len(other) != 0 {
		t.Fatalf("another app's episodes = %+v, %v; want none", other, err)
	}
	if other, err := fx.Store.ListAlertEpisodes(fx.Ctx, uuid.NewString(), "", t0.Add(-time.Hour), 10); err != nil || len(other) != 0 {
		t.Fatalf("another account's episodes = %+v, %v; want none", other, err)
	}
	got, err := fx.Store.AlertEpisodeByID(fx.Ctx, fx.Account.ID, eps[1].ID)
	if err != nil || got.ID != eps[1].ID || got.RuleName != rule.Name {
		t.Fatalf("AlertEpisodeByID = %+v, %v", got, err)
	}
	for _, id := range []string{"not-a-uuid", uuid.NewString()} {
		if _, err := fx.Store.AlertEpisodeByID(fx.Ctx, fx.Account.ID, id); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AlertEpisodeByID(%q) = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := fx.Store.AlertEpisodeByID(fx.Ctx, uuid.NewString(), eps[1].ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's episode = %v, want ErrNotFound", err)
	}

	// A flapping rule keeps its newest AlertEpisodesPerRule episodes.
	rule = createEpisodeRule(t, fx)
	at := t0
	for range api.AlertEpisodesPerRule + 3 {
		set(state.AlertStateFiring, at)
		set(state.AlertStateOk, at.Add(time.Second))
		at = at.Add(time.Minute)
	}
	eps = list(t0.Add(-time.Hour))
	if len(eps) != api.AlertEpisodesPerRule || !eps[0].OpenedAt.Equal(at.Add(-time.Minute)) {
		t.Fatalf("flapping rule keeps %d episodes (newest %v); want %d ending with the latest", len(eps), eps[0].OpenedAt, api.AlertEpisodesPerRule)
	}
}

// testRouteMonitorIncidentSummariesAreAccountScoped pins the incident list's
// route monitor read: an account without incidents reads none, and a
// malformed or unknown id is not found.
func testRouteMonitorIncidentSummariesAreAccountScoped(t *testing.T, fx *Fixture) {
	t.Helper()
	since := time.Now().Add(-time.Hour)
	for _, appID := range []string{"", fx.App.ID} {
		got, err := fx.Store.ListRouteMonitorIncidentSummaries(fx.Ctx, fx.Account.ID, appID, since, 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("ListRouteMonitorIncidentSummaries(app %q) = %+v, %v; want none", appID, got, err)
		}
	}
	for _, id := range []string{"not-a-uuid", uuid.NewString()} {
		if _, err := fx.Store.RouteMonitorIncidentSummaryByID(fx.Ctx, fx.Account.ID, id); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("RouteMonitorIncidentSummaryByID(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func createEpisodeRule(t *testing.T, fx *Fixture) state.AlertRule {
	t.Helper()
	rule, err := fx.Store.CreateAlertRule(fx.Ctx, state.AlertRule{
		AccountID: fx.Account.ID, AppID: fx.App.ID, Name: "episodes-" + uuid.NewString(), Enabled: true,
		Metric: state.AlertMetric("error_rate_pct"), Comparison: state.AlertComparison("gt"), Threshold: 5,
		WindowSpec: state.AlertWindowSpec("5m"), Action: state.AlertAction("webhook"), WebhookURL: "https://example.test/hook",
		WebhookSecretSealed: []byte("sealed-conformance"), CooldownMinutes: 15, State: state.AlertState("ok"),
	})
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}
	return rule
}
