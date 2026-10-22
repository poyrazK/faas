package state_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessAlertScopeStore(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		ctx := t.Context()
		a, e := s.CreateAccount(ctx, "scope-alert@example.com", api.PlanPro)
		if e != nil {
			t.Fatal(e)
		}
		app, e := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "scope-alert", Type: state.AppTypeApp, Status: state.AppActive})
		if e != nil {
			t.Fatal(e)
		}
		rules := map[string]state.AlertRule{}
		for _, scope := range []string{"", "default", "staging"} {
			rule, e := s.CreateAlertRule(ctx, state.AlertRule{AccountID: a.ID, AppID: app.ID, Name: "readiness-" + scope, Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), MCPReadinessScope: scope, Comparison: state.AlertGt, Threshold: 0, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("opaque sealed fixture"), CooldownMinutes: 15})
			if e != nil || rule.MCPReadinessScope != scope {
				t.Fatal("create", scope, rule, e)
			}
			rules[scope] = rule
			fetched, e := s.AlertRuleByID(ctx, rule.ID)
			if e != nil || fetched.MCPReadinessScope != scope {
				t.Fatal("read", scope, fetched, e)
			}
			threshold := float64(2)
			url := "https://other.example/hook"
			updated, e := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Threshold: &threshold, WebhookURL: &url})
			if e != nil || updated.MCPReadinessScope != scope || updated.Threshold != threshold || updated.WebhookURL != url {
				t.Fatal("update lost selector", updated, e)
			}
		}
		rows, e := s.ListAlertRulesForAccount(ctx, a.ID)
		if e != nil || len(rows) != 3 {
			t.Fatal("list", rows, e)
		}
		for _, r := range rows {
			if r.ID != rules[r.MCPReadinessScope].ID {
				t.Fatal("list lost environment", r)
			}
		}
		for _, tc := range []struct{ metric, scope string }{{api.MetricMCPReadinessUnhealthy, "__all__"}, {"request_count", "staging"}, {api.MetricMCPProtocolErrorRate, "staging"}} {
			r := rules["default"]
			r.Name = "invalid-" + tc.metric
			r.Metric = state.AlertMetric(tc.metric)
			r.MCPReadinessScope = tc.scope
			if _, e = s.CreateAlertRule(ctx, r); !errors.Is(e, state.ErrInvalidArgument) {
				t.Fatal("invalid selector admitted", tc, e)
			}
		}
		if pg, ok := s.(*state.PgStore); ok {
			_, e = (*pg.Pool()).Exec(ctx, `UPDATE alert_rules SET mcp_readiness_scope='staging' WHERE id=$1`, rules["default"].ID)
			var p *pgconn.PgError
			if !errors.As(e, &p) || p.ConstraintName != "alert_rules_mcp_readiness_scope_immutable" {
				t.Fatal("raw SQL changed rule identity", e)
			}
			_, e = (*pg.Pool()).Exec(ctx, `UPDATE alert_rules SET metric='request_count' WHERE id=$1`, rules["default"].ID)
			if !errors.As(e, &p) || p.ConstraintName != "alert_rules_mcp_readiness_scope_chk" {
				t.Fatal("raw metric change kept invalid selector", e)
			}
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
