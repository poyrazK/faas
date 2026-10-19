package state

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessNotificationRecovery(t *testing.T) {
	check := func(t *testing.T, s Store) {
		ctx := t.Context()
		account, err := s.CreateAccount(ctx, "notification-recovery@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "notification-recovery", Type: AppTypeApp, Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
		outbox := s.(MCPReadinessOutboxStore)
		source := s.(MCPReadinessNotificationStore)
		windows := s.(MCPReadinessMaintenanceStore)
		now := time.Now().UTC().Truncate(time.Microsecond)
		makeRule := func(name, scope string) AlertRule {
			t.Helper()
			r, err := s.CreateAlertRule(ctx, AlertRule{AccountID: account.ID, AppID: app.ID, Name: name, Enabled: true, Metric: AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: AlertGt, Threshold: 0, WindowSpec: AlertWindow5m, Action: AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("private-secret"), CooldownMinutes: 5, MCPReadinessScope: scope})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		observe := func(rule AlertRule, at time.Time, breach bool) MCPReadinessTransition {
			t.Helper()
			coverage := strings.Repeat("a", 64)
			p, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": coverage, "mcp_readiness_scopes": []string{"staging"}, "mcp_readiness": []api.MCPReadinessMonitorStatus{}})
			v, err := outbox.RecordMCPReadinessObservation(ctx, MCPReadinessObservation{Rule: rule, At: at, Breached: breach, Coverage: coverage, Payload: p, ObservedValue: 1})
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		health := func(id string, at time.Time) api.MCPReadinessNotificationQueue {
			t.Helper()
			p, err := source.ListMCPReadinessNotificationQueues(ctx, account.ID, app.ID, "staging", "", 50, at)
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range p.Queues {
				if q.RuleID == id {
					return q
				}
			}
			t.Fatal("missing queue", id, p)
			return api.MCPReadinessNotificationQueue{}
		}
		request := func(event string, version int64, action string) api.MCPReadinessNotificationAction {
			return api.MCPReadinessNotificationAction{RequestID: uuid.NewString(), EventID: event, ExpectedVersion: &version, Action: action, Reason: "Receiver credentials repaired"}
		}
		conflict := func(r api.MCPReadinessNotificationAction, at time.Time) {
			t.Helper()
			if _, err := source.ActMCPReadinessNotification(ctx, account.ID, app.ID, r, at); !errors.Is(err, ErrConflict) {
				t.Fatal("expected conflict", r, err)
			}
		}
		rule := makeRule("recovery", "staging")
		first := observe(rule, now, true)
		if q := health(rule.ID, now); q.CanRetry || !q.CanAbandon || q.Head.DeliveryVersion != 0 {
			t.Fatal("fresh head", q)
		}
		claim, won, err := outbox.ClaimMCPReadinessDelivery(ctx, rule.ID, now)
		if err != nil || !won {
			t.Fatal(claim, won, err)
		}
		q := health(rule.ID, now)
		if q.State != "delivering" || q.CanAbandon || q.CanRetry || q.Head.DeliveryVersion != 1 {
			t.Fatal("live lease", q)
		}
		conflict(request(first.EventID, q.Head.DeliveryVersion, "abandon"), now)
		done := now.Add(time.Second)
		if err = s.UpdateAlertDeliveryStatus(ctx, claim.Delivery.ID, AlertDeliveryFailed, 1, 503, "private-response-secret", nil); err != nil {
			t.Fatal(err)
		}
		if err = outbox.CompleteMCPReadinessDelivery(ctx, first.EventID, claim.Token, done); err != nil {
			t.Fatal(err)
		}
		recovered := observe(rule, now.Add(2*time.Second), false)
		q = health(rule.ID, now.Add(3*time.Second))
		if q.State != "delayed" || q.OutstandingCount != 2 || q.BlockedCount != 1 || q.OldestAgeSeconds != 3 || q.FailureKind != "http_5xx" || !q.CanRetry || q.Head.DeliveryVersion != 2 {
			t.Fatal("outage health", q)
		}
		body, _ := json.Marshal(q)
		if strings.Contains(string(body), "private-") || strings.Contains(string(body), "example.com") {
			t.Fatal("secret in health", string(body))
		}
		conflict(request(recovered.EventID, 0, "abandon"), done)
		retry := request(first.EventID, q.Head.DeliveryVersion, "retry")
		var wg sync.WaitGroup
		results := make(chan api.MCPReadinessNotificationReceipt, 8)
		failures := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				r, err := source.ActMCPReadinessNotification(ctx, account.ID, app.ID, retry, now.Add(3*time.Second))
				if err != nil {
					failures <- err
				} else {
					results <- r
				}
			})
		}
		wg.Wait()
		close(results)
		close(failures)
		for err := range failures {
			t.Fatal("idempotent concurrency", err)
		}
		if len(results) != 8 {
			t.Fatal("missing receipts")
		}
		var receipt api.MCPReadinessNotificationReceipt
		for r := range results {
			if receipt.RequestID != "" && r != receipt {
				t.Fatal("receipt changed", receipt, r)
			}
			receipt = r
		}
		if receipt.DeliveryVersion != 3 || receipt.CreatedBy != account.ID {
			t.Fatal("receipt attribution", receipt)
		}
		changed := retry
		changed.Reason = "different decision"
		conflict(changed, now.Add(4*time.Second))
		stale := retry
		stale.RequestID = uuid.NewString()
		conflict(stale, now.Add(4*time.Second))
		again, won, err := outbox.ClaimMCPReadinessDelivery(ctx, rule.ID, now.Add(4*time.Second))
		if err != nil || !won || again.EventID != first.EventID || string(again.Delivery.Payload) != string(claim.Delivery.Payload) {
			t.Fatal("retry changed event or did not bypass cooldown", again, won, err)
		}
		q = health(rule.ID, now.Add(5*time.Second))
		conflict(request(first.EventID, q.Head.DeliveryVersion, "abandon"), now.Add(5*time.Second))
		expired := again.LeaseUntil.Add(time.Second)
		q = health(rule.ID, expired)
		if q.FailureKind != "lease_expired" || !q.CanAbandon || !q.CanRetry {
			t.Fatal("expired lease", q)
		}
		abandon := request(first.EventID, q.Head.DeliveryVersion, "abandon")
		abandoned, err := source.ActMCPReadinessNotification(ctx, account.ID, app.ID, abandon, expired)
		if err != nil {
			t.Fatal(err)
		}
		if err = outbox.CompleteMCPReadinessDelivery(ctx, again.EventID, again.Token, expired); !errors.Is(err, ErrConflict) {
			t.Fatal("abandonment failed to fence worker", err)
		}
		if pg, ok := s.(*PgStore); ok {
			source = NewPgStore(pg.pool.Pool)
		} // Durable receipt survives a new store instance.
		duplicate, err := source.ActMCPReadinessNotification(ctx, account.ID, app.ID, abandon, expired.Add(time.Hour))
		if err != nil || duplicate != abandoned {
			t.Fatal("abandon receipt replay", duplicate, err)
		}
		next, won, err := outbox.ClaimMCPReadinessDelivery(ctx, rule.ID, expired)
		if err != nil || !won || next.EventID != recovered.EventID {
			t.Fatal("queue did not advance", next, won, err)
		}
		if err = s.UpdateAlertDeliveryStatus(ctx, next.Delivery.ID, AlertDeliveryDelivered, 1, 200, "", &expired); err != nil {
			t.Fatal(err)
		}
		if err = outbox.CompleteMCPReadinessDelivery(ctx, next.EventID, next.Token, expired); err != nil {
			t.Fatal(err)
		}
		if q = health(rule.ID, expired); q.State != "idle" || q.OutstandingCount != 0 {
			t.Fatal("not drained", q)
		}
		duplicate, err = source.ActMCPReadinessNotification(ctx, account.ID, app.ID, retry, expired)
		if err != nil || duplicate != receipt {
			t.Fatal("old retry receipt changed", duplicate, err)
		}
		conflict(request(first.EventID, abandoned.DeliveryVersion, "retry"), expired)
		history, err := s.(MCPReadinessIncidentStore).ListMCPReadinessIncidents(ctx, account.ID, app.ID, "staging", "", 20)
		if err != nil || len(history.Events) != 2 {
			t.Fatal(history, err)
		}
		event := history.Events[1]
		if event.EventID != first.EventID || event.DeliveryStatus != "abandoned" || event.NextAttemptAt != nil || event.AttemptCount != 2 || event.IncidentStatus != "resolved" || event.LastOperatorAction == nil || *event.LastOperatorAction != abandoned {
			t.Fatal("abandon erased incident evidence", event)
		}

		pausedRule := makeRule("paused", "staging")
		paused := observe(pausedRule, now, true)
		disabled := false
		if _, err = s.UpdateAlertRule(ctx, pausedRule.ID, UpdateAlertRuleParams{Enabled: &disabled}); err != nil {
			t.Fatal(err)
		}
		q = health(pausedRule.ID, now)
		if q.State != "paused" || q.CanAbandon || q.CanRetry {
			t.Fatal("paused", q)
		}
		conflict(request(paused.EventID, q.Head.DeliveryVersion, "abandon"), now)
		enabled := true
		if _, err = s.UpdateAlertRule(ctx, pausedRule.ID, UpdateAlertRuleParams{Enabled: &enabled}); err != nil {
			t.Fatal(err)
		}
		conflict(request(paused.EventID, 0, "retry"), now) // Unattempted work cannot be retried.

		// Distinct concurrent decisions against one version cannot both succeed.
		decisions := make(chan error, 2)
		for range 2 {
			wg.Go(func() {
				_, err := source.ActMCPReadinessNotification(ctx, account.ID, app.ID, request(paused.EventID, 0, "abandon"), now)
				decisions <- err
			})
		}
		wg.Wait()
		close(decisions)
		successes, conflicts := 0, 0
		for err := range decisions {
			if err == nil {
				successes++
			} else if errors.Is(err, ErrConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatal("concurrent decisions both applied", successes, conflicts)
		}

		mutedRule := makeRule("muted", "")
		muted := observe(mutedRule, now, true)
		if _, err = windows.CreateMCPReadinessMaintenance(ctx, account.ID, app.ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", EndsAt: now.Add(time.Hour), Reason: "planned work"}, now); err != nil {
			t.Fatal(err)
		}
		q = health(mutedRule.ID, now)
		if q.State != "maintenance" || q.CanAbandon || q.CanRetry {
			t.Fatal("muted", q)
		}
		conflict(request(muted.EventID, 0, "abandon"), now)
		// Crossing the entire window during downtime must not allow manual replay.
		conflict(request(muted.EventID, 0, "abandon"), now.Add(2*time.Hour))
		if _, won, err = outbox.ClaimMCPReadinessDelivery(ctx, mutedRule.ID, now.Add(2*time.Hour)); err != nil || won {
			t.Fatal("muted backlog sent", won, err)
		}
		conflict(request(muted.EventID, 1, "retry"), now.Add(2*time.Hour))
		// Maintenance reconciliation must preserve abandoned terminal events, even
		// when their incident remains open and is selected for reconciliation.
		for _, id := range []string{rule.ID, pausedRule.ID} {
			if _, won, err = outbox.ClaimMCPReadinessDelivery(ctx, id, now.Add(2*time.Hour)); err != nil || won {
				t.Fatal("abandoned event reclaimed", id, won, err)
			}
		}

		history, err = s.(MCPReadinessIncidentStore).ListMCPReadinessIncidents(ctx, account.ID, app.ID, "staging", "", 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range history.Events {
			if (e.EventID == first.EventID || e.EventID == paused.EventID) && (e.DeliveryStatus != "abandoned" || e.LastOperatorAction == nil) {
				t.Fatal("maintenance rewrote abandoned evidence", e)
			}
		}

		_ = makeRule("production", "production")
		page, err := source.ListMCPReadinessNotificationQueues(ctx, account.ID, app.ID, "staging", "", 1, now)
		if err != nil || len(page.Queues) != 1 || page.NextAfter == "" {
			t.Fatal("pagination", page, err)
		}
		seen := map[string]bool{page.Queues[0].RuleID: true}
		for page.NextAfter != "" {
			page, err = source.ListMCPReadinessNotificationQueues(ctx, account.ID, app.ID, "staging", page.NextAfter, 1, now)
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range page.Queues {
				if seen[q.RuleID] || q.ReadinessScope == "production" {
					t.Fatal("duplicate/wrong scope", q)
				}
				seen[q.RuleID] = true
			}
		}
		if len(seen) != 3 {
			t.Fatal("scope omitted all-environment rule", seen)
		}
		foreign, err := s.CreateAccount(ctx, "notification-foreign@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = source.ListMCPReadinessNotificationQueues(ctx, foreign.ID, app.ID, "", "", 20, now); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign health", err)
		}
		if _, err = source.ActMCPReadinessNotification(ctx, foreign.ID, app.ID, retry, now); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign receipt", err)
		}
		bad := request(uuid.NewString(), 0, "abandon")
		if _, err = source.ActMCPReadinessNotification(ctx, account.ID, app.ID, bad, now); !errors.Is(err, ErrNotFound) {
			t.Fatal("unknown event", err)
		}
		if err = s.DeleteAlertRule(ctx, rule.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = source.ActMCPReadinessNotification(ctx, account.ID, app.ID, retry, now); !errors.Is(err, ErrNotFound) {
			t.Fatal("deleted rule kept receipt", err)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
