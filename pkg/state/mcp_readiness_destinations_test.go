package state_test

import (
	"bytes"
	"encoding/json"
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

func TestMCPReadinessDestinationQueues(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"primary_outage", "independent_leases", "targeted_abandon", "legacy_partial", "expired_primary_retry", "expired_primary_maintenance", "legacy_claim", "receiver_url_order"} {
				t.Run(scenario, func(t *testing.T) {
					var s state.Store = state.NewMemStore()
					if backend == "postgres" {
						s = state.NewPgStore(pgtest.OpenMigrated(t))
					}
					f := state.NewAttentionFixture(t, s)
					now := (*f.Now())
					_, err := s.(state.MCPReadinessFallbackStore).SetMCPReadinessFallback(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, state.MCPReadinessFallbackConfig{Enabled: true, WebhookURL: "https://fallback.example.com/hook", WebhookSecretSealed: []byte("sealed-fallback"), AfterFailures: 1})
					if err != nil {
						t.Fatal(err)
					}
					claim := func(event, destination string) state.MCPReadinessDeliveryClaim {
						t.Helper()
						c, won, err := (*f.Outbox()).ClaimMCPReadinessDelivery(t.Context(), (*f.Rule()).ID, now)
						if err != nil || !won || c.EventID != event || c.Delivery.ReadinessDestination != destination {
							t.Fatalf("claim %s/%s: %+v won=%v err=%v", event, destination, c, won, err)
						}
						return c
					}
					finish := func(c state.MCPReadinessDeliveryClaim, success bool) {
						t.Helper()
						status, code := state.AlertDeliveryFailed, 503
						if success {
							status, code = state.AlertDeliveryDelivered, 200
						}
						if err := s.UpdateAlertDeliveryStatus(t.Context(), c.Delivery.ID, status, 1, code, "", &now); err != nil {
							t.Fatal(err)
						}
						if err := (*f.Outbox()).CompleteMCPReadinessDelivery(t.Context(), c.EventID, c.Token, now); err != nil {
							t.Fatal(err)
						}
					}
					queues := func() api.MCPReadinessNotificationQueue {
						t.Helper()
						page, err := s.(state.MCPReadinessNotificationStore).ListMCPReadinessNotificationQueues(t.Context(), (*f.Account()).ID, (*f.App()).ID, "staging", "", 20, now)
						if err != nil || len(page.Queues) != 1 {
							t.Fatal(page, err)
						}
						raw, _ := json.Marshal(page)
						for _, private := range []string{"lease_token", "sealed-fallback", (*f.Rule()).WebhookURL, "https://fallback.example.com"} {
							if strings.Contains(string(raw), private) {
								t.Fatal("queue leaked private state", string(raw))
							}
						}
						return page.Queues[0]
					}
					action := func(destination, event, verb string) api.MCPReadinessNotificationAction {
						t.Helper()
						q := queues()
						var version int64 = -1
						for _, d := range q.Destinations {
							if d.Destination == destination && d.Head != nil && d.Head.EventID == event {
								version = d.Head.DeliveryVersion
							}
						}
						if version < 0 {
							t.Fatal("missing destination head", destination, event, q)
						}
						return api.MCPReadinessNotificationAction{RequestID: uuid.NewString(), EventID: event, Destination: destination, ExpectedVersion: &version, Action: verb, Reason: "Receiver repaired"}
					}
					first := f.Observe(now, true, "")
					c := claim(first.EventID, "primary")
					if scenario == "expired_primary_maintenance" {
						now = now.Add(state.MCPReadinessDeliveryLease + time.Second)
						if _, err := s.(state.MCPReadinessMaintenanceStore).CreateMCPReadinessMaintenance(t.Context(), (*f.Account()).ID, (*f.App()).ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", EndsAt: now.Add(time.Minute), Reason: "Receiver maintenance"}, now); err != nil {
							t.Fatal(err)
						}
						f.Claim(now, false)
						now = now.Add(2 * time.Minute)
						continuation := f.Observe(now, true, "")
						finish(claim(continuation.EventID, "primary"), false)
						finish(claim(continuation.EventID, "fallback"), true)
						now = now.Add(time.Second)
						recovery := f.Observe(now, false, "")
						finish(claim(recovery.EventID, "fallback"), true)
						finish(claim(recovery.EventID, "primary"), true)
						f.Claim(now, false)
						return
					}
					if scenario == "expired_primary_retry" {
						now = now.Add(state.MCPReadinessDeliveryLease + time.Second)
						retry := action("primary", first.EventID, "retry")
						if _, err := s.(state.MCPReadinessNotificationStore).ActMCPReadinessNotification(t.Context(), (*f.Account()).ID, (*f.App()).ID, retry, now); err != nil {
							t.Fatal(err)
						}
						finish(claim(first.EventID, "primary"), false)
						finish(claim(first.EventID, "fallback"), true)
						now = now.Add(time.Second)
						recovery := f.Observe(now, false, "")
						finish(claim(recovery.EventID, "fallback"), true)
						finish(claim(recovery.EventID, "primary"), true)
						if err := (*f.Outbox()).CompleteMCPReadinessDelivery(t.Context(), c.EventID, c.Token, now); !errors.Is(err, state.ErrConflict) {
							t.Fatal("retired old fence completed", err)
						}
						f.Claim(now, false)
						return
					}
					if scenario == "legacy_claim" {
						// An additive upgrade must not drop a live old-worker claim or deadline.
						if mem, ok := s.(*state.MemStore); ok {
							(*mem.Mu()).Lock()
							e := (*mem.ReadinessIncidentEvents())[c.EventID]
							e.Destinations = nil
							(*mem.ReadinessIncidentEvents())[e.ID] = e
							(*mem.Mu()).Unlock()
						}
						if pg, ok := s.(*state.PgStore); ok {
							if _, err := (*pg.Pool()).Exec(t.Context(), `UPDATE mcp_readiness_incident_events SET destination_deliveries='{}' WHERE id=$1`, c.EventID); err != nil {
								t.Fatal(err)
							}
							(*f.Outbox()) = state.NewPgStore((*pg.Pool()).Pool)
						}
						f.Claim(now.Add(time.Second), false)
						finish(c, false)
						fallback := claim(first.EventID, "fallback")
						if !bytes.Equal(c.Delivery.Payload, fallback.Delivery.Payload) {
							t.Fatal("payload changed on upgrade")
						}
						finish(fallback, true)
						now = now.Add(time.Second)
						recovered := f.Observe(now, false, "")
						if scenario == "targeted_abandon" {
							fallback := claim(recovered.EventID, "fallback")
							primary := claim(recovered.EventID, "primary")
							finish(fallback, false)
							r := action("fallback", recovered.EventID, "abandon")
							source := s.(state.MCPReadinessNotificationStore)
							if _, err := source.ActMCPReadinessNotification(t.Context(), (*f.Account()).ID, (*f.App()).ID, r, now); err != nil {
								t.Fatal(err)
							}
							if err := (*f.Outbox()).CompleteMCPReadinessDelivery(t.Context(), fallback.EventID, fallback.Token, now); !errors.Is(err, state.ErrConflict) {
								t.Fatal("abandoned lease completed", err)
							}
							finish(primary, true)
							if e := f.Event(recovered.EventID); e.DeliveryStatus != "abandoned" || !e.DeliveryRouting.PrimaryDelivered || e.DeliveryRouting.FallbackDelivered {
								t.Fatal("abandon affected wrong receiver", e)
							}
							f.Claim(now, false)
							return
						}
						finish(claim(recovered.EventID, "fallback"), true)
						f.Claim(now, false)
						return
					}
					finish(c, true)
					f.Configure(first.IncidentID, now, 2)
					now = now.Add(time.Minute)
					reminder := f.Observe(now, true, "")
					finish(claim(reminder.EventID, "primary"), false)
					finish(claim(reminder.EventID, "fallback"), true)
					now = now.Add(time.Second)
					recovered := f.Observe(now, false, "")
					if scenario == "independent_leases" {
						// Two workers can own separate receivers of the same recovery. Other
						// contenders must not duplicate either active destination claim.
						var mu sync.Mutex
						var claims []state.MCPReadinessDeliveryClaim
						var wg sync.WaitGroup
						for range 8 {
							wg.Go(func() {
								v, won, err := (*f.Outbox()).ClaimMCPReadinessDelivery(t.Context(), (*f.Rule()).ID, now)
								mu.Lock()
								defer mu.Unlock()
								if err != nil {
									t.Error(err)
								}
								if won {
									claims = append(claims, v)
								}
							})
						}
						wg.Wait()
						if len(claims) != 2 || claims[0].Delivery.ReadinessDestination == claims[1].Delivery.ReadinessDestination {
							t.Fatal("destination leases not isolated", claims)
						}
						var fallback, primary state.MCPReadinessDeliveryClaim
						for _, v := range claims {
							if v.Delivery.ReadinessDestination == "primary" {
								primary = v
							} else {
								fallback = v
							}
						}
						finish(primary, true)
						now = now.Add(state.MCPReadinessDeliveryLease + time.Second)
						replacement := claim(recovered.EventID, "fallback")
						if err := (*f.Outbox()).CompleteMCPReadinessDelivery(t.Context(), fallback.EventID, fallback.Token, now); !errors.Is(err, state.ErrConflict) {
							t.Fatal("stale receiver token accepted", err)
						}
						finish(replacement, false)
						// A failed fallback recovery does not block primary for another incident.
						now = now.Add(time.Second)
						next := f.Observe(now, true, "")
						finish(claim(next.EventID, "primary"), true)
						now = now.Add(time.Second)
						nextRecovery := f.Observe(now, false, "")
						finish(claim(nextRecovery.EventID, "primary"), true)
						now = now.Add(5 * time.Minute)
						finish(claim(recovered.EventID, "fallback"), true)
						if e := f.Event(recovered.EventID); e.DeliveryStatus != "delivered" || !e.DeliveryRouting.PrimaryDelivered || !e.DeliveryRouting.FallbackDelivered {
							t.Fatal(e)
						}
						f.Claim(now, false)
						return
					}
					finish(claim(recovered.EventID, "fallback"), true)
					primary := claim(recovered.EventID, "primary") // Keep primary ownership active.
					if scenario == "legacy_partial" {
						if mem, ok := s.(*state.MemStore); ok {
							(*mem.Mu()).Lock()
							e := (*mem.ReadinessIncidentEvents())[primary.EventID]
							e.Destinations = nil
							(*mem.ReadinessIncidentEvents())[e.ID] = e
							(*mem.Mu()).Unlock()
						}
						if pg, ok := s.(*state.PgStore); ok {
							if _, err := (*pg.Pool()).Exec(t.Context(), `UPDATE mcp_readiness_incident_events SET destination_deliveries='{}' WHERE id=$1`, primary.EventID); err != nil {
								t.Fatal(err)
							}
							(*f.Outbox()) = state.NewPgStore((*pg.Pool()).Pool)
						}
						f.Claim(now, false)
						finish(primary, false)
						if e := f.Event(primary.EventID); !e.DeliveryRouting.FallbackDelivered || e.DeliveryRouting.PrimaryDelivered {
							t.Fatal("legacy successful fallback was lost", e)
						}
						now = now.Add(5 * time.Minute)
						finish(claim(primary.EventID, "primary"), true)
						f.Claim(now, false)
						return
					}
					if scenario == "receiver_url_order" {
						newPrimary := "https://new-primary.example.com/hook"
						if _, err := s.UpdateAlertRule(t.Context(), (*f.Rule()).ID, state.UpdateAlertRuleParams{WebhookURL: &newPrimary}); err != nil {
							t.Fatal(err)
						}
						if _, err := s.(state.MCPReadinessFallbackStore).SetMCPReadinessFallback(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 1, state.MCPReadinessFallbackConfig{Enabled: true, WebhookURL: (*f.Rule()).WebhookURL, WebhookSecretSealed: []byte("new-sealed"), AfterFailures: 1}); err != nil {
							t.Fatal(err)
						}
						now = now.Add(time.Second)
						f.Observe(now, true, "")
						f.Claim(now, false)
						q := queues()
						if len(q.Destinations) != 2 || q.Destinations[1].State != "waiting_receiver" {
							t.Fatal("same receiver reordered across roles", q)
						}
						finish(primary, false)
						f.Claim(now, false)
						return
					}
					now = now.Add(time.Second)
					next := f.Observe(now, true, "")
					nextClaim := claim(next.EventID, "fallback")
					if nextClaim.Delivery.ReadinessFailoverReason != "primary_queue_blocked" {
						t.Fatal(nextClaim.Delivery)
					}
					finish(nextClaim, true)
					f.Configure(next.IncidentID, now, 2)
					now = now.Add(time.Minute)
					nextReminder := f.Observe(now, true, "")
					if nextReminder.EventID == "" {
						t.Fatal("primary backlog suppressed fallback reminder")
					}
					finish(claim(nextReminder.EventID, "fallback"), false)
					retry := action("fallback", nextReminder.EventID, "retry")
					source := s.(state.MCPReadinessNotificationStore)
					receipt, err := source.ActMCPReadinessNotification(t.Context(), (*f.Account()).ID, (*f.App()).ID, retry, now)
					if err != nil || receipt.Destination != "fallback" {
						t.Fatal(receipt, err)
					}
					if replay, err := source.ActMCPReadinessNotification(t.Context(), (*f.Account()).ID, (*f.App()).ID, retry, now); err != nil || replay != receipt {
						t.Fatal("targeted replay", replay, err)
					}
					mismatch := retry
					mismatch.Destination = "primary"
					if _, err := source.ActMCPReadinessNotification(t.Context(), (*f.Account()).ID, (*f.App()).ID, mismatch, now); !errors.Is(err, state.ErrConflict) {
						t.Fatal("request ID reused across destinations", err)
					}
					if _, err := source.ActMCPReadinessNotification(t.Context(), uuid.NewString(), (*f.App()).ID, retry, now); !errors.Is(err, state.ErrNotFound) {
						t.Fatal("foreign action", err)
					}
					if err := (*f.Outbox()).CompleteMCPReadinessDelivery(t.Context(), primary.EventID, "wrong-token", now); !errors.Is(err, state.ErrConflict) {
						t.Fatal("foreign token", err)
					}
					finish(claim(nextReminder.EventID, "fallback"), true)
					now = now.Add(time.Second)
					nextRecovery := f.Observe(now, false, "")
					finish(claim(nextRecovery.EventID, "fallback"), true)
					// Fallback progress and its operator action did not cancel primary's lease.
					finish(primary, false)
					q := queues()
					if q.OutstandingCount != 1 || len(q.Destinations) != 1 || q.Destinations[0].Destination != "primary" || !q.Destinations[0].CanRetry {
						t.Fatal("unfinished primary recovery lost", q)
					}
					retryPrimary := action("primary", recovered.EventID, "retry")
					if _, err := source.ActMCPReadinessNotification(t.Context(), (*f.Account()).ID, (*f.App()).ID, retryPrimary, now); err != nil {
						t.Fatal(err)
					}
					finish(claim(recovered.EventID, "primary"), true)
					now = now.Add(time.Second)
					last := f.Observe(now, true, "")
					finish(claim(last.EventID, "primary"), true)
					f.Claim(now, false)
				})
			}
		})
	}
}

func TestMCPReadinessDestinationSuppressionView(t *testing.T) {
	now := time.Now().UTC()
	e := state.ReadinessIncidentEventRecord{Status: "suppressed", Destinations: map[string]state.ReadinessDestinationDelivery{"primary": {Status: "failed", AvailableAt: now}, "fallback": {Status: "delivered"}}}
	views := state.ReadinessDestinationViews(e, now)
	if len(views) != 2 || views[0].Status != "suppressed" || views[0].NextAttemptAt != nil || views[1].Status != "delivered" {
		t.Fatal("suppression hid successful receiver progress", views)
	}
}
