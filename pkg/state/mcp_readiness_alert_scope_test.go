package state

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessAlertScopeStore(t *testing.T) {
	check := func(t *testing.T, s Store) {
		ctx := t.Context()
		a, e := s.CreateAccount(ctx, "scope-alert@example.com", api.PlanPro)
		if e != nil {
			t.Fatal(e)
		}
		app, e := s.CreateApp(ctx, App{AccountID: a.ID, Slug: "scope-alert", Type: AppTypeApp, Status: AppActive})
		if e != nil {
			t.Fatal(e)
		}
		rules := map[string]AlertRule{}
		for _, scope := range []string{"", "default", "staging"} {
			rule, e := s.CreateAlertRule(ctx, AlertRule{AccountID: a.ID, AppID: app.ID, Name: "readiness-" + scope, Enabled: true, Metric: AlertMetric(api.MetricMCPReadinessUnhealthy), MCPReadinessScope: scope, Comparison: AlertGt, Threshold: 0, WindowSpec: AlertWindow5m, Action: AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("opaque sealed fixture"), CooldownMinutes: 15})
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
			updated, e := s.UpdateAlertRule(ctx, rule.ID, UpdateAlertRuleParams{Threshold: &threshold, WebhookURL: &url})
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
			r.Metric = AlertMetric(tc.metric)
			r.MCPReadinessScope = tc.scope
			if _, e = s.CreateAlertRule(ctx, r); !errors.Is(e, ErrInvalidArgument) {
				t.Fatal("invalid selector admitted", tc, e)
			}
		}
		if pg, ok := s.(*PgStore); ok {
			_, e = pg.pool.Exec(ctx, `UPDATE alert_rules SET mcp_readiness_scope='staging' WHERE id=$1`, rules["default"].ID)
			var p *pgconn.PgError
			if !errors.As(e, &p) || p.ConstraintName != "alert_rules_mcp_readiness_scope_immutable" {
				t.Fatal("raw SQL changed rule identity", e)
			}
			_, e = pg.pool.Exec(ctx, `UPDATE alert_rules SET metric='request_count' WHERE id=$1`, rules["default"].ID)
			if !errors.As(e, &p) || p.ConstraintName != "alert_rules_mcp_readiness_scope_chk" {
				t.Fatal("raw metric change kept invalid selector", e)
			}
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
