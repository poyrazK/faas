package state_test

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
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessFallback(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"threshold_and_recovery", "reminder_and_two_recoveries", "pinned_destinations_and_rotation", "expired_lease", "disabled_at_first_claim", "concurrent_configuration"} {
				t.Run(scenario, func(t *testing.T) {
					var store state.Store = state.NewMemStore()
					if backend == "postgres" {
						store = state.NewPgStore(pgtest.OpenMigrated(t))
					}
					f := state.NewAttentionFixture(t, store)
					source := store.(state.MCPReadinessFallbackStore)
					config := state.MCPReadinessFallbackConfig{Enabled: true, WebhookURL: "https://fallback.example.com/hook", WebhookSecretSealed: []byte("fallback-sealed"), AfterFailures: 1}
					if scenario == "threshold_and_recovery" {
						config.AfterFailures = 2
					}
					set := func(version int64, c state.MCPReadinessFallbackConfig) api.MCPReadinessFallbackPolicy {
						t.Helper()
						v, err := source.SetMCPReadinessFallback(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, version, c)
						if err != nil {
							t.Fatal(err)
						}
						return v
					}
					initial, err := source.GetMCPReadinessFallback(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID)
					if err != nil || initial.Version != 0 || initial.Enabled {
						t.Fatal(initial, err)
					}
					if scenario == "concurrent_configuration" {
						var wg sync.WaitGroup
						results := make(chan error, 8)
						for range 8 {
							wg.Go(func() {
								_, err := source.SetMCPReadinessFallback(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, config)
								results <- err
							})
						}
						wg.Wait()
						close(results)
						wins := 0
						for err := range results {
							if err == nil {
								wins++
							} else if !errors.Is(err, state.ErrConflict) {
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
					now := (*f.Now())
					first := f.Observe(now, true, "")
					claim := func(event, destination, url string) state.MCPReadinessDeliveryClaim {
						t.Helper()
						v, won, err := (*f.Outbox()).ClaimMCPReadinessDelivery(t.Context(), (*f.Rule()).ID, now)
						if err != nil || !won || v.EventID != event || v.Delivery.ReadinessDestination != destination || v.Rule.WebhookURL != url {
							t.Fatalf("claim: %+v won=%v err=%v", v, won, err)
						}
						return v
					}
					finish := func(v state.MCPReadinessDeliveryClaim, success bool) {
						t.Helper()
						status, code := state.AlertDeliveryFailed, 503
						if success {
							status, code = state.AlertDeliveryDelivered, 200
						}
						if err := store.UpdateAlertDeliveryStatus(t.Context(), v.Delivery.ID, status, 3, code, "", &now); err != nil {
							t.Fatal(err)
						}
						if err := (*f.Outbox()).CompleteMCPReadinessDelivery(t.Context(), v.EventID, v.Token, now); err != nil {
							t.Fatal(err)
						}
					}
					primary := claim(first.EventID, "primary", (*f.Rule()).WebhookURL)
					if scenario == "disabled_at_first_claim" {
						set(0, config)
						finish(primary, false)
						now = now.Add(5 * time.Minute)
						primary = claim(first.EventID, "primary", (*f.Rule()).WebhookURL)
						finish(primary, true)
						recovery := f.Observe(now.Add(time.Second), false, "")
						now = now.Add(time.Second)
						finish(claim(recovery.EventID, "primary", (*f.Rule()).WebhookURL), true)
						return
					}
					if scenario == "expired_lease" {
						old := primary
						now = now.Add(state.MCPReadinessDeliveryLease + time.Second)
						primary = claim(first.EventID, "primary", (*f.Rule()).WebhookURL)
						if err := (*f.Outbox()).CompleteMCPReadinessDelivery(t.Context(), old.EventID, old.Token, now); !errors.Is(err, state.ErrConflict) {
							t.Fatal("expired token", err)
						}
					}
					if scenario == "reminder_and_two_recoveries" {
						finish(primary, true)
						f.Configure(first.IncidentID, now, 2)
						now = now.Add(time.Minute)
						reminder := f.Observe(now, true, "")
						if reminder.EventID == "" {
							t.Fatal("missing reminder")
						}
						primary = claim(reminder.EventID, "primary", (*f.Rule()).WebhookURL)
					}
					if scenario == "pinned_destinations_and_rotation" {
						url := "https://new-primary.example.com/hook"
						if _, err := store.UpdateAlertRule(t.Context(), (*f.Rule()).ID, state.UpdateAlertRuleParams{WebhookURL: &url}); err != nil {
							t.Fatal(err)
						}
						replacement := config
						replacement.WebhookURL = "https://new-fallback.example.com/hook"
						set(1, replacement)
					}
					finish(primary, false)
					if scenario == "threshold_and_recovery" {
						if _, won, err := (*f.Outbox()).ClaimMCPReadinessDelivery(t.Context(), (*f.Rule()).ID, now.Add(time.Second)); err != nil || won {
							t.Fatal("failed claim cooldown", won, err)
						}
						now = now.Add(5 * time.Minute)
						primary = claim(first.EventID, "primary", (*f.Rule()).WebhookURL)
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
						set(3, state.MCPReadinessFallbackConfig{})
					}
					finish(fallback, true)
					now = now.Add(time.Second)
					recovered := f.Observe(now, false, "")
					// Recreate the PG adapter to demonstrate that routing is not process state.
					if pg, ok := store.(*state.PgStore); ok {
						(*f.Outbox()) = state.NewPgStore((*pg.Pool()).Pool)
					}
					recovery := claim(recovered.EventID, "fallback", config.WebhookURL)
					finish(recovery, true)
					if scenario == "reminder_and_two_recoveries" || scenario == "expired_lease" {
						partial := f.Event(recovered.EventID)
						if partial.DeliveryStatus != "pending" || partial.DeliveryRouting == nil || !partial.DeliveryRouting.FallbackDelivered || partial.DeliveryRouting.PrimaryDelivered {
							t.Fatal("partial recovery progress", partial)
						}
						recovery = claim(recovered.EventID, "primary", (*f.Rule()).WebhookURL)
						if recovery.Delivery.ReadinessFailoverReason != "recovery_to_primary" {
							t.Fatal(recovery.Delivery)
						}
						finish(recovery, false)
						now = now.Add(5 * time.Minute)
						finish(claim(recovered.EventID, "primary", (*f.Rule()).WebhookURL), true)
					}
					if _, won, err := (*f.Outbox()).ClaimMCPReadinessDelivery(t.Context(), (*f.Rule()).ID, now); err != nil || won {
						t.Fatal("terminal event repeated", won, err)
					}
					page, err := store.(state.MCPReadinessIncidentStore).ListMCPReadinessIncidents(t.Context(), (*f.Account()).ID, (*f.App()).ID, "staging", "", 20)
					if err != nil || page.Events[0].DeliveryRouting == nil || !page.Events[0].DeliveryRouting.FallbackDelivered || page.Events[0].DeliveryStatus != "delivered" {
						t.Fatal("routing history", page, err)
					}
					deliveries, err := store.ListAlertDeliveriesForRule(t.Context(), (*f.Rule()).ID, 50, false)
					if err != nil || len(deliveries) < 3 {
						t.Fatal(deliveries, err)
					}
					for _, d := range deliveries {
						if d.ReadinessDestination == "" || !strings.Contains(d.IdempotencyKey, d.ReadinessEventID) {
							t.Fatal("missing attempt attribution", d)
						}
					}
					if _, err := source.GetMCPReadinessFallback(t.Context(), uuid.NewString(), (*f.App()).ID, (*f.Rule()).ID); !errors.Is(err, state.ErrNotFound) {
						t.Fatal("foreign read", err)
					}
					if _, err := source.SetMCPReadinessFallback(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, config); !errors.Is(err, state.ErrConflict) {
						t.Fatal("stale policy", err)
					}
				})
			}
		})
	}
}

func TestMCPReadinessFallbackSuppression(t *testing.T) {
	check := func(t *testing.T, store state.Store) {
		f := state.NewAttentionFixture(t, store)
		now := (*f.Now())
		_, err := store.(state.MCPReadinessFallbackStore).SetMCPReadinessFallback(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, state.MCPReadinessFallbackConfig{Enabled: true, WebhookURL: "https://fallback.example.com/hook", WebhookSecretSealed: []byte("sealed"), AfterFailures: 1})
		if err != nil {
			t.Fatal(err)
		}
		first := f.Observe(now, true, "")
		f.Complete(f.Claim(now, true), now, state.AlertDeliveryFailed)
		now = now.Add(time.Second)
		_, err = store.(state.MCPReadinessMaintenanceStore).CreateMCPReadinessMaintenance(t.Context(), (*f.Account()).ID, (*f.App()).ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", EndsAt: now.Add(time.Minute), Reason: "planned receiver work"}, now)
		if err != nil {
			t.Fatal(err)
		}
		f.Claim(now, false)
		if event := f.Event(first.EventID); event.DeliveryStatus != "suppressed" {
			t.Fatal("maintenance did not suppress fallback", event)
		}
		now = now.Add(2 * time.Minute)
		continuation := f.Observe(now, true, "")
		c := f.Claim(now, true)
		if c.EventID != continuation.EventID || c.Delivery.ReadinessDestination != "fallback" {
			t.Fatal("maintenance lost sticky route", c)
		}
		f.Complete(c, now, state.AlertDeliveryDelivered)
		f.Configure(first.IncidentID, now, 2)
		now = now.Add(time.Minute)
		reminder := f.Observe(now, true, "")
		if reminder.EventID == "" {
			t.Fatal("missing reminder")
		}
		f.Act(first.IncidentID, f.Request(first.IncidentID, "acknowledge", now), now)
		f.Claim(now, false)
		if event := f.Event(reminder.EventID); event.SuppressionReason != "acknowledged" || event.AttemptCount != 0 {
			t.Fatal("acknowledged reminder reached fallback", event)
		}
		now = now.Add(time.Second)
		recovery := f.Observe(now, false, "")
		c = f.Claim(now, true)
		if c.EventID != recovery.EventID || c.Delivery.ReadinessDestination != "fallback" {
			t.Fatal("acknowledgement cancelled recovery", c)
		}
		f.Complete(c, now, state.AlertDeliveryDelivered)
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
