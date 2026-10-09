package state_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-907 against Postgres: quota and unique name, references found from
// match_expr, an update touching referencing rules (change log, no new
// rule-set version), and the delete guard.
func TestPgStore_EdgeRuleLists(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "lists")

	created, err := s.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{
		AccountID: acct, Name: "office", Kind: api.EdgeRuleListKindIP, Items: []string{"203.0.113.0/24"},
	}, 2)
	if err != nil || created.ID == "" || !reflect.DeepEqual(created.Items, []string{"203.0.113.0/24"}) {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if _, err := s.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: acct, Name: "office", Kind: "ip"}, 2); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate name = %v, want ErrConflict", err)
	}
	if _, err := s.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: acct, Name: "second", Kind: "country"}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: acct, Name: "third", Kind: "country"}, 2); !errors.Is(err, state.ErrEdgeRuleListQuota) {
		t.Fatalf("over quota = %v, want ErrEdgeRuleListQuota", err)
	}

	params := pgSampleEdgeRuleParams(acct, app, "lists.example.com")
	params.Match = &api.EdgeRuleMatchExpr{Not: &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"}}
	rule, err := s.CreateEdgeRule(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := s.EdgeRuleListReferences(ctx, acct)
	if err != nil || len(refs["office"]) != 1 || refs["office"][0].RuleID != rule.ID || len(refs["second"]) != 0 {
		t.Fatalf("refs = %+v, %v", refs, err)
	}

	versionStore, _ := any(s).(state.EdgeRuleSetVersionStore)
	var versionsBefore []state.EdgeRuleSetVersion
	if versionStore != nil {
		versionsBefore, _ = versionStore.ListEdgeRuleSetVersions(ctx, app, 100)
	}
	time.Sleep(10 * time.Millisecond)
	items := []string{"198.51.100.0/24"}
	updated, touched, err := s.UpdateEdgeRuleList(ctx, acct, "office", state.UpdateEdgeRuleListParams{Items: &items})
	if err != nil || !reflect.DeepEqual(updated.Items, items) || len(touched) != 1 {
		t.Fatalf("update = %+v touched=%v err=%v", updated, touched, err)
	}
	after, _ := s.GetEdgeRuleByID(ctx, rule.ID)
	if !after.UpdatedAt.After(rule.UpdatedAt) {
		t.Fatal("list update must touch the referencing rule")
	}
	if versionStore != nil {
		versionsAfter, _ := versionStore.ListEdgeRuleSetVersions(ctx, app, 100)
		if len(versionsAfter) != len(versionsBefore) {
			t.Fatalf("list update recorded a rule-set version: %d -> %d", len(versionsBefore), len(versionsAfter))
		}
	}
	byName, err := s.EdgeRuleListsByName(ctx, acct, []string{"office", "missing"})
	if err != nil || len(byName) != 1 || byName[0].Name != "office" {
		t.Fatalf("by name = %+v, %v", byName, err)
	}

	var inUse *state.EdgeRuleListInUseError
	if err := s.DeleteEdgeRuleList(ctx, acct, "office"); !errors.As(err, &inUse) || len(inUse.Refs) != 1 {
		t.Fatalf("delete in use = %v", err)
	}
	if err := s.DeleteEdgeRule(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEdgeRuleList(ctx, acct, "office"); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if err := s.DeleteEdgeRuleList(ctx, acct, "office"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delete missing = %v, want ErrNotFound", err)
	}
}

// ADR-908 against Postgres: batch insert (inet and empty client IPs),
// newest-first paging by (occurred_at, id), filters, and pruning.
func TestPgStore_EdgeRuleEvents(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "events")
	rule, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acct, app, "events.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	var events []state.EdgeRuleEvent
	for i := range 4 {
		outcome := state.EdgeRuleHitMatched
		if i == 3 {
			outcome = state.EdgeRuleHitLogged
		}
		events = append(events, state.EdgeRuleEvent{
			RuleID: rule.ID, AppID: app, OccurredAt: base.Add(-time.Duration(i) * time.Minute), Outcome: outcome,
			Method: "GET", Host: "events.example.com", Path: "/x", ClientIP: "2001:db8::1", Country: "DE", UserAgent: "ua",
		})
	}
	events[1].ClientIP = ""
	events = append(events, state.EdgeRuleEvent{RuleID: rule.ID, AppID: app, OccurredAt: base.Add(-10 * 24 * time.Hour), Outcome: state.EdgeRuleHitMatched})
	if err := s.RecordEdgeRuleEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	page, err := s.ListEdgeRuleEvents(ctx, state.EdgeRuleEventQuery{AppID: app, Since: base.Add(-time.Hour), Limit: 2})
	if err != nil || len(page) != 2 || !page[0].OccurredAt.Equal(base) || page[0].ClientIP != "2001:db8::1" || page[1].ClientIP != "" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	next, err := s.ListEdgeRuleEvents(ctx, state.EdgeRuleEventQuery{AppID: app, Since: base.Add(-time.Hour), Limit: 10, BeforeAt: page[1].OccurredAt, BeforeID: page[1].ID})
	if err != nil || len(next) != 2 {
		t.Fatalf("next page = %+v, %v", next, err)
	}
	logged, err := s.ListEdgeRuleEvents(ctx, state.EdgeRuleEventQuery{AppID: app, RuleID: rule.ID, Outcome: state.EdgeRuleHitLogged, Since: base.Add(-time.Hour), Limit: 10})
	if err != nil || len(logged) != 1 {
		t.Fatalf("logged = %+v, %v", logged, err)
	}
	n, err := s.PruneEdgeRuleEvents(ctx, base.Add(-state.EdgeRuleEventRetention))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v; want 1", n, err)
	}
}

// ADR-904 against Postgres: hits aggregate into hourly buckets and read
// back per rule.
func TestPgStore_EdgeRuleHits(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "hits")
	rule, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acct, app, "hits.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for range 2 {
		if err := s.RecordEdgeRuleHits(ctx, []state.EdgeRuleHit{
			{RuleID: rule.ID, AppID: app, Bucket: now, Outcome: state.EdgeRuleHitMatched, Hits: 3},
			{RuleID: rule.ID, AppID: app, Bucket: now, Outcome: state.EdgeRuleHitLogged, Hits: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := s.EdgeRuleHitStatsForApp(ctx, app, now.Add(-time.Hour))
	if err != nil || len(stats) != 1 || stats[0].Matched != 6 || stats[0].Logged != 2 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
}
