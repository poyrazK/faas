package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type pagerDutyReleaseFixture struct {
	*attentionFixture
	incoming MCPReadinessPagerDutyAcknowledgementStore
	incident string
	configs  map[string]MCPReadinessPagerDutyAcknowledgementConfig
}

func newPagerDutyReleaseFixture(t *testing.T, backend, region string) *pagerDutyReleaseFixture {
	var s Store = NewMemStore()
	if backend == "postgres" {
		s = NewPgStore(pgtest.OpenMigrated(t))
	}
	f := newAttentionFixture(t, s)
	ctx := t.Context()
	primary := pagerDutyConfig("release-primary")
	primary.Region = region
	if _, err := s.(MCPReadinessPagerDutyStore).SetMCPReadinessPagerDuty(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, primary); err != nil {
		t.Fatal(err)
	}
	if _, err := s.(MCPReadinessFallbackStore).SetMCPReadinessFallback(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, nativeFallbackConfig("release-fallback", region, 1)); err != nil {
		t.Fatal(err)
	}
	r := &pagerDutyReleaseFixture{attentionFixture: f, incoming: s.(MCPReadinessPagerDutyAcknowledgementStore), configs: map[string]MCPReadinessPagerDutyAcknowledgementConfig{
		"primary":  {Destination: "primary", Enabled: true, SyncUnacknowledgements: true, ExpectedPagerDutyVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("primary-webhook")},
		"fallback": {Destination: "fallback", Enabled: true, SyncUnacknowledgements: true, ExpectedFallbackVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: []byte("fallback-webhook")},
	}}
	for _, d := range []string{"primary", "fallback"} {
		p, err := r.incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 0, r.configs[d])
		if err != nil || !p.SyncUnacknowledgements {
			t.Fatal(p, err)
		}
	}
	binding := r.configs["primary"]
	binding.Region = region
	r.configs["primary"] = binding
	r.incident = f.observe(f.now, true, "").IncidentID
	f.configure(r.incident, f.now.Add(time.Second), 3)
	c := f.claim(f.now, true)
	if err := s.(MCPReadinessProviderOutboxStore).CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, f.now, MCPReadinessProviderResult{StatusCode: 503, Failure: "provider_rejected"}); err != nil {
		t.Fatal(err)
	}
	c = f.claim(f.now, true)
	if c.Delivery.ReadinessDestination != "fallback" {
		t.Fatal(c.Delivery.ReadinessDestination)
	}
	if err := s.(MCPReadinessProviderOutboxStore).CompleteMCPReadinessProviderDelivery(ctx, c.EventID, c.Token, f.now, MCPReadinessProviderResult{StatusCode: 202, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *pagerDutyReleaseFixture) input(destination, event, provider, action string, occur int) MCPReadinessPagerDutyAcknowledgementInput {
	f.t.Helper()
	region := f.configs["primary"].Region
	if region == "" {
		region = "us"
	}
	return MCPReadinessPagerDutyAcknowledgementInput{BindingDestination: destination, EventID: event, IncidentID: f.incident, ProviderIncidentID: provider, ServiceID: "PSVC001", Region: region, OccurredAt: f.now.Add(time.Duration(occur) * time.Second), Action: action}
}
func (f *pagerDutyReleaseFixture) apply(e MCPReadinessPagerDutyAcknowledgementInput, version int64, received int, outcome string) api.MCPReadinessPagerDutyAcknowledgementEvent {
	f.t.Helper()
	r, err := f.incoming.ApplyMCPReadinessPagerDutyAcknowledgement(f.t.Context(), f.rule.ID, version, e, f.now.Add(time.Duration(received)*time.Second))
	if err != nil || r.Outcome != outcome {
		f.t.Fatal("provider decision", e.EventID, r, err)
	}
	return r
}
func TestMCPReadinessPagerDutyReleaseLifecycle(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, region := range []string{"us", "eu"} {
			t.Run(backend+"/"+region, func(t *testing.T) {
				f := newPagerDutyReleaseFixture(t, backend, region)
				f.apply(f.input("primary", "ack-primary", "PPRIMARY", "", 2), 1, 5, "acknowledged")
				// A different destination is ordered by provider time, even if received after primary.
				f.apply(f.input("fallback", "ack-fallback", "PFALLBACK", "", 3), 1, 6, "already_acknowledged")
				if pg, ok := f.store.(*PgStore); ok {
					f.incoming = NewPgStore(pg.pool.Pool)
				}
				p := f.apply(f.input("primary", "release-primary", "PPRIMARY", "release", 4), 1, 7, "retained")
				v := f.view(f.incident, f.now.Add(7*time.Second))
				if v.State != "acknowledged" || len(v.ProviderAcknowledgements) != 1 || v.ProviderAcknowledgements[0].Destination != "fallback" || v.NextReminderAt != nil || v.Acknowledgement.ProviderIncidentID != "PFALLBACK" {
					t.Fatal(v)
				}
				f.apply(f.input("fallback", "old-fallback", "PFALLBACK", "", 2), 1, 8, "stale")
				f.apply(f.input("fallback", "wrong-provider", "OTHER", "release", 5), 1, 8, "retained")
				released := f.apply(f.input("fallback", "release-fallback", "PFALLBACK", "release", 6), 1, 9, "released")
				v = f.view(f.incident, f.now.Add(9*time.Second))
				if v.Acknowledgement != nil || v.ResolvedAt != nil || len(v.ProviderAcknowledgements) != 0 || v.NextReminderAt == nil || !v.NextReminderAt.Equal(f.now.Add(69*time.Second)) || v.RemindersCreated != 0 {
					t.Fatal(v)
				}
				again := f.apply(f.input("primary", "release-primary", "PPRIMARY", "release", 4), 1, 10, "retained")
				if again.ID != p.ID || !again.ReceivedAt.Equal(p.ReceivedAt) {
					t.Fatal("release replay changed its receipt", again, p)
				}
				again = f.apply(f.input("fallback", "release-fallback", "PFALLBACK", "release", 6), 1, 10, "released")
				if again.ID != released.ID || f.view(f.incident, f.now.Add(10*time.Second)).Version != v.Version {
					t.Fatal("duplicate release reapplied")
				}
				f.observe(f.now.Add(68*time.Second), true, "")
				if f.view(f.incident, f.now.Add(68*time.Second)).RemindersCreated != 0 {
					t.Fatal("immediate reminder after release")
				}
				f.observe(f.now.Add(69*time.Second), true, "")
				if f.view(f.incident, f.now.Add(69*time.Second)).RemindersCreated != 1 {
					t.Fatal("reminders did not resume at interval")
				}
			})
		}
	}
}
func TestMCPReadinessPagerDutyReleaseOrderingAndManualOverride(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newPagerDutyReleaseFixture(t, backend, "us")
			f.apply(f.input("primary", "release-before-ack", "PPRIMARY", "release", 10), 1, 12, "not_acknowledged")
			f.apply(f.input("primary", "delayed-ack", "PPRIMARY", "", 9), 1, 13, "stale")
			f.apply(f.input("primary", "equal-ack", "PPRIMARY", "", 10), 1, 14, "acknowledged")
			f.apply(f.input("primary", "equal-release", "PPRIMARY", "release", 10), 1, 15, "stale")
			manual := f.request(f.incident, "acknowledge", f.now.Add(16*time.Second))
			expiry := f.now.Add(20 * time.Second)
			manual.ExpiresAt = &expiry
			f.act(f.incident, manual, f.now.Add(16*time.Second))
			f.apply(f.input("primary", "manual-protected", "PPRIMARY", "release", 17), 1, 18, "retained")
			v := f.view(f.incident, f.now.Add(18*time.Second))
			if v.Acknowledgement == nil || v.Acknowledgement.Source != "" || !v.Acknowledgement.ExpiresAt.Equal(expiry) || len(v.ProviderAcknowledgements) != 0 {
				t.Fatal(v)
			}
			// An unacknowledgement after local expiry still fences a delayed provider acknowledgement.
			f.apply(f.input("primary", "expired-release", "PPRIMARY", "release", 22), 1, 23, "not_acknowledged")
			f.apply(f.input("primary", "expired-delayed-ack", "PPRIMARY", "", 21), 1, 24, "stale")
			f.apply(f.input("primary", "fresh-ack", "PPRIMARY", "", 25), 1, 26, "acknowledged")
			f.act(f.incident, f.request(f.incident, "release", f.now.Add(27*time.Second)), f.now.Add(27*time.Second))
			f.apply(f.input("primary", "before-local-release", "PPRIMARY", "", 26), 1, 28, "stale")
			f.apply(f.input("primary", "fresh-after-local", "PPRIMARY", "", 29), 1, 30, "acknowledged")
		})
	}
}
func TestMCPReadinessPagerDutyReleaseOptInAndAmbiguousOwnership(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newPagerDutyReleaseFixture(t, backend, "us")
			ctx := t.Context()
			cfg := f.configs["fallback"]
			cfg.SyncUnacknowledgements = false
			if _, err := f.incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 1, cfg); err != nil {
				t.Fatal(err)
			}
			f.apply(f.input("primary", "primary-ack", "PPRIMARY", "", 2), 1, 3, "acknowledged")
			f.apply(f.input("fallback", "fallback-ack-off", "PFALLBACK", "", 4), 2, 5, "already_acknowledged")
			f.apply(f.input("primary", "primary-release", "PPRIMARY", "release", 6), 1, 7, "retained")
			r, err := f.incoming.ApplyMCPReadinessPagerDutyAcknowledgement(ctx, f.rule.ID, 2, f.input("fallback", "disabled-release", "PFALLBACK", "release", 8), f.now.Add(9*time.Second))
			if err != nil || r.ID != "" || f.view(f.incident, f.now.Add(9*time.Second)).State != "acknowledged" {
				t.Fatal(r, err)
			}
			cfg.SyncUnacknowledgements = true
			if _, err = f.incoming.SetMCPReadinessPagerDutyAcknowledgements(ctx, f.account.ID, f.app.ID, f.rule.ID, 2, cfg); err != nil {
				t.Fatal(err)
			}
			f.apply(f.input("fallback", "no-retroactive-release", "PFALLBACK", "release", 10), 3, 11, "retained")
			f.apply(f.input("fallback", "new-opt-in-ack", "PFALLBACK", "", 12), 3, 13, "already_acknowledged")
			f.apply(f.input("fallback", "new-opt-in-release", "PFALLBACK", "release", 14), 3, 15, "released")
			// Multiple provider incidents for one destination require manual review and release.
			f.apply(f.input("primary", "one-provider", "PPRIMARY", "", 16), 1, 17, "acknowledged")
			// Equal-time acknowledgements from different incidents are ambiguous too.
			f.apply(f.input("primary", "equal-other-provider", "POTHER", "", 16), 1, 18, "already_acknowledged")
			f.apply(f.input("primary", "another-provider", "POTHER", "", 18), 1, 19, "already_acknowledged")
			f.apply(f.input("primary", "ambiguous-release", "PPRIMARY", "release", 20), 1, 21, "retained")
			v := f.view(f.incident, f.now.Add(21*time.Second))
			if len(v.ProviderAcknowledgements) != 1 || v.ProviderAcknowledgements[0].ReleaseSyncEnabled {
				t.Fatal(v)
			}
		})
	}
}
func TestMCPReadinessPagerDutyReleaseSharedEventAndBudget(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newPagerDutyReleaseFixture(t, backend, "us")
			f.configure(f.incident, f.now.Add(time.Second), 1)
			f.observe(f.now.Add(61*time.Second), true, "")
			if f.view(f.incident, f.now.Add(61*time.Second)).RemindersCreated != 1 {
				t.Fatal("fixture budget not consumed")
			}
			a := f.apply(f.input("primary", "shared-ack", "PSHARED", "", 62), 1, 63, "acknowledged")
			b := f.apply(f.input("fallback", "shared-ack", "PSHARED", "", 62), 1, 64, "acknowledged")
			if a.ID != b.ID {
				t.Fatal("shared acknowledgement was reapplied")
			}
			first := f.apply(f.input("fallback", "shared-release", "PSHARED", "release", 65), 1, 66, "retained")
			second := f.apply(f.input("primary", "shared-release", "PSHARED", "release", 65), 1, 67, "released")
			if first.ID == second.ID {
				t.Fatal("release destinations shared a dedup slot")
			}
			f.apply(f.input("fallback", "shared-release", "PSHARED", "release", 65), 1, 68, "retained")
			bad := f.input("fallback", "shared-release", "OTHER", "release", 65)
			if _, err := f.incoming.ApplyMCPReadinessPagerDutyAcknowledgement(t.Context(), f.rule.ID, 1, bad, f.now.Add(69*time.Second)); !errors.Is(err, ErrConflict) {
				t.Fatal("conflicting release event accepted", err)
			}
			v := f.view(f.incident, f.now.Add(69*time.Second))
			if v.NextReminderAt != nil || v.RemindersCreated != 1 {
				t.Fatal("release reset reminder budget", v)
			}
			f.observe(f.now.Add(130*time.Second), true, "")
			if f.view(f.incident, f.now.Add(130*time.Second)).RemindersCreated != 1 {
				t.Fatal("release extended reminder budget")
			}
		})
	}
}
