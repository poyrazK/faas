package state

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessReminderDefaults(t *testing.T) {
	check := func(t *testing.T, s Store) {
		f := newAttentionFixture(t, s)
		source := s.(MCPReadinessReminderDefaultsStore)
		get := func() api.MCPReadinessReminderDefaults {
			t.Helper()
			v, e := source.GetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, f.rule.ID)
			if e != nil {
				t.Fatal(e)
			}
			return v
		}
		set := func(version int64, p api.MCPReadinessReminderPolicy) api.MCPReadinessReminderDefaults {
			t.Helper()
			v, e := source.SetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, f.rule.ID, version, p)
			if e != nil {
				t.Fatal(e)
			}
			return v
		}
		if v := get(); v.Version != 0 || v.ReminderPolicy.MaxReminders != 0 {
			t.Fatal(v)
		}
		for _, p := range []api.MCPReadinessReminderPolicy{{IntervalSeconds: 59, MaxReminders: 1}, {IntervalSeconds: 86401, MaxReminders: 1}, {IntervalSeconds: 60, MaxReminders: 11}, {IntervalSeconds: 60}, {MaxReminders: 1}} {
			if _, e := source.SetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 0, p); !errors.Is(e, ErrInvalidArgument) {
				t.Fatal(p, e)
			}
		}
		p := api.MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: 2}
		if v := set(0, p); v.Version != 1 {
			t.Fatal(v)
		}
		var wg sync.WaitGroup
		var wins atomic.Int32
		for range 8 {
			wg.Go(func() {
				_, e := source.SetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 1, p)
				if e == nil {
					wins.Add(1)
				} else if !errors.Is(e, ErrConflict) {
					t.Error(e)
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 || get().Version != 2 {
			t.Fatal("CAS", wins.Load(), get())
		}
		at := f.now
		first := f.observe(at, true, "")
		v := f.view(first.IncidentID, at)
		if v.ReminderPolicy != p || v.ReminderPolicySource != "rule_default" || v.ReminderDefaultsVersion == nil || *v.ReminderDefaultsVersion != 2 || v.Version != 0 || v.NextReminderAt == nil || !v.NextReminderAt.Equal(at.Add(time.Minute)) {
			t.Fatal(v)
		}
		// Change before first delivery: inheritance belongs to incident opening.
		set(2, api.MCPReadinessReminderPolicy{})
		v = f.view(first.IncidentID, at)
		if v.ReminderPolicy != p || *v.ReminderDefaultsVersion != 2 {
			t.Fatal("retroactive default", v)
		}
		f.complete(f.claim(at, true), at, AlertDeliveryDelivered)
		if event := f.observe(at.Add(time.Minute), true, ""); event.EventID == "" {
			t.Fatal("missing inherited reminder")
		}
		f.complete(f.claim(at.Add(time.Minute), true), at.Add(time.Minute), AlertDeliveryDelivered)
		// An override retains provenance and lifetime count.
		f.configure(first.IncidentID, at.Add(2*time.Minute), 0)
		v = f.view(first.IncidentID, at)
		if v.ReminderPolicySource != "incident_override" || *v.ReminderDefaultsVersion != 2 || v.RemindersCreated != 1 || v.ReminderPolicy.MaxReminders != 0 {
			t.Fatal(v)
		}
		f.configure(first.IncidentID, at.Add(3*time.Minute), 2)
		if v = f.view(first.IncidentID, at); v.RemindersCreated != 1 {
			t.Fatal("budget reset", v)
		}
		ack := f.request(first.IncidentID, "acknowledge", at.Add(3*time.Minute))
		f.act(first.IncidentID, ack, at.Add(3*time.Minute))
		if event := f.observe(at.Add(4*time.Minute), true, ""); event.EventID != "" {
			t.Fatal("acknowledgement bypassed", event)
		}
		f.observe(at.Add(5*time.Minute), false, "")
		f.complete(f.claim(at.Add(5*time.Minute), true), at.Add(5*time.Minute), AlertDeliveryDelivered)
		next := f.observe(at.Add(6*time.Minute), true, "")
		v = f.view(next.IncidentID, at.Add(6*time.Minute))
		if v.ReminderPolicy.MaxReminders != 0 || v.ReminderDefaultsVersion == nil || *v.ReminderDefaultsVersion != 3 || v.ReminderPolicySource != "rule_default" || v.RemindersCreated != 0 {
			t.Fatal("disabled inheritance", v)
		}
		// Enabling defaults never backfills the current incident.
		set(3, p)
		if v = f.view(next.IncidentID, at); v.ReminderPolicy.MaxReminders != 0 || *v.ReminderDefaultsVersion != 3 {
			t.Fatal(v)
		}
		other := newAttentionFixture(t, s)
		if _, e := source.GetMCPReadinessReminderDefaults(t.Context(), other.account.ID, other.app.ID, f.rule.ID); !errors.Is(e, ErrNotFound) {
			t.Fatal("ownership", e)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessReminderDefaultsUnconfigured(t *testing.T) {
	check := func(t *testing.T, s Store) {
		f := newAttentionFixture(t, s)
		first := f.observe(f.now, true, "")
		v := f.view(first.IncidentID, f.now)
		if v.ReminderPolicySource != "rule_default" || v.ReminderDefaultsVersion == nil || *v.ReminderDefaultsVersion != 0 || v.NextReminderAt != nil {
			t.Fatal(v)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessReminderDefaultsMaintenanceAndLegacy(t *testing.T) {
	check := func(t *testing.T, s Store) {
		f := newAttentionFixture(t, s)
		at := f.now
		source := s.(MCPReadinessReminderDefaultsStore)
		policy := api.MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: 2}
		if _, e := source.SetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 0, policy); e != nil {
			t.Fatal(e)
		}
		if _, e := s.(MCPReadinessMaintenanceStore).CreateMCPReadinessMaintenance(t.Context(), f.account.ID, f.app.ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", StartsAt: at, EndsAt: at.Add(3 * time.Minute), Reason: "Planned work"}, at); e != nil {
			t.Fatal(e)
		}
		first := f.observe(at, true, "")
		f.claim(at, false)
		if v := f.view(first.IncidentID, at); v.ReminderPolicy != policy || v.ReminderDefaultsVersion == nil || *v.ReminderDefaultsVersion != 1 {
			t.Fatal("opening during maintenance", v)
		}
		if _, e := source.SetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 1, api.MCPReadinessReminderPolicy{}); e != nil {
			t.Fatal(e)
		}
		continuation := f.observe(at.Add(3*time.Minute), true, "")
		if continuation.IncidentID != first.IncidentID || f.event(continuation.EventID).NotificationReason != "maintenance_ended" {
			t.Fatal(continuation)
		}
		f.complete(f.claim(at.Add(3*time.Minute), true), at.Add(3*time.Minute), AlertDeliveryDelivered)
		if e := f.observe(at.Add(3*time.Minute+30*time.Second), true, ""); e.EventID != "" {
			t.Fatal("early reminder", e)
		}
		if e := f.observe(at.Add(4*time.Minute), true, ""); e.EventID == "" {
			t.Fatal("inherited reminder lost")
		}
		f.complete(f.claim(at.Add(4*time.Minute), true), at.Add(4*time.Minute), AlertDeliveryDelivered)
		// Simulate a pre-upgrade attention snapshot without provenance.
		switch v := s.(type) {
		case *MemStore:
			v.mu.Lock()
			r := v.readinessAttention[first.IncidentID]
			r.PolicySource = ""
			r.DefaultsVersion = nil
			v.readinessAttention[first.IncidentID] = r
			v.mu.Unlock()
		case *PgStore:
			if _, e := v.pool.Exec(t.Context(), `UPDATE mcp_readiness_incident_attention SET snapshot=snapshot-'policy_source'-'defaults_version' WHERE incident_id=$1`, first.IncidentID); e != nil {
				t.Fatal(e)
			}
		}
		f.observe(at.Add(4*time.Minute+30*time.Second), true, "")
		if v := f.view(first.IncidentID, at); v.ReminderPolicySource != "legacy" || v.ReminderDefaultsVersion != nil || v.ReminderPolicy != policy || v.RemindersCreated != 1 {
			t.Fatal("legacy backfilled", v)
		}
		f.configure(first.IncidentID, at.Add(4*time.Minute+40*time.Second), 0)
		if v := f.view(first.IncidentID, at); v.ReminderPolicySource != "incident_override" || v.ReminderDefaultsVersion != nil || v.RemindersCreated != 1 {
			t.Fatal("legacy override", v)
		}
		enabled := false
		if _, e := s.UpdateAlertRule(t.Context(), f.rule.ID, UpdateAlertRuleParams{Enabled: &enabled}); e != nil {
			t.Fatal(e)
		}
		if _, e := source.SetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, f.rule.ID, 2, policy); e != nil {
			t.Fatal("disabled config", e)
		}
		ordinary, e := s.CreateAlertRule(t.Context(), AlertRule{AccountID: f.account.ID, AppID: f.app.ID, Name: "Ordinary alert", Metric: AlertMetricErrorRate, Comparison: AlertGt, WindowSpec: AlertWindow5m, Action: AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("fixture"), CooldownMinutes: 5})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = source.GetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, ordinary.ID); !errors.Is(e, ErrInvalidArgument) {
			t.Fatal("ordinary rule", e)
		}
		if _, e = source.SetMCPReadinessReminderDefaults(t.Context(), f.account.ID, f.app.ID, ordinary.ID, 0, policy); !errors.Is(e, ErrInvalidArgument) {
			t.Fatal("ordinary rule mutation", e)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
