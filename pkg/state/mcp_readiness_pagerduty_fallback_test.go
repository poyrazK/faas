package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func nativeFallbackConfig(key, region string, failures int) MCPReadinessFallbackConfig {
	p := pagerDutyConfig(key)
	return MCPReadinessFallbackConfig{Provider: "pagerduty", Enabled: true, Region: region, IntegrationKeySealed: p.IntegrationKeySealed, Fingerprint: p.Fingerprint, AfterFailures: failures}
}
func TestMCPReadinessPagerDutyFallbackLifecycle(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, region := range []string{"us", "eu"} {
				t.Run(region, func(t *testing.T) {
					var s Store = NewMemStore()
					if backend == "postgres" {
						s = NewPgStore(pgtest.OpenMigrated(t))
					}
					f := newAttentionFixture(t, s)
					ctx := t.Context()
					at := f.now
					fallback := s.(MCPReadinessFallbackStore)
					provider := s.(MCPReadinessProviderOutboxStore)
					incoming := s.(MCPReadinessPagerDutyAcknowledgementStore)
					config := nativeFallbackConfig("fallback-original", region, 2)
					p, err := fallback.SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, config)
					if err != nil || p.Provider != "pagerduty" || p.Region != region || p.IntegrationKeyMasked != "***" || p.WebhookURL != "" || p.WebhookSecretSealedMasked != "" {
						t.Fatal(p, err)
					}
					raw, _ := json.Marshal(p)
					if strings.Contains(string(raw), config.Fingerprint) || strings.Contains(string(raw), "sealed-") {
						t.Fatal("private fallback identity leaked")
					}
					if _, err = fallback.SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, config); !errors.Is(err, ErrConflict) {
						t.Fatal("missing CAS", err)
					}
					binding := MCPReadinessPagerDutyAcknowledgementConfig{Enabled: true, Destination: "fallback", ExpectedFallbackVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("sealed-webhook")}
					wrong := binding
					wrong.ExpectedFallbackVersion = 2
					if _, err = incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, wrong); !errors.Is(err, ErrConflict) {
						t.Fatal("stale fallback binding", err)
					}
					bound, err := incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, binding)
					if err != nil || bound.Destination != "fallback" || bound.FallbackVersion != 1 || bound.PagerDutyVersion != 0 || bound.Region != region {
						t.Fatal(bound, err)
					}
					first := f.observe(at, true, "")
					input := MCPReadinessPagerDutyAcknowledgementInput{EventID: "fallback-ack", IncidentID: first.IncidentID, ProviderIncidentID: "PINC001", ServiceID: "PSVC001", Region: region, OccurredAt: at.Add(time.Second)}
					rejectUnactivated := func() {
						t.Helper()
						if _, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, input, at.Add(2*time.Second)); !errors.Is(err, ErrNotFound) {
							t.Fatal("unactivated fallback acknowledged", err)
						}
					}
					rejectUnactivated()
					primary := f.claim(at, true)
					if primary.Provider != "webhook" || primary.Delivery.ReadinessDestination != "primary" {
						t.Fatal(primary)
					}
					rejectUnactivated()
					f.complete(primary, at, AlertDeliveryFailed)
					f.claim(at.Add(time.Second), false)
					at = at.Add(5 * time.Minute)
					primary = f.claim(at, true)
					f.complete(primary, at, AlertDeliveryFailed)
					c := f.claim(at, true)
					endpoint, _ := api.MCPReadinessPagerDutyEndpointForRegion(region)
					if c.Provider != "pagerduty" || c.Delivery.ReadinessDestination != "fallback" || c.Rule.WebhookURL != endpoint || !bytes.Equal(c.Rule.WebhookSecretSealed, config.IntegrationKeySealed) || !bytes.Equal(c.Delivery.Payload, primary.Delivery.Payload) {
						t.Fatal("fallback dispatch changed identity", c.Provider, c.Rule.WebhookURL)
					}
					retry := at.Add(20 * time.Minute)
					if err = provider.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, "wrong", at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); !errors.Is(err, ErrConflict) {
						t.Fatal("fence bypass", err)
					}
					if err = provider.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{StatusCode: 503, Failure: "provider_rejected", RetryAfter: &retry}); err != nil {
						t.Fatal(err)
					}
					event := f.event(first.EventID)
					found := false
					for _, d := range event.DestinationDeliveries {
						if d.Destination == "fallback" {
							found = true
							if d.Provider != "pagerduty" || d.ProviderStatus != "retrying" || d.NextAttemptAt == nil || !d.NextAttemptAt.Equal(retry) {
								t.Fatal(d)
							}
						}
					}
					if !found {
						t.Fatal("missing fallback progress")
					}
					if pg, ok := s.(*PgStore); ok {
						s = NewPgStore(pg.pool.Pool)
						f.store = s
						f.outbox = s.(MCPReadinessOutboxStore)
						f.source = s.(MCPReadinessAttentionStore)
						fallback = s.(MCPReadinessFallbackStore)
						provider = s.(MCPReadinessProviderOutboxStore)
						incoming = s.(MCPReadinessPagerDutyAcknowledgementStore)
					}
					f.claim(at.Add(10*time.Minute), false)
					rewrapped := config
					rewrapped.IntegrationKeySealed = []byte("rewrapped-same-key")
					if _, err = fallback.SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, rewrapped); err != nil {
						t.Fatal(err)
					}
					c = f.claim(retry, true)
					if string(c.Rule.WebhookSecretSealed) != "rewrapped-same-key" {
						t.Fatal("same integration did not refresh encrypted key")
					}
					replacement := nativeFallbackConfig("different-integration", map[string]string{"us": "eu", "eu": "us"}[region], 2)
					if _, err = fallback.SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 2, replacement); err != nil {
						t.Fatal(err)
					}
					// The acknowledgement binding remains attached to the old pinned fallback.
					input.OccurredAt = retry.Add(time.Second)
					if err = provider.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, retry, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
						t.Fatal(err)
					}
					for _, mutate := range []func(*MCPReadinessPagerDutyAcknowledgementInput){func(e *MCPReadinessPagerDutyAcknowledgementInput) { e.ServiceID = "FOREIGN" }, func(e *MCPReadinessPagerDutyAcknowledgementInput) {
						e.Region = map[string]string{"us": "eu", "eu": "us"}[region]
					}} {
						bad := input
						mutate(&bad)
						if _, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, bad, retry.Add(2*time.Second)); !errors.Is(err, ErrNotFound) {
							t.Fatal("foreign fallback event matched", err)
						}
					}
					receipt, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, input, retry.Add(2*time.Second))
					if err != nil || receipt.Outcome != "acknowledged" || receipt.Destination != "fallback" {
						t.Fatal(receipt, err)
					}
					duplicate, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, input, retry.Add(3*time.Second))
					if err != nil || duplicate.ID != receipt.ID || duplicate.AttentionVersion != receipt.AttentionVersion {
						t.Fatal(duplicate, err)
					}
					alias := strings.ReplaceAll(f.rule.ID, "-", "")
					duplicate, err = incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, alias, 1, input, retry.Add(3*time.Second))
					if err != nil || duplicate.ID != receipt.ID {
						t.Fatal("compact rule ID did not resolve", duplicate, err)
					}
					conflict := input
					conflict.ProviderIncidentID = "PINC002"
					if _, err = incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, conflict, retry.Add(3*time.Second)); !errors.Is(err, ErrConflict) {
						t.Fatal("conflicting event reused", err)
					}
					// Disable future fallback, retaining the last used encrypted key for this incident.
					if _, err = fallback.SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 3, MCPReadinessFallbackConfig{}); err != nil {
						t.Fatal(err)
					}
					recovered := f.observe(retry.Add(time.Minute), false, "")
					c = f.claim(retry.Add(time.Minute), true)
					if c.EventID != recovered.EventID || c.Provider != "pagerduty" || c.Rule.WebhookURL != endpoint || string(c.Rule.WebhookSecretSealed) != "rewrapped-same-key" {
						t.Fatal("recovery retargeted or lost key", c.Provider, c.Rule.WebhookURL)
					}
					if err = provider.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, retry.Add(time.Minute), MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
						t.Fatal(err)
					}
					f.claim(retry.Add(2*time.Minute), false)
					input.EventID = "after-recovery"
					input.OccurredAt = retry.Add(2 * time.Minute)
					receipt, err = incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, input, retry.Add(2*time.Minute))
					if err != nil || receipt.Outcome != "resolved" {
						t.Fatal(receipt, err)
					}
					// A new incident follows the disabled policy and stays on the webhook.
					f.observe(retry.Add(10*time.Minute), true, "")
					c = f.claim(retry.Add(10*time.Minute), true)
					if c.Provider != "webhook" {
						t.Fatal("disabled policy affected new incident", c.Provider)
					}
				})
			}
		})
	}
}

func TestMCPReadinessPagerDutyFallbackDistinctIntegrations(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s Store = NewMemStore()
			if backend == "postgres" {
				s = NewPgStore(pgtest.OpenMigrated(t))
			}
			f := newAttentionFixture(t, s)
			ctx := t.Context()
			native := s.(MCPReadinessPagerDutyStore)
			fallback := s.(MCPReadinessFallbackStore)
			p := pagerDutyConfig("same-key")
			c := nativeFallbackConfig("same-key", "us", 1)
			if _, err := native.SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, p); err != nil {
				t.Fatal(err)
			}
			if _, err := fallback.SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, c); !errors.Is(err, ErrInvalidArgument) {
				t.Fatal("same native integration accepted", err)
			}
			if _, err := native.SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, MCPReadinessPagerDutyConfig{}); err != nil {
				t.Fatal(err)
			}
			if _, err := fallback.SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, c); err != nil {
				t.Fatal(err)
			}
			if _, err := native.SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 2, p); !errors.Is(err, ErrInvalidArgument) {
				t.Fatal("primary change created duplicate integration", err)
			}
			for _, bad := range []MCPReadinessFallbackConfig{{Enabled: true, Provider: "other", AfterFailures: 1}, {Enabled: true, Provider: "pagerduty", Region: "eu", AfterFailures: 1, IntegrationKeySealed: []byte("sealed"), Fingerprint: "bad"}, {Provider: "pagerduty"}} {
				if _, err := fallback.SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, bad); !errors.Is(err, ErrInvalidArgument) {
					t.Fatal("invalid fallback stored", err)
				}
			}
		})
	}
}

func TestMCPReadinessPagerDutyFallbackDualRecovery(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"accepted-primary", "expired-primary-lease"} {
				t.Run(scenario, func(t *testing.T) {
					var s Store = NewMemStore()
					if backend == "postgres" {
						s = NewPgStore(pgtest.OpenMigrated(t))
					}
					f := newAttentionFixture(t, s)
					ctx := t.Context()
					at := f.now
					provider := s.(MCPReadinessProviderOutboxStore)
					primary := pagerDutyConfig("primary-key")
					fallback := nativeFallbackConfig("fallback-key", "eu", 1)
					if _, err := s.(MCPReadinessPagerDutyStore).SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, primary); err != nil {
						t.Fatal(err)
					}
					if _, err := s.(MCPReadinessFallbackStore).SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, fallback); err != nil {
						t.Fatal(err)
					}
					first := f.observe(at, true, "")
					c := f.claim(at, true)
					if scenario == "accepted-primary" {
						if err := provider.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
							t.Fatal(err)
						}
						f.configure(first.IncidentID, at, 2)
						at = at.Add(time.Minute)
						f.observe(at, true, "")
						c = f.claim(at, true)
					} else {
						old := c
						at = at.Add(MCPReadinessDeliveryLease + time.Second)
						c = f.claim(at, true)
						if err := provider.CompleteMCPReadinessProviderDelivery(ctx, old.EventID, old.Token, at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); !errors.Is(err, ErrConflict) {
							t.Fatal("old primary lease completed", err)
						}
					}
					if err := provider.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{Failure: "provider_rejected", StatusCode: 400}); err != nil {
						t.Fatal(err)
					}
					c = f.claim(at, true)
					if c.Provider != "pagerduty" || c.Delivery.ReadinessDestination != "fallback" || c.Rule.WebhookURL != api.MCPReadinessPagerDutyEUEndpoint || !bytes.Equal(c.Rule.WebhookSecretSealed, fallback.IntegrationKeySealed) {
						t.Fatal("wrong fallback target")
					}
					if err := provider.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
						t.Fatal(err)
					}
					// Configuration changes after dispatch cannot retarget either recovery.
					if _, err := s.(MCPReadinessPagerDutyStore).SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, pagerDutyConfig("replacement-primary")); err != nil {
						t.Fatal(err)
					}
					if _, err := s.(MCPReadinessFallbackStore).SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, MCPReadinessFallbackConfig{}); err != nil {
						t.Fatal(err)
					}
					at = at.Add(time.Second)
					resolved := f.observe(at, false, "")
					for _, dest := range []string{"fallback", "primary"} {
						c = f.claim(at, true)
						expected := fallback.IntegrationKeySealed
						endpoint := api.MCPReadinessPagerDutyEUEndpoint
						if dest == "primary" {
							expected = primary.IntegrationKeySealed
							endpoint = api.MCPReadinessPagerDutyEndpoint
						}
						if c.EventID != resolved.EventID || c.Provider != "pagerduty" || c.Delivery.ReadinessDestination != dest || c.Rule.WebhookURL != endpoint || !bytes.Equal(c.Rule.WebhookSecretSealed, expected) {
							t.Fatal("recovery target changed", dest, c)
						}
						if err := provider.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
							t.Fatal(err)
						}
					}
					f.claim(at.Add(time.Second), false)
				})
			}
		})
	}
}
