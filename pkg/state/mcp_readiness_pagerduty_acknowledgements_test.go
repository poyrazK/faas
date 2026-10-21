package state_test

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
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPagerDutyAcknowledgements(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		for _, region := range []string{"us", "eu"} {
			t.Run(region, func(t *testing.T) {
				f := state.NewAttentionFixture(t, s)
				ctx := t.Context()
				at := (*f.Now())
				native := s.(state.MCPReadinessPagerDutyStore)
				source := s.(state.MCPReadinessPagerDutyAcknowledgementStore)
				outbox := s.(state.MCPReadinessProviderOutboxStore)
				p, err := source.GetMCPReadinessPagerDutyAcknowledgements(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID)
				if err != nil || p.Enabled || p.Version != 0 || p.WebhookPath != "/v1/webhooks/mcp-readiness/pagerduty/"+(*f.Rule()).ID {
					t.Fatal(p, err)
				}
				primary := state.PagerDutyConfig("ack-key")
				primary.Region = region
				if _, err = native.SetMCPReadinessPagerDuty(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, primary); err != nil {
					t.Fatal(err)
				}
				config := state.MCPReadinessPagerDutyAcknowledgementConfig{Enabled: true, ExpectedPagerDutyVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("sealed-webhook-secret")}
				wrong := config
				wrong.ExpectedPagerDutyVersion = 2
				if _, err = source.SetMCPReadinessPagerDutyAcknowledgements(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, wrong); !errors.Is(err, state.ErrConflict) {
					t.Fatal("stale primary accepted", err)
				}
				p, err = source.SetMCPReadinessPagerDutyAcknowledgements(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, config)
				if err != nil || p.Version != 1 || p.Region != region || p.SigningSecretMasked != "***" || p.PagerDutyVersion != 1 {
					t.Fatal(p, err)
				}
				raw, _ := json.Marshal(p)
				if strings.Contains(string(raw), "sealed-webhook-secret") || strings.Contains(string(raw), primary.Fingerprint) {
					t.Fatal("private binding leaked")
				}
				if _, err = s.(state.MCPReadinessReminderDefaultsStore).SetMCPReadinessReminderDefaults(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, api.MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: 3}); err != nil {
					t.Fatal(err)
				}
				first := f.Observe(at, true, "")
				input := state.MCPReadinessPagerDutyAcknowledgementInput{EventID: "provider-event-1", IncidentID: first.IncidentID, ProviderIncidentID: "PINC001", ServiceID: config.ServiceID, Region: region, OccurredAt: at.Add(61 * time.Second)}
				apply := func(e state.MCPReadinessPagerDutyAcknowledgementInput, now time.Time) api.MCPReadinessPagerDutyAcknowledgementEvent {
					t.Helper()
					r, err := source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 1, e, now)
					if err != nil {
						t.Fatal(err)
					}
					return r
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 1, input, at.Add(62*time.Second)); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("unattempted incident acknowledged", err)
				}
				c := f.Claim(at, true)
				if err = outbox.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, state.MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				reminder := f.Observe(at.Add(time.Minute), true, "")
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
					if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 1, bad, at.Add(62*time.Second)); !errors.Is(err, state.ErrNotFound) {
						t.Fatal(kind, err)
					}
				}
				receipt := apply(input, at.Add(62*time.Second))
				if receipt.Outcome != "acknowledged" {
					t.Fatal(receipt)
				}
				view := f.View(first.IncidentID, at.Add(62*time.Second))
				if view.State != "acknowledged" || view.NextReminderAt != nil || view.Acknowledgement.Source != "pagerduty" || view.Acknowledgement.ProviderEventID != input.EventID {
					t.Fatal(view)
				}
				if event := f.Event(reminder.EventID); event.DeliveryStatus != "suppressed" || event.SuppressionReason != "acknowledged" {
					t.Fatal(event)
				}
				f.Claim(at.Add(63*time.Second), false)
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						r, err := source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 1, input, at.Add(64*time.Second))
						if err != nil || r.ID != receipt.ID || r.AttentionVersion != receipt.AttentionVersion || !r.ReceivedAt.Equal(receipt.ReceivedAt) {
							t.Error("duplicate changed receipt", r, err)
						}
					})
				}
				wg.Wait()
				bad := input
				bad.ProviderIncidentID = "OTHER"
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 1, bad, at.Add(64*time.Second)); !errors.Is(err, state.ErrConflict) {
					t.Fatal("event ID reused", err)
				}
				f.Act(first.IncidentID, f.Request(first.IncidentID, "release", at.Add(65*time.Second)), at.Add(65*time.Second))
				apply(input, at.Add(66*time.Second))
				if f.View(first.IncidentID, at.Add(66*time.Second)).State != "unacknowledged" {
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
				manual := f.Request(first.IncidentID, "acknowledge", at.Add(70*time.Second))
				expiry := at.Add(5 * time.Minute)
				manual.ExpiresAt = &expiry
				f.Act(first.IncidentID, manual, at.Add(70*time.Second))
				next.EventID = "provider-after-local"
				next.OccurredAt = at.Add(71 * time.Second)
				if r := apply(next, at.Add(72*time.Second)); r.Outcome != "already_acknowledged" {
					t.Fatal(r)
				}
				if v := f.View(first.IncidentID, at.Add(73*time.Second)); v.Acknowledgement.ExpiresAt == nil || !v.Acknowledgement.ExpiresAt.Equal(expiry) || v.Acknowledgement.Source != "" {
					t.Fatal("manual expiry changed", v)
				}
				// Recovery remains owed even when PagerDuty/local acknowledgement is active.
				f.Observe(at.Add(80*time.Second), false, "")
				c = f.Claim(at.Add(80*time.Second), true)
				if c.Provider != "pagerduty" || !strings.Contains(string(c.Delivery.Payload), "alert.resolved") {
					t.Fatal("acknowledgement suppressed recovery")
				}
				if err = outbox.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at.Add(80*time.Second), state.MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				next.EventID = "provider-resolved"
				next.OccurredAt = at.Add(81 * time.Second)
				if r := apply(next, at.Add(82*time.Second)); r.Outcome != "resolved" {
					t.Fatal(r)
				}
				all, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, "", 50)
				if err != nil || len(all.Events) != 5 {
					t.Fatal(all, err)
				}
				page, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, "", 1)
				if err != nil || page.NextBefore == "" {
					t.Fatal(page, err)
				}
				second, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, page.NextBefore, 1)
				if err != nil || len(second.Events) != 1 || second.Events[0].ID == page.Events[0].ID {
					t.Fatal(second, err)
				}
				history, err := s.(state.MCPReadinessAttentionStore).ListMCPReadinessAttentionHistory(ctx, (*f.Account()).ID, (*f.App()).ID, first.IncidentID, "", 50)
				if err != nil || len(history.Actions) != 4 {
					t.Fatal(history, err)
				}
				for _, h := range history.Actions {
					if h.Source == "pagerduty" && (h.ProviderEventID == "" || h.ProviderOccurredAt == nil) {
						t.Fatal("missing audit provenance", h)
					}
				}
				other := state.NewAttentionFixture(t, s)
				if _, err = source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, (*other.Account()).ID, (*other.App()).ID, (*f.Rule()).ID, "", 20); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("foreign history", err)
				}
				// An outgoing integration change does not retarget this existing binding.
				changed := state.PagerDutyConfig("new-key")
				changed.Region = "eu"
				if region == "eu" {
					changed.Region = "us"
				}
				if _, err = native.SetMCPReadinessPagerDuty(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 1, changed); err != nil {
					t.Fatal(err)
				}
				secondIncident := f.Observe(at.Add(90*time.Second), true, "")
				c = f.Claim(at.Add(90*time.Second), true)
				if err = outbox.CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at.Add(90*time.Second), state.MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
					t.Fatal(err)
				}
				newer := input
				newer.IncidentID = secondIncident.IncidentID
				newer.EventID = "new-integration"
				newer.Region = changed.Region
				newer.OccurredAt = at.Add(91 * time.Second)
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 1, newer, at.Add(92*time.Second)); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("binding followed integration", err)
				}
				config.ExpectedPagerDutyVersion = 2
				if _, err = source.SetMCPReadinessPagerDutyAcknowledgements(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 1, config); err != nil {
					t.Fatal(err)
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 1, newer, at.Add(92*time.Second)); !errors.Is(err, state.ErrConflict) {
					t.Fatal("stale verification accepted", err)
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 2, newer, at.Add(92*time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 2, input, at.Add(93*time.Second)); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("old incident rebound", err)
				}
				if _, err = source.SetMCPReadinessPagerDutyAcknowledgements(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 2, state.MCPReadinessPagerDutyAcknowledgementConfig{}); err != nil {
					t.Fatal(err)
				}
				if _, err = source.ReadMCPReadinessPagerDutyAcknowledgementReceiver(ctx, (*f.Rule()).ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("disabled secret available", err)
				}
				if _, err = source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 2, newer, at.Add(94*time.Second)); !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
					t.Fatal("revoked policy mutated incident", err)
				}
			})
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessPagerDutyAcknowledgementReceiptBound(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		f := state.NewAttentionFixture(t, s)
		ctx := t.Context()
		at := (*f.Now())
		if _, err := s.(state.MCPReadinessPagerDutyStore).SetMCPReadinessPagerDuty(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, state.PagerDutyConfig("bounded")); err != nil {
			t.Fatal(err)
		}
		source := s.(state.MCPReadinessPagerDutyAcknowledgementStore)
		config := state.MCPReadinessPagerDutyAcknowledgementConfig{Enabled: true, ExpectedPagerDutyVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("sealed")}
		if _, err := source.SetMCPReadinessPagerDutyAcknowledgements(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, config); err != nil {
			t.Fatal(err)
		}
		first := f.Observe(at, true, "")
		c := f.Claim(at, true)
		if err := s.(state.MCPReadinessProviderOutboxStore).CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, at, state.MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
			t.Fatal(err)
		}
		for n := range api.MCPReadinessPagerDutyWebhookEventsPerIncident + 1 {
			input := state.MCPReadinessPagerDutyAcknowledgementInput{EventID: fmt.Sprintf("event-%d", n), IncidentID: first.IncidentID, ProviderIncidentID: "PINC001", ServiceID: "PSVC001", Region: "us", OccurredAt: at.Add(time.Duration(n+1) * time.Second)}
			receipt, err := source.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, (*f.Rule()).ID, 1, input, at.Add(time.Duration(n+1)*time.Second))
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
			page, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, before, 50)
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
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
