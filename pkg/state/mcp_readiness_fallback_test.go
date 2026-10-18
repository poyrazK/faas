package state

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessFallback(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"threshold_and_recovery", "reminder_and_two_recoveries", "pinned_destinations_and_rotation", "expired_lease", "disabled_at_first_claim", "concurrent_configuration"} {
				t.Run(scenario, func(t *testing.T) {
					var store Store = NewMemStore()
					if backend == "postgres" {
						store = NewPgStore(pgtest.OpenMigrated(t))
					}
					f := newAttentionFixture(t, store)
					source := store.(MCPReadinessFallbackStore)
					config := MCPReadinessFallbackConfig{Enabled: true, WebhookURL: "https://fallback.example.com/hook", WebhookSecretSealed: []byte("fallback-sealed"), AfterFailures: 1}
					if scenario == "threshold_and_recovery" {
						config.AfterFailures = 2
					}
					set := func(version int64, c MCPReadinessFallbackConfig) api.MCPReadinessFallbackPolicy {
						t.Helper()
						v, err := source.SetMCPReadinessFallback(t.Context(), f.account.ID, f.app.ID, f.rule.ID, version, c)
						if err != nil {
							t.Fatal(err)
						}
						return v
					}
					initial, err := source.GetMCPReadinessFallback(t.Context(), f.account.ID, f.app.ID, f.rule.ID)
					if err != nil || initial.Version != 0 || initial.Enabled {
						t.Fatal(initial, err)
					}
					if scenario == "concurrent_configuration" {
						var wg sync.WaitGroup
						results := make(chan error, 8)
						for range 8 {
							wg.Go(func() {
								_, err := source.SetMCPReadinessFallback(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 0, config)
								results <- err
							})
						}
						wg.Wait()
						close(results)
						wins := 0
						for err := range results {
							if err == nil {
								wins++
							} else if !errors.Is(err, ErrConflict) {
								t.Fatal(err)
							}
						}
						if wins != 1 {
							t.Fatal("policy replacement lost fencing", wins)
						}
						return
					}
					if scenario != "disabled_at_first_claim" {
						v := set(0, config)
						if v.WebhookSecretSealedMasked != "***" || v.Version != 1 {
							t.Fatal(v)
						}
					}
					now := f.now
					first := f.observe(now, true, "")
					claim := func(event, destination, url string) MCPReadinessDeliveryClaim {
						t.Helper()
						v, won, err := f.outbox.ClaimMCPReadinessDelivery(t.Context(), f.rule.ID, now)
						if err != nil || !won || v.EventID != event || v.Delivery.ReadinessDestination != destination || v.Rule.WebhookURL != url {
							t.Fatalf("claim: %+v won=%v err=%v", v, won, err)
						}
						return v
					}
					finish := func(v MCPReadinessDeliveryClaim, success bool) {
						t.Helper()
						status, code := AlertDeliveryFailed, 503
						if success {
							status, code = AlertDeliveryDelivered, 200
						}
						if err := store.UpdateAlertDeliveryStatus(t.Context(), v.Delivery.ID, status, 3, code, "", &now); err != nil {
							t.Fatal(err)
						}
						if err := f.outbox.CompleteMCPReadinessDelivery(t.Context(), v.EventID, v.Token, now); err != nil {
							t.Fatal(err)
						}
					}
					primary := claim(first.EventID, "primary", f.rule.WebhookURL)
					if scenario == "disabled_at_first_claim" {
						set(0, config)
						finish(primary, false)
						now = now.Add(5 * time.Minute)
						primary = claim(first.EventID, "primary", f.rule.WebhookURL)
						finish(primary, true)
						recovery := f.observe(now.Add(time.Second), false, "")
						now = now.Add(time.Second)
						finish(claim(recovery.EventID, "primary", f.rule.WebhookURL), true)
						return
					}
					if scenario == "expired_lease" {
						old := primary
						now = now.Add(MCPReadinessDeliveryLease + time.Second)
						primary = claim(first.EventID, "primary", f.rule.WebhookURL)
						if err := f.outbox.CompleteMCPReadinessDelivery(t.Context(), old.EventID, old.Token, now); !errors.Is(err, ErrConflict) {
							t.Fatal("expired token", err)
						}
					}
					if scenario == "reminder_and_two_recoveries" {
						finish(primary, true)
						f.configure(first.IncidentID, now, 2)
						now = now.Add(time.Minute)
						reminder := f.observe(now, true, "")
						if reminder.EventID == "" {
							t.Fatal("missing reminder")
						}
						primary = claim(reminder.EventID, "primary", f.rule.WebhookURL)
					}
					if scenario == "pinned_destinations_and_rotation" {
						url := "https://new-primary.example.com/hook"
						if _, err := store.UpdateAlertRule(t.Context(), f.rule.ID, UpdateAlertRuleParams{WebhookURL: &url}); err != nil {
							t.Fatal(err)
						}
						replacement := config
						replacement.WebhookURL = "https://new-fallback.example.com/hook"
						set(1, replacement)
					}
					finish(primary, false)
					if scenario == "threshold_and_recovery" {
						if _, won, err := f.outbox.ClaimMCPReadinessDelivery(t.Context(), f.rule.ID, now.Add(time.Second)); err != nil || won {
							t.Fatal("failed claim cooldown", won, err)
						}
						now = now.Add(5 * time.Minute)
						primary = claim(first.EventID, "primary", f.rule.WebhookURL)
						finish(primary, false)
					}
					fallback := claim(primary.EventID, "fallback", config.WebhookURL)
					if !bytes.Equal(primary.Delivery.Payload, fallback.Delivery.Payload) || primary.Delivery.ReadinessEventID != fallback.Delivery.ReadinessEventID || fallback.Delivery.ReadinessFailoverReason != "primary_failure_threshold" {
						t.Fatal("event identity or reason changed", primary.Delivery, fallback.Delivery)
					}
					if scenario == "pinned_destinations_and_rotation" {
						if string(fallback.Rule.WebhookSecretSealed) != "fallback-sealed" {
							t.Fatal("redirected credential")
						}
						finish(fallback, false)
						rotated := config
						rotated.WebhookSecretSealed = []byte("rotated-sealed")
						set(2, rotated)
						now = now.Add(5 * time.Minute)
						fallback = claim(primary.EventID, "fallback", config.WebhookURL)
						if string(fallback.Rule.WebhookSecretSealed) != "rotated-sealed" {
							t.Fatal("rotation was not used")
						}
						set(3, MCPReadinessFallbackConfig{})
					}
					finish(fallback, true)
					now = now.Add(time.Second)
					recovered := f.observe(now, false, "")
					// Recreate the PG adapter to demonstrate that routing is not process state.
					if pg, ok := store.(*PgStore); ok {
						f.outbox = NewPgStore(pg.pool)
					}
					recovery := claim(recovered.EventID, "fallback", config.WebhookURL)
					finish(recovery, true)
					if scenario == "reminder_and_two_recoveries" || scenario == "expired_lease" {
						partial := f.event(recovered.EventID)
						if partial.DeliveryStatus != "pending" || partial.DeliveryRouting == nil || !partial.DeliveryRouting.FallbackDelivered || partial.DeliveryRouting.PrimaryDelivered {
							t.Fatal("partial recovery progress", partial)
						}
						recovery = claim(recovered.EventID, "primary", f.rule.WebhookURL)
						if recovery.Delivery.ReadinessFailoverReason != "recovery_to_primary" {
							t.Fatal(recovery.Delivery)
						}
						finish(recovery, false)
						now = now.Add(5 * time.Minute)
						finish(claim(recovered.EventID, "primary", f.rule.WebhookURL), true)
					}
					if _, won, err := f.outbox.ClaimMCPReadinessDelivery(t.Context(), f.rule.ID, now); err != nil || won {
						t.Fatal("terminal event repeated", won, err)
					}
					page, err := store.(MCPReadinessIncidentStore).ListMCPReadinessIncidents(t.Context(), f.account.ID, f.app.ID, "staging", "", 20)
					if err != nil || page.Events[0].DeliveryRouting == nil || !page.Events[0].DeliveryRouting.FallbackDelivered || page.Events[0].DeliveryStatus != "delivered" {
						t.Fatal("routing history", page, err)
					}
					deliveries, err := store.ListAlertDeliveriesForRule(t.Context(), f.rule.ID, 50, false)
					if err != nil || len(deliveries) < 3 {
						t.Fatal(deliveries, err)
					}
					for _, d := range deliveries {
						if d.ReadinessDestination == "" || !strings.Contains(d.IdempotencyKey, d.ReadinessEventID) {
							t.Fatal("missing attempt attribution", d)
						}
					}
					if _, err := source.GetMCPReadinessFallback(t.Context(), uuid.NewString(), f.app.ID, f.rule.ID); !errors.Is(err, ErrNotFound) {
						t.Fatal("foreign read", err)
					}
					if _, err := source.SetMCPReadinessFallback(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 0, config); !errors.Is(err, ErrConflict) {
						t.Fatal("stale policy", err)
					}
				})
			}
		})
	}
}

func TestMCPReadinessFallbackSuppression(t *testing.T) {
	check := func(t *testing.T, store Store) {
		f := newAttentionFixture(t, store)
		now := f.now
		_, err := store.(MCPReadinessFallbackStore).SetMCPReadinessFallback(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 0, MCPReadinessFallbackConfig{Enabled: true, WebhookURL: "https://fallback.example.com/hook", WebhookSecretSealed: []byte("sealed"), AfterFailures: 1})
		if err != nil {
			t.Fatal(err)
		}
		first := f.observe(now, true, "")
		f.complete(f.claim(now, true), now, AlertDeliveryFailed)
		now = now.Add(time.Second)
		_, err = store.(MCPReadinessMaintenanceStore).CreateMCPReadinessMaintenance(t.Context(), f.account.ID, f.app.ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", EndsAt: now.Add(time.Minute), Reason: "planned receiver work"}, now)
		if err != nil {
			t.Fatal(err)
		}
		f.claim(now, false)
		if event := f.event(first.EventID); event.DeliveryStatus != "suppressed" {
			t.Fatal("maintenance did not suppress fallback", event)
		}
		now = now.Add(2 * time.Minute)
		continuation := f.observe(now, true, "")
		c := f.claim(now, true)
		if c.EventID != continuation.EventID || c.Delivery.ReadinessDestination != "fallback" {
			t.Fatal("maintenance lost sticky route", c)
		}
		f.complete(c, now, AlertDeliveryDelivered)
		f.configure(first.IncidentID, now, 2)
		now = now.Add(time.Minute)
		reminder := f.observe(now, true, "")
		if reminder.EventID == "" {
			t.Fatal("missing reminder")
		}
		f.act(first.IncidentID, f.request(first.IncidentID, "acknowledge", now), now)
		f.claim(now, false)
		if event := f.event(reminder.EventID); event.SuppressionReason != "acknowledged" || event.AttemptCount != 0 {
			t.Fatal("acknowledged reminder reached fallback", event)
		}
		now = now.Add(time.Second)
		recovery := f.observe(now, false, "")
		c = f.claim(now, true)
		if c.EventID != recovery.EventID || c.Delivery.ReadinessDestination != "fallback" {
			t.Fatal("acknowledgement cancelled recovery", c)
		}
		f.complete(c, now, AlertDeliveryDelivered)
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
