package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessIndependentOutbox(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		ctx := t.Context()
		a, err := s.CreateAccount(ctx, "outbox@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "outbox-app", Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		rule, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: a.ID, AppID: app.ID, Name: "outbox-rule", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, Threshold: 0, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("fixture"), CooldownMinutes: 5})
		if err != nil {
			t.Fatal(err)
		}
		source := s.(state.MCPReadinessOutboxStore)
		timeline := s.(state.MCPReadinessIncidentStore)
		at := time.Now().UTC().Truncate(time.Microsecond)
		coverage := strings.Repeat("a", 64)
		payload, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": coverage, "mcp_readiness_scopes": []string{"staging"}, "mcp_readiness": []api.MCPReadinessMonitorStatus{}})
		observe := func(when time.Time, breach bool) state.MCPReadinessTransition {
			t.Helper()
			v, err := source.RecordMCPReadinessObservation(ctx, state.MCPReadinessObservation{Rule: rule, At: when, Breached: breach, Coverage: coverage, Payload: payload, ObservedValue: 1})
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		first := observe(at, true)
		var wg sync.WaitGroup
		claims := make(chan state.MCPReadinessDeliveryClaim, 8)
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				claim, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, at)
				if err != nil {
					errs <- err
				}
				if won {
					claims <- claim
				}
			})
		}
		wg.Wait()
		close(claims)
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		if len(claims) != 1 {
			t.Fatal("overlapping workers claimed same event", len(claims))
		}
		old := <-claims
		recovery := observe(at.Add(time.Second), false)
		second := observe(at.Add(2*time.Second), true)
		if first.IncidentID != recovery.IncidentID || second.IncidentID == first.IncidentID {
			t.Fatal("transition identities", first, recovery, second)
		}
		if stale := observe(at.Add(time.Second), false); stale.EventID != "" {
			t.Fatal("stale observation changed history", stale)
		}
		page, err := timeline.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "staging", "", 20)
		if err != nil || len(page.Events) != 3 || page.Events[0].IncidentStatus != "open" || page.Events[1].IncidentStatus != "resolved" || page.Events[1].AttemptCount != 0 || page.Events[1].AttemptedAt != nil {
			t.Fatal("history blocked by pending delivery", page, err)
		}
		if _, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, at.Add(time.Minute)); err != nil || won {
			t.Fatal("head lease bypass", won, err)
		}
		reclaimed, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, at.Add(state.MCPReadinessDeliveryLease+time.Second))
		if err != nil || !won || reclaimed.EventID != first.EventID || reclaimed.Token == old.Token {
			t.Fatal("restart reclaim", won, err)
		}
		done := at.Add(state.MCPReadinessDeliveryLease + 2*time.Second)
		if err = s.UpdateAlertDeliveryStatus(ctx, old.Delivery.ID, state.AlertDeliveryDelivered, 1, 200, "", &done); err != nil {
			t.Fatal(err)
		}
		if err = source.CompleteMCPReadinessDelivery(ctx, old.EventID, old.Token, done); !errors.Is(err, state.ErrConflict) {
			t.Fatal("stale worker completed newer lease", err)
		}
		// No transport result simulates a crash/identity failure after the claim.
		if err = source.CompleteMCPReadinessDelivery(ctx, reclaimed.EventID, reclaimed.Token, done); err != nil {
			t.Fatal(err)
		}
		if _, won, err = source.ClaimMCPReadinessDelivery(ctx, rule.ID, done.Add(time.Second)); err != nil || won {
			t.Fatal("cooldown bypass or recovery overtook failed fire", won, err)
		}
		disabled := false
		if _, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Enabled: &disabled}); err != nil {
			t.Fatal(err)
		}
		now := done.Add(time.Hour)
		if _, won, err = source.ClaimMCPReadinessDelivery(ctx, rule.ID, now); err != nil || won {
			t.Fatal("disabled rule dispatched", won, err)
		}
		enabled := true
		if _, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Enabled: &enabled}); err != nil {
			t.Fatal(err)
		}
		for _, eventID := range []string{first.EventID, recovery.EventID, second.EventID} {
			claim, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, now)
			if err != nil || !won || claim.EventID != eventID {
				t.Fatal("queue order", claim, won, err)
			}
			if err = s.UpdateAlertDeliveryStatus(ctx, claim.Delivery.ID, state.AlertDeliveryDelivered, 1, 200, "", &now); err != nil {
				t.Fatal(err)
			}
			if err = source.CompleteMCPReadinessDelivery(ctx, claim.EventID, claim.Token, now); err != nil {
				t.Fatal(err)
			}
		}
		if _, won, err = source.ClaimMCPReadinessDelivery(ctx, rule.ID, now); err != nil || won {
			t.Fatal("delivered event repeated", won, err)
		}
		page, err = timeline.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "", "", 20)
		if err != nil || len(page.Events) != 3 || page.Events[2].AttemptCount != 3 {
			t.Fatal("attempts polluted logical history", page, err)
		}
		foreign, err := s.CreateAccount(ctx, "outbox-foreign@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		// Historical incidents can have a different owner from later incidents
		// on the same rule. Scope each event through its own incident record.
		if pg, ok := s.(*state.PgStore); ok {
			if _, err = (*pg.Pool()).Exec(ctx, `UPDATE mcp_readiness_incidents SET account_id=$2 WHERE id=$1`, first.IncidentID, foreign.ID); err != nil {
				t.Fatal(err)
			}
		} else if mem, ok := s.(*state.MemStore); ok {
			(*mem.Mu()).Lock()
			inc := (*mem.ReadinessIncidents())[first.IncidentID]
			inc.AccountID = foreign.ID
			(*mem.ReadinessIncidents())[first.IncidentID] = inc
			(*mem.Mu()).Unlock()
		}
		page, err = timeline.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "", "", 20)
		if err != nil || len(page.Events) != 1 || page.Events[0].IncidentID != second.IncidentID {
			t.Fatal("incident ownership leaked through rule", page, err)
		}
		if pg, ok := s.(*state.PgStore); ok {
			if _, err = (*pg.Pool()).Exec(ctx, `UPDATE mcp_readiness_incident_events SET delivery_status='pending' WHERE id=$1`, first.EventID); err != nil {
				t.Fatal(err)
			}
		} else if mem, ok := s.(*state.MemStore); ok {
			(*mem.Mu()).Lock()
			ev := (*mem.ReadinessIncidentEvents())[first.EventID]
			ev.Status = "pending"
			(*mem.ReadinessIncidentEvents())[first.EventID] = ev
			(*mem.Mu()).Unlock()
		}
		if _, won, err = source.ClaimMCPReadinessDelivery(ctx, rule.ID, now.Add(time.Hour)); err != nil || won {
			t.Fatal("worker claimed another incident owner's event", won, err)
		}

	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
