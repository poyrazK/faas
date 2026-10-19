package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPagerDutyAcknowledgements(t *testing.T) {
	check := func(t *testing.T, s Store) {
		for _, region := range []string{"us", "eu"} {
			t.Run(region, func(t *testing.T) {
				f := newAttentionFixture(t, s)
				ctx := t.Context()
				at := f.now
				native := s.(MCPReadinessPagerDutyStore)
				source := s.(MCPReadinessPagerDutyAcknowledgementStore)
				outbox := s.(MCPReadinessProviderOutboxStore)
				p, err := source.GetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID)
				if err != nil || p.Enabled || p.Version != 0 || p.WebhookPath != "/v1/webhooks/mcp-readiness/pagerduty/"+f.rule.ID {
					t.Fatal(p, err)
				}
				primary := pagerDutyConfig("ack-key")
				primary.Region = region
				if _, err = native.SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, primary); err != nil {
					t.Fatal(err)
				}
				config := MCPReadinessPagerDutyAcknowledgementConfig{Enabled: true, ExpectedPagerDutyVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("sealed-webhook-secret")}
				wrong := config
				wrong.ExpectedPagerDutyVersion = 2
				if _, err = source.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, wrong); !errors.Is(err, ErrConflict) {
					t.Fatal("stale primary accepted", err)
				}
				p, err = source.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, config)
				if err != nil || p.Version != 1 || p.Region != region || p.SigningSecretMasked != "***" || p.PagerDutyVersion != 1 {
					t.Fatal(p, err)
				}
				raw, _ := json.Marshal(p)
				if strings.Contains(string(raw), "sealed-webhook-secret") || strings.Contains(string(raw), primary.Fingerprint) {
					t.Fatal("private binding leaked")
				}
				if _, err = s.(MCPReadinessReminderDefaultsStore).SetMCPReadinessReminderDefaults(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, api.MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: 3}); err != nil {
					t.Fatal(err)
				}
				first := f.observe(at, true, "")
				input := MCPReadinessPagerDutyAcknowledgementInput{EventID: "provider-event-1", IncidentID: first.IncidentID, ProviderIncidentID: "PINC001", ServiceID: config.ServiceID, Region: region, OccurredAt: at.Add(61 * time.Second)}
				apply := func(e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) api.MCPReadinessPagerDutyAcknowledgementEvent {
					t.Helper()
					r, err := source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, e, now)
					if err != nil {
						t.Fatal(err)
					}
					return r
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, input, at.Add(62*time.Second)); !errors.Is(err, ErrNotFound) {
					t.Fatal("unattempted incident acknowledged", err)
				}
				c := f.claim(at, true)
				if err = outbox.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				reminder := f.observe(at.Add(time.Minute), true, "")
				if reminder.EventID == "" {
					t.Fatal("missing queued reminder")
				}
				for _, kind := range []string{"service", "region"} {
					bad := input
					if kind == "service" {
						bad.ServiceID = "OTHER"
					} else {
						bad.Region = "eu"
						if region == "eu" {
							bad.Region = "us"
						}
					}
					if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, bad, at.Add(62*time.Second)); !errors.Is(err, ErrNotFound) {
						t.Fatal(kind, err)
					}
				}
				receipt := apply(input, at.Add(62*time.Second))
				if receipt.Outcome != "acknowledged" {
					t.Fatal(receipt)
				}
				view := f.view(first.IncidentID, at.Add(62*time.Second))
				if view.State != "acknowledged" || view.NextReminderAt != nil || view.Acknowledgement.Source != "pagerduty" || view.Acknowledgement.ProviderEventID != input.EventID {
					t.Fatal(view)
				}
				if event := f.event(reminder.EventID); event.DeliveryStatus != "suppressed" || event.SuppressionReason != "acknowledged" {
					t.Fatal(event)
				}
				f.claim(at.Add(63*time.Second), false)
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						r, err := source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, input, at.Add(64*time.Second))
						if err != nil || r.ID != receipt.ID || r.AttentionVersion != receipt.AttentionVersion || !r.ReceivedAt.Equal(receipt.ReceivedAt) {
							t.Error("duplicate changed receipt", r, err)
						}
					})
				}
				wg.Wait()
				bad := input
				bad.ProviderIncidentID = "OTHER"
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, bad, at.Add(64*time.Second)); !errors.Is(err, ErrConflict) {
					t.Fatal("event ID reused", err)
				}
				f.act(first.IncidentID, f.request(first.IncidentID, "release", at.Add(65*time.Second)), at.Add(65*time.Second))
				apply(input, at.Add(66*time.Second))
				if f.view(first.IncidentID, at.Add(66*time.Second)).State != "unacknowledged" {
					t.Fatal("duplicate undid manual release")
				}
				stale := input
				stale.EventID = "provider-stale"
				stale.OccurredAt = at.Add(64 * time.Second)
				if r := apply(stale, at.Add(67*time.Second)); r.Outcome != "stale" {
					t.Fatal(r)
				}
				next := input
				next.EventID = "provider-event-2"
				next.OccurredAt = at.Add(68 * time.Second)
				if r := apply(next, at.Add(69*time.Second)); r.Outcome != "acknowledged" {
					t.Fatal(r)
				}
				// Local acknowledgement expiry is preserved instead of becoming indefinite.
				manual := f.request(first.IncidentID, "acknowledge", at.Add(70*time.Second))
				expiry := at.Add(5 * time.Minute)
				manual.ExpiresAt = &expiry
				f.act(first.IncidentID, manual, at.Add(70*time.Second))
				next.EventID = "provider-after-local"
				next.OccurredAt = at.Add(71 * time.Second)
				if r := apply(next, at.Add(72*time.Second)); r.Outcome != "already_acknowledged" {
					t.Fatal(r)
				}
				if v := f.view(first.IncidentID, at.Add(73*time.Second)); v.Acknowledgement.ExpiresAt == nil || !v.Acknowledgement.ExpiresAt.Equal(expiry) || v.Acknowledgement.Source != "" {
					t.Fatal("manual expiry changed", v)
				}
				// Recovery remains owed even when PagerDuty/local acknowledgement is active.
				f.observe(at.Add(80*time.Second), false, "")
				c = f.claim(at.Add(80*time.Second), true)
				if c.Provider != "pagerduty" || !strings.Contains(string(c.Delivery.Payload), "alert.resolved") {
					t.Fatal("acknowledgement suppressed recovery")
				}
				if err = outbox.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at.Add(80*time.Second), MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				next.EventID = "provider-resolved"
				next.OccurredAt = at.Add(81 * time.Second)
				if r := apply(next, at.Add(82*time.Second)); r.Outcome != "resolved" {
					t.Fatal(r)
				}
				all, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, f.account.ID, f.app.ID, f.rule.ID, "", 50)
				if err != nil || len(all.Events) != 5 {
					t.Fatal(all, err)
				}
				page, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, f.account.ID, f.app.ID, f.rule.ID, "", 1)
				if err != nil || page.NextBefore == "" {
					t.Fatal(page, err)
				}
				second, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, f.account.ID, f.app.ID, f.rule.ID, page.NextBefore, 1)
				if err != nil || len(second.Events) != 1 || second.Events[0].ID == page.Events[0].ID {
					t.Fatal(second, err)
				}
				history, err := s.(MCPReadinessAttentionStore).ListMCPReadinessAttentionHistory(ctx, f.account.ID, f.app.ID, first.IncidentID, "", 50)
				if err != nil || len(history.Actions) != 4 {
					t.Fatal(history, err)
				}
				for _, h := range history.Actions {
					if h.Source == "pagerduty" && (h.ProviderEventID == "" || h.ProviderOccurredAt == nil) {
						t.Fatal("missing audit provenance", h)
					}
				}
				other := newAttentionFixture(t, s)
				if _, err = source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, other.account.ID, other.app.ID, f.rule.ID, "", 20); !errors.Is(err, ErrNotFound) {
					t.Fatal("foreign history", err)
				}
				// An outgoing integration change does not retarget this existing binding.
				changed := pagerDutyConfig("new-key")
				changed.Region = "eu"
				if region == "eu" {
					changed.Region = "us"
				}
				if _, err = native.SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, changed); err != nil {
					t.Fatal(err)
				}
				secondIncident := f.observe(at.Add(90*time.Second), true, "")
				c = f.claim(at.Add(90*time.Second), true)
				if err = outbox.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at.Add(90*time.Second), MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				newer := input
				newer.IncidentID = secondIncident.IncidentID
				newer.EventID = "new-integration"
				newer.Region = changed.Region
				newer.OccurredAt = at.Add(91 * time.Second)
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, newer, at.Add(92*time.Second)); !errors.Is(err, ErrNotFound) {
					t.Fatal("binding followed integration", err)
				}
				config.ExpectedPagerDutyVersion = 2
				if _, err = source.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, config); err != nil {
					t.Fatal(err)
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, newer, at.Add(92*time.Second)); !errors.Is(err, ErrConflict) {
					t.Fatal("stale verification accepted", err)
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 2, newer, at.Add(92*time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 2, input, at.Add(93*time.Second)); !errors.Is(err, ErrNotFound) {
					t.Fatal("old incident rebound", err)
				}
				if _, err = source.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 2, MCPReadinessPagerDutyAcknowledgementConfig{}); err != nil {
					t.Fatal(err)
				}
				if _, err = source.ReadMCPReadinessPagerDutyAcknowledgementReceiver(ctx, f.rule.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal("disabled secret available", err)
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 2, newer, at.Add(94*time.Second)); !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
					t.Fatal("revoked policy mutated incident", err)
				}
			})
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessPagerDutyAcknowledgementReceiptBound(t *testing.T) {
	check := func(t *testing.T, s Store) {
		f := newAttentionFixture(t, s)
		ctx := t.Context()
		at := f.now
		if _, err := s.(MCPReadinessPagerDutyStore).SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, pagerDutyConfig("bounded")); err != nil {
			t.Fatal(err)
		}
		source := s.(MCPReadinessPagerDutyAcknowledgementStore)
		config := MCPReadinessPagerDutyAcknowledgementConfig{Enabled: true, ExpectedPagerDutyVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("sealed")}
		if _, err := source.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, config); err != nil {
			t.Fatal(err)
		}
		first := f.observe(at, true, "")
		c := f.claim(at, true)
		if err := s.(MCPReadinessProviderOutboxStore).CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
			t.Fatal(err)
		}
		for n := range api.MCPReadinessPagerDutyWebhookEventsPerIncident + 1 {
			input := MCPReadinessPagerDutyAcknowledgementInput{EventID: fmt.Sprintf("event-%d", n), IncidentID: first.IncidentID, ProviderIncidentID: "PINC001", ServiceID: "PSVC001", Region: "us", OccurredAt: at.Add(time.Duration(n+1) * time.Second)}
			receipt, err := source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 1, input, at.Add(time.Duration(n+1)*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if n == api.MCPReadinessPagerDutyWebhookEventsPerIncident && receipt.ID != "" {
				t.Fatal("event limit exceeded", receipt)
			}
		}
		count := 0
		before := ""
		for {
			page, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, f.account.ID, f.app.ID, f.rule.ID, before, 50)
			if err != nil {
				t.Fatal(err)
			}
			count += len(page.Events)
			if page.NextBefore == "" {
				break
			}
			before = page.NextBefore
		}
		if count != api.MCPReadinessPagerDutyWebhookEventsPerIncident {
			t.Fatal(count)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
