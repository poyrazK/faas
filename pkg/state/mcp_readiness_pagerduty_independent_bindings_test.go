package state

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPagerDutyIndependentBindings(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, first := range []string{"primary", "fallback"} {
			t.Run(backend+"/"+first, func(t *testing.T) {
				var s Store = NewMemStore()
				if backend == "postgres" {
					s = NewPgStore(pgtest.OpenMigrated(t))
				}
				f := newAttentionFixture(t, s)
				ctx := t.Context()
				at := f.now
				if _, err := s.(MCPReadinessPagerDutyStore).SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, pagerDutyConfig("independent-primary")); err != nil {
					t.Fatal(err)
				}
				if _, err := s.(MCPReadinessFallbackStore).SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, nativeFallbackConfig("independent-fallback", "us", 1)); err != nil {
					t.Fatal(err)
				}
				incoming := s.(MCPReadinessPagerDutyAcknowledgementStore)
				bindings := map[string]MCPReadinessPagerDutyAcknowledgementConfig{
					"primary":  {Destination: "primary", Enabled: true, ExpectedPagerDutyVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("sealed-primary")},
					"fallback": {Destination: "fallback", Enabled: true, ExpectedFallbackVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("sealed-fallback")},
				}
				other := "fallback"
				if first == "fallback" {
					other = "primary"
				}
				// A destination-specific bootstrap URL cannot be reassigned by configuring the other first.
				bootstrap, err := incoming.GetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, other)
				if err != nil || !strings.HasSuffix(bootstrap.WebhookPath, "/"+other) {
					t.Fatal(bootstrap, err)
				}
				for _, d := range []string{first, other} {
					policy, err := incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, bindings[d])
					if err != nil || policy.Version != 1 || policy.Destination != d {
						t.Fatal(policy, err)
					}
				}
				if pg, ok := s.(*PgStore); ok {
					s = NewPgStore(pg.pool)
					incoming = s.(MCPReadinessPagerDutyAcknowledgementStore)
				}
				legacy, err := incoming.ReadMCPReadinessPagerDutyAcknowledgementReceiver(ctx, f.rule.ID)
				if err != nil || legacy.Destination != first {
					t.Fatal("legacy callback retargeted", legacy.Destination, err)
				}
				for _, d := range []string{"primary", "fallback"} {
					policy, err := incoming.GetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, d)
					if err != nil || policy.Version != 1 || !strings.HasSuffix(policy.WebhookPath, "/"+d) {
						t.Fatal(policy, err)
					}
					receiver, err := incoming.ReadMCPReadinessPagerDutyAcknowledgementReceiver(ctx, f.rule.ID, d)
					if err != nil || !bytes.Equal(receiver.SigningSecretSealed, bindings[d].SigningSecretSealed) {
						t.Fatal("signing secrets mixed", d, err)
					}
				}
				opened := f.observe(at, true, "")
				event := MCPReadinessPagerDutyAcknowledgementInput{BindingDestination: "fallback", EventID: "shared-provider-event", IncidentID: opened.IncidentID, ProviderIncidentID: "PINC001", ServiceID: "PSVC001", Region: "us", OccurredAt: at.Add(time.Second)}
				if _, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, event, at.Add(2*time.Second)); !errors.Is(err, ErrNotFound) {
					t.Fatal("unactivated fallback accepted", err)
				}
				c := f.claim(at, true)
				if err := s.(MCPReadinessProviderOutboxStore).CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{StatusCode: 503, Failure: "provider_rejected"}); err != nil {
					t.Fatal(err)
				}
				c = f.claim(at, true)
				if c.Delivery.ReadinessDestination != "fallback" {
					t.Fatal("fallback not activated", c.Delivery.ReadinessDestination)
				}
				if err := s.(MCPReadinessProviderOutboxStore).CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				event.BindingDestination = "primary"
				receipt, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, event, at.Add(2*time.Second))
				if err != nil || receipt.Outcome != "acknowledged" || receipt.Destination != "primary" {
					t.Fatal(receipt, err)
				}
				f.act(opened.IncidentID, f.request(opened.IncidentID, "release", at.Add(3*time.Second)), at.Add(3*time.Second))
				event.BindingDestination = "fallback"
				duplicate, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, event, at.Add(4*time.Second))
				if err != nil || duplicate.ID != receipt.ID || duplicate.Destination != "primary" {
					t.Fatal("cross-destination replay changed receipt", duplicate, err)
				}
				attention := f.view(opened.IncidentID, at.Add(4*time.Second))
				if attention.Version != 2 || attention.Acknowledgement != nil {
					t.Fatal("duplicate reapplied after release", attention)
				}
				changed := event
				changed.ProviderIncidentID = "OTHER"
				if _, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, changed, at.Add(4*time.Second)); !errors.Is(err, ErrConflict) {
					t.Fatal("conflicting cross-destination replay accepted", err)
				}
				// A fallback rotation changes neither the primary secret nor its version.
				config := bindings["fallback"]
				config.SigningSecretSealed = []byte("rotated-fallback")
				if _, err := incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, config); err != nil {
					t.Fatal(err)
				}
				if _, err := incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, event, at.Add(5*time.Second)); !errors.Is(err, ErrConflict) {
					t.Fatal("old fallback verification fence survived rotation", err)
				}
				primary, err := incoming.ReadMCPReadinessPagerDutyAcknowledgementReceiver(ctx, f.rule.ID, "primary")
				if err != nil || primary.Version != 1 || !bytes.Equal(primary.SigningSecretSealed, bindings["primary"].SigningSecretSealed) {
					t.Fatal("fallback rotation changed primary", err)
				}
				if _, err := incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, MCPReadinessPagerDutyAcknowledgementConfig{Destination: "primary"}); err != nil {
					t.Fatal(err)
				}
				if _, err := incoming.ReadMCPReadinessPagerDutyAcknowledgementReceiver(ctx, f.rule.ID, "primary"); !errors.Is(err, ErrNotFound) {
					t.Fatal("disabled primary accepted", err)
				}
				event.EventID = "fallback-after-primary-disabled"
				event.OccurredAt = at.Add(5 * time.Second)
				receipt, err = incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 2, event, at.Add(6*time.Second))
				if err != nil || receipt.Outcome != "acknowledged" || receipt.Destination != "fallback" {
					t.Fatal("primary disable affected fallback", receipt, err)
				}
				if _, err := incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 2, MCPReadinessPagerDutyAcknowledgementConfig{Destination: "fallback"}); err != nil {
					t.Fatal(err)
				}
				policy, err := incoming.GetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, "fallback")
				if err != nil || policy.Enabled || policy.Version != 3 || policy.Destination != "fallback" {
					t.Fatal(policy, err)
				}
			})
		}
	}
}
