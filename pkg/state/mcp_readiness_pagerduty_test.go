package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func pagerDutyConfig(key string) MCPReadinessPagerDutyConfig {
	sum := sha256.Sum256([]byte(key))
	return MCPReadinessPagerDutyConfig{Enabled: true, IntegrationKeySealed: []byte("sealed-" + key), Fingerprint: hex.EncodeToString(sum[:])}
}
func TestMCPReadinessPagerDuty(t *testing.T) {
	check := func(t *testing.T, s Store) {
		f := newAttentionFixture(t, s)
		source := s.(MCPReadinessPagerDutyStore)
		outbox := s.(MCPReadinessProviderOutboxStore)
		at := f.now
		set := func(version int64, c MCPReadinessPagerDutyConfig) {
			t.Helper()
			v, e := source.SetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID, version, c)
			if e != nil || v.Version != version+1 || v.Enabled != c.Enabled {
				t.Fatal(v, e)
			}
			b, _ := json.Marshal(v)
			if strings.Contains(string(b), "sealed-") || strings.Contains(string(b), c.Fingerprint) && c.Fingerprint != "" {
				t.Fatal("secret leaked", string(b))
			}
		}
		p, e := source.GetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID)
		if e != nil || p.Version != 0 || p.Enabled {
			t.Fatal(p, e)
		}
		a, b := pagerDutyConfig("a"), pagerDutyConfig("b")
		set(0, a)
		var wg sync.WaitGroup
		var wins atomic.Int32
		for range 8 {
			wg.Go(func() {
				_, e := source.SetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 1, a)
				if e == nil {
					wins.Add(1)
				} else if !errors.Is(e, ErrConflict) {
					t.Error(e)
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatal(wins.Load())
		}
		first := f.observe(at, true, "")
		c := f.claim(at, true)
		if c.Provider != "pagerduty" || c.Rule.WebhookURL != api.MCPReadinessPagerDutyEndpoint || string(c.Rule.WebhookSecretSealed) != string(a.IntegrationKeySealed) {
			t.Fatal("wrong native receiver", c.Provider)
		}
		set(2, b)
		retry := at.Add(time.Hour)
		r := MCPReadinessProviderResult{StatusCode: 429, Failure: "provider_rejected", RetryAfter: &retry}
		if e = outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, "wrong", at, r); !errors.Is(e, ErrConflict) {
			t.Fatal("lease fence", e)
		}
		if e = outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, at, r); e != nil {
			t.Fatal(e)
		}
		f.claim(at.Add(30*time.Minute), false)
		v := f.event(first.EventID)
		if len(v.DestinationDeliveries) != 1 || v.DestinationDeliveries[0].ProviderStatus != "retrying" || !v.DestinationDeliveries[0].NextAttemptAt.Equal(retry) {
			t.Fatal(v)
		}
		c = f.claim(retry, true)
		if string(c.Rule.WebhookSecretSealed) != string(a.IntegrationKeySealed) {
			t.Fatal("configuration retargeted active incident")
		}
		if e = outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, retry, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); e != nil {
			t.Fatal(e)
		}
		if e = outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, retry, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); !errors.Is(e, ErrConflict) {
			t.Fatal("duplicate completion", e)
		}
		if v = f.event(first.EventID); v.DestinationDeliveries[0].ProviderStatus != "accepted" || v.DeliveryRouting == nil || v.DeliveryRouting.PagerDutyVersion != 2 {
			t.Fatal(v)
		}
		f.configure(first.IncidentID, retry, 2)
		reminder := f.observe(retry.Add(time.Minute), true, "")
		c = f.claim(retry.Add(time.Minute), true)
		if reminder.IncidentID != first.IncidentID || c.Provider != "pagerduty" || string(c.Rule.WebhookSecretSealed) != string(a.IntegrationKeySealed) {
			t.Fatal("reminder changed target")
		}
		if e = outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, retry.Add(time.Minute), MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); e != nil {
			t.Fatal(e)
		}
		set(3, MCPReadinessPagerDutyConfig{})
		recovered := f.observe(retry.Add(2*time.Minute), false, "")
		c = f.claim(retry.Add(2*time.Minute), true)
		if recovered.IncidentID != first.IncidentID || c.Provider != "pagerduty" || string(c.Rule.WebhookSecretSealed) != string(a.IntegrationKeySealed) {
			t.Fatal("recovery retargeted")
		}
		if e = outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, retry.Add(2*time.Minute), MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); e != nil {
			t.Fatal(e)
		}
		f.observe(retry.Add(3*time.Minute), true, "")
		if c = f.claim(retry.Add(3*time.Minute), true); c.Provider != "webhook" || c.Rule.WebhookURL != f.rule.WebhookURL {
			t.Fatal("disable ignored")
		}
		other := newAttentionFixture(t, s)
		if _, e = source.GetMCPReadinessPagerDuty(t.Context(), other.account.ID, other.app.ID, f.rule.ID); !errors.Is(e, ErrNotFound) {
			t.Fatal("ownership", e)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
func TestMCPReadinessPagerDutyFallback(t *testing.T) {
	check := func(t *testing.T, s Store) {
		f := newAttentionFixture(t, s)
		at := f.now
		if _, e := s.(MCPReadinessPagerDutyStore).SetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 0, pagerDutyConfig("a")); e != nil {
			t.Fatal(e)
		}
		if _, e := s.(MCPReadinessFallbackStore).SetMCPReadinessFallback(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 0, MCPReadinessFallbackConfig{Enabled: true, WebhookURL: "https://fallback.example.com/hook", WebhookSecretSealed: []byte("sealed"), AfterFailures: 1}); e != nil {
			t.Fatal(e)
		}
		f.observe(at, true, "")
		c := f.claim(at, true)
		if e := s.(MCPReadinessProviderOutboxStore).CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, at, MCPReadinessProviderResult{Failure: "transport_error"}); e != nil {
			t.Fatal(e)
		}
		c = f.claim(at.Add(time.Second), true)
		if c.Provider != "webhook" || c.Delivery.ReadinessDestination != "fallback" {
			t.Fatal(c.Provider)
		}
		f.complete(c, at.Add(time.Second), AlertDeliveryDelivered)
		f.observe(at.Add(2*time.Second), false, "")
		c = f.claim(at.Add(2*time.Second), true)
		if c.Delivery.ReadinessDestination != "fallback" {
			t.Fatal(c)
		}
		f.complete(c, at.Add(2*time.Second), AlertDeliveryDelivered)
		c = f.claim(at.Add(3*time.Second), true)
		if c.Provider != "pagerduty" || c.Delivery.ReadinessDestination != "primary" {
			t.Fatal("uncertain primary lost recovery")
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessPagerDutyUnattemptedAction(t *testing.T) {
	check := func(t *testing.T, s Store) {
		f := newAttentionFixture(t, s)
		at := f.now
		source := s.(MCPReadinessPagerDutyStore)
		if _, e := source.SetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 0, pagerDutyConfig("a")); e != nil {
			t.Fatal(e)
		}
		first := f.observe(at, true, "")
		queues, e := s.(MCPReadinessNotificationStore).ListMCPReadinessNotificationQueues(t.Context(), f.account.ID, f.app.ID, "staging", "", 20, at)
		if e != nil {
			t.Fatal(e)
		}
		if len(queues.Queues) != 1 || queues.Queues[0].Destinations[0].Provider != "pagerduty" {
			t.Fatal(queues)
		}
		version := int64(0)
		// A pre-attempt retry is rejected and cannot pin an integration.
		if _, e = s.(MCPReadinessNotificationStore).ActMCPReadinessNotification(t.Context(), f.account.ID, f.app.ID, api.MCPReadinessNotificationAction{RequestID: uuid.NewString(), EventID: first.EventID, Destination: "primary", Action: "retry", ExpectedVersion: &version, Reason: "Retry before first delivery"}, at); !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
		if _, e = source.SetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 1, MCPReadinessPagerDutyConfig{}); e != nil {
			t.Fatal(e)
		}
		if c := f.claim(at, true); c.Provider != "webhook" || c.Rule.WebhookURL != f.rule.WebhookURL {
			t.Fatal("queue action pinned obsolete provider", c.Provider)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessPagerDutyRegions(t *testing.T) {
	check := func(t *testing.T, s Store) {
		for _, original := range []string{"us", "eu"} {
			t.Run(original, func(t *testing.T) {
				f := newAttentionFixture(t, s)
				source := s.(MCPReadinessPagerDutyStore)
				outbox := s.(MCPReadinessProviderOutboxStore)
				p, err := source.GetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID)
				if err != nil || p.Region != "us" {
					t.Fatal(p, err)
				}
				config := pagerDutyConfig("regional-key")
				config.Region = original
				set := func(version int64, c MCPReadinessPagerDutyConfig) {
					t.Helper()
					p, err := source.SetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID, version, c)
					if err != nil || p.Region != readinessPagerDutyRegion(c) {
						t.Fatal(p, err)
					}
				}
				// First delivery, rather than incident opening or queue inspection, pins region.
				set(0, pagerDutyConfig("regional-key"))
				first := f.observe(f.now, true, "")
				if _, err := s.(MCPReadinessNotificationStore).ListMCPReadinessNotificationQueues(t.Context(), f.account.ID, f.app.ID, "staging", "", 20, f.now); err != nil {
					t.Fatal(err)
				}
				set(1, config)
				endpoint, _ := api.MCPReadinessPagerDutyEndpointForRegion(original)
				assertClaim := func(at time.Time) MCPReadinessDeliveryClaim {
					t.Helper()
					c := f.claim(at, true)
					if c.Provider != "pagerduty" || c.Rule.WebhookURL != endpoint || string(c.Rule.WebhookSecretSealed) != string(config.IntegrationKeySealed) {
						t.Fatal("regional incident retargeted", c.Rule.WebhookURL)
					}
					return c
				}
				c := assertClaim(f.now)
				changed := config
				changed.Region = "eu"
				if original == "eu" {
					changed.Region = "us"
				}
				// Same key, different region: do not even refresh ciphertext across regions.
				changed.IntegrationKeySealed = []byte("replacement-sealed-key")
				set(2, changed)
				if err := outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, f.now, MCPReadinessProviderResult{Failure: "transport_error"}); err != nil {
					t.Fatal(err)
				}
				at := f.now.Add(time.Hour)
				c = assertClaim(at)
				if err := outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				f.configure(first.IncidentID, at, 1)
				f.observe(at.Add(time.Minute), true, "")
				c = assertClaim(at.Add(time.Minute))
				if err := outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, at.Add(time.Minute), MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				// Disabling still owes recovery to the original regional integration.
				set(3, MCPReadinessPagerDutyConfig{})
				f.observe(at.Add(2*time.Minute), false, "")
				c = assertClaim(at.Add(2 * time.Minute))
				if err := outbox.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, at.Add(2*time.Minute), MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				// The next incident uses the replacement region.
				set(4, changed)
				f.observe(at.Add(3*time.Minute), true, "")
				c = f.claim(at.Add(3*time.Minute), true)
				next, _ := api.MCPReadinessPagerDutyEndpointForRegion(changed.Region)
				if c.Rule.WebhookURL != next || string(c.Rule.WebhookSecretSealed) != string(changed.IntegrationKeySealed) {
					t.Fatal("new incident ignored regional update")
				}
				for _, invalid := range []string{"ap", "US", "https://example.com"} {
					bad := config
					bad.Region = invalid
					if _, err := source.SetMCPReadinessPagerDuty(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 5, bad); !errors.Is(err, ErrInvalidArgument) {
						t.Fatal(invalid, err)
					}
				}
			})
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessPagerDutyRegionalReceiverOrdering(t *testing.T) {
	rule := AlertRule{WebhookURL: "https://example.com/hook"}
	config := pagerDutyConfig("same-key")
	eu := config
	eu.Region = "eu"
	primary := func(c MCPReadinessPagerDutyConfig) readinessIncidentRouting {
		r := readinessIncidentRouting{}
		initializeReadinessRoute(rule, MCPReadinessFallbackConfig{PrimaryPagerDuty: c}, &r, readinessIncidentEventRecord{}, false)
		return r
	}
	usRoute, euRoute := primary(config), primary(eu)
	// A US primary and EU fallback using the same key must not block one another.
	usRoute.Activated, usRoute.PrimaryInformed = true, true
	usRoute.Fallback = euRoute.Primary
	at := time.Now().UTC()
	events := []readinessIncidentEventRecord{{ID: "recovery", IncidentID: "incident", Kind: "alert.resolved", At: at, Sequence: 1}}
	plan := readinessDestinationPlan(rule, MCPReadinessFallbackConfig{}, events, map[string]readinessIncident{"incident": {Routing: usRoute}})
	if len(plan) != 2 || plan[0].ReceiverBlocked || plan[1].ReceiverBlocked {
		t.Fatal("regional receivers conflated", plan)
	}
	usRoute.Fallback = usRoute.Primary
	plan = readinessDestinationPlan(rule, MCPReadinessFallbackConfig{}, events, map[string]readinessIncident{"incident": {Routing: usRoute}})
	if len(plan) != 2 || !plan[1].ReceiverBlocked {
		t.Fatal("same regional receiver lost FIFO", plan)
	}
}
