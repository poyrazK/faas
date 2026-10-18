package state

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessIndependentOutbox(t *testing.T) {
	check := func(t *testing.T, s Store) {
		ctx := t.Context()
		a, err := s.CreateAccount(ctx, "outbox@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, App{AccountID: a.ID, Slug: "outbox-app", Type: AppTypeApp, Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
		rule, err := s.CreateAlertRule(ctx, AlertRule{AccountID: a.ID, AppID: app.ID, Name: "outbox-rule", Enabled: true, Metric: AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: AlertGt, Threshold: 0, WindowSpec: AlertWindow5m, Action: AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("fixture"), CooldownMinutes: 5})
		if err != nil {
			t.Fatal(err)
		}
		source := s.(MCPReadinessOutboxStore)
		timeline := s.(MCPReadinessIncidentStore)
		at := time.Now().UTC().Truncate(time.Microsecond)
		coverage := strings.Repeat("a", 64)
		payload, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": coverage, "mcp_readiness_scopes": []string{"staging"}, "mcp_readiness": []api.MCPReadinessMonitorStatus{}})
		observe := func(when time.Time, breach bool) MCPReadinessTransition {
			t.Helper()
			v, err := source.RecordMCPReadinessObservation(ctx, MCPReadinessObservation{Rule: rule, At: when, Breached: breach, Coverage: coverage, Payload: payload, ObservedValue: 1})
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		first := observe(at, true)
		var wg sync.WaitGroup
		claims := make(chan MCPReadinessDeliveryClaim, 8)
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
		reclaimed, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, at.Add(MCPReadinessDeliveryLease+time.Second))
		if err != nil || !won || reclaimed.EventID != first.EventID || reclaimed.Token == old.Token {
			t.Fatal("restart reclaim", won, err)
		}
		done := at.Add(MCPReadinessDeliveryLease + 2*time.Second)
		if err = s.UpdateAlertDeliveryStatus(ctx, old.Delivery.ID, AlertDeliveryDelivered, 1, 200, "", &done); err != nil {
			t.Fatal(err)
		}
		if err = source.CompleteMCPReadinessDelivery(ctx, old.EventID, old.Token, done); !errors.Is(err, ErrConflict) {
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
		if _, err = s.UpdateAlertRule(ctx, rule.ID, UpdateAlertRuleParams{Enabled: &disabled}); err != nil {
			t.Fatal(err)
		}
		now := done.Add(time.Hour)
		if _, won, err = source.ClaimMCPReadinessDelivery(ctx, rule.ID, now); err != nil || won {
			t.Fatal("disabled rule dispatched", won, err)
		}
		enabled := true
		if _, err = s.UpdateAlertRule(ctx, rule.ID, UpdateAlertRuleParams{Enabled: &enabled}); err != nil {
			t.Fatal(err)
		}
		for _, eventID := range []string{first.EventID, recovery.EventID, second.EventID} {
			claim, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, now)
			if err != nil || !won || claim.EventID != eventID {
				t.Fatal("queue order", claim, won, err)
			}
			if err = s.UpdateAlertDeliveryStatus(ctx, claim.Delivery.ID, AlertDeliveryDelivered, 1, 200, "", &now); err != nil {
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
		if pg, ok := s.(*PgStore); ok {
			if _, err = pg.pool.Exec(ctx, `UPDATE mcp_readiness_incidents SET account_id=$2 WHERE id=$1`, first.IncidentID, foreign.ID); err != nil {
				t.Fatal(err)
			}
		} else if mem, ok := s.(*MemStore); ok {
			mem.mu.Lock()
			inc := mem.readinessIncidents[first.IncidentID]
			inc.AccountID = foreign.ID
			mem.readinessIncidents[first.IncidentID] = inc
			mem.mu.Unlock()
		}
		page, err = timeline.ListMCPReadinessIncidents(ctx, a.ID, app.ID, "", "", 20)
		if err != nil || len(page.Events) != 1 || page.Events[0].IncidentID != second.IncidentID {
			t.Fatal("incident ownership leaked through rule", page, err)
		}
		if pg, ok := s.(*PgStore); ok {
			if _, err = pg.pool.Exec(ctx, `UPDATE mcp_readiness_incident_events SET delivery_status='pending' WHERE id=$1`, first.EventID); err != nil {
				t.Fatal(err)
			}
		} else if mem, ok := s.(*MemStore); ok {
			mem.mu.Lock()
			ev := mem.readinessIncidentEvents[first.EventID]
			ev.Status = "pending"
			mem.readinessIncidentEvents[first.EventID] = ev
			mem.mu.Unlock()
		}
		if _, won, err = source.ClaimMCPReadinessDelivery(ctx, rule.ID, now.Add(time.Hour)); err != nil || won {
			t.Fatal("worker claimed another incident owner's event", won, err)
		}

	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
