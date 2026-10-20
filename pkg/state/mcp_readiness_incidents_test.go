package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
)

func TestMCPReadinessIncidentTimelineStore(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		ctx := t.Context()
		a, err := s.CreateAccount(ctx, "incident-store@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "incident-store", Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		rule, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: a.ID, AppID: app.ID, Name: "readiness", Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("fixture"), CooldownMinutes: 15})
		if err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		for i, scopes := range [][]string{{"default"}, {"staging"}, {"default", "staging"}} {
			input := rule
			input.ID = ""
			input.Name = fmt.Sprintf("readiness-%d", i)
			input.Enabled = true
			input, err = s.CreateAlertRule(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": strings.Repeat("a", 64), "mcp_readiness_scopes": scopes, "mcp_readiness": []api.MCPReadinessMonitorStatus{{Role: "reader", History: []api.MCPReadinessMonitorObservation{{}}}}})
			_, err = s.(state.MCPReadinessOutboxStore).RecordMCPReadinessObservation(ctx, state.MCPReadinessObservation{Rule: input, At: at, Breached: true, Coverage: strings.Repeat("a", 64), Payload: body, ObservedValue: 1})
			if err != nil {
				t.Fatal(err)
			}
		}
		// A retry/recovery insert and a fire claim compete on the same journal
		// successor key. The fire must not overwrite a previously recorded row.
		key := uuid.NewString()
		if _, err = s.RecordAlertDelivery(ctx, state.AlertDelivery{RuleID: rule.ID, AccountID: a.ID, AppID: app.ID, IdempotencyKey: key, Payload: []byte(`{}`), FiredAt: at}); err != nil {
			t.Fatal(err)
		}
		if _, won, err := s.ClaimAlertFire(ctx, rule.ID, key, []byte(`{}`), 1, at.Add(time.Second)); err != nil || won {
			t.Fatal("fire overwrote journal successor", won, err)
		}
		source := s.(state.MCPReadinessIncidentStore)
		page, err := source.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "staging", "", 1)
		if err != nil || len(page.Events) != 1 || page.NextBefore == "" || len(page.Events[0].Subjects[0].History) != 0 {
			t.Fatal("first page", page, err)
		}
		next, err := source.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "staging", page.NextBefore, 1)
		if err != nil || len(next.Events) != 1 || next.NextBefore != "" || next.Events[0].ID == page.Events[0].ID {
			t.Fatal("keyset ties", next, err)
		}
		all, err := source.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "", "", 50)
		if err != nil || len(all.Events) != 3 {
			t.Fatal("all scopes or test isolation", all, err)
		}
		if _, err = source.ListMCPReadinessIncidents(ctx, "foreign", app.ID, "", "", 20); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("ownership", err)
		}
		if _, err = source.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "__all__", "", 20); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("scope", err)
		}
		if _, err = source.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "", "bad-cursor", 20); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("cursor", err)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
