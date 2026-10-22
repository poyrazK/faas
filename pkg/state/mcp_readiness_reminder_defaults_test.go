package state_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessReminderDefaults(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		f := state.NewAttentionFixture(t, s)
		source := s.(state.MCPReadinessReminderDefaultsStore)
		get := func() api.MCPReadinessReminderDefaults {
			t.Helper()
			v, e := source.GetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID)
			if e != nil {
				t.Fatal(e)
			}
			return v
		}
		set := func(version int64, p api.MCPReadinessReminderPolicy) api.MCPReadinessReminderDefaults {
			t.Helper()
			v, e := source.SetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, version, p)
			if e != nil {
				t.Fatal(e)
			}
			return v
		}
		if v := get(); v.Version != 0 || v.ReminderPolicy.MaxReminders != 0 {
			t.Fatal(v)
		}
		for _, p := range []api.MCPReadinessReminderPolicy{{IntervalSeconds: 59, MaxReminders: 1}, {IntervalSeconds: 86401, MaxReminders: 1}, {IntervalSeconds: 60, MaxReminders: 11}, {IntervalSeconds: 60}, {MaxReminders: 1}} {
			if _, e := source.SetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, p); !errors.Is(e, state.ErrInvalidArgument) {
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
				_, e := source.SetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 1, p)
				if e == nil {
					wins.Add(1)
				} else if !errors.Is(e, state.ErrConflict) {
					t.Error(e)
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 || get().Version != 2 {
			t.Fatal("CAS", wins.Load(), get())
		}
		at := (*f.Now())
		first := f.Observe(at, true, "")
		v := f.View(first.IncidentID, at)
		if v.ReminderPolicy != p || v.ReminderPolicySource != "rule_default" || v.ReminderDefaultsVersion == nil || *v.ReminderDefaultsVersion != 2 || v.Version != 0 || v.NextReminderAt == nil || !v.NextReminderAt.Equal(at.Add(time.Minute)) {
			t.Fatal(v)
		}
		// Change before first delivery: inheritance belongs to incident opening.
		set(2, api.MCPReadinessReminderPolicy{})
		v = f.View(first.IncidentID, at)
		if v.ReminderPolicy != p || *v.ReminderDefaultsVersion != 2 {
			t.Fatal("retroactive default", v)
		}
		f.Complete(f.Claim(at, true), at, state.AlertDeliveryDelivered)
		if event := f.Observe(at.Add(time.Minute), true, ""); event.EventID == "" {
			t.Fatal("missing inherited reminder")
		}
		f.Complete(f.Claim(at.Add(time.Minute), true), at.Add(time.Minute), state.AlertDeliveryDelivered)
		// An override retains provenance and lifetime count.
		f.Configure(first.IncidentID, at.Add(2*time.Minute), 0)
		v = f.View(first.IncidentID, at)
		if v.ReminderPolicySource != "incident_override" || *v.ReminderDefaultsVersion != 2 || v.RemindersCreated != 1 || v.ReminderPolicy.MaxReminders != 0 {
			t.Fatal(v)
		}
		f.Configure(first.IncidentID, at.Add(3*time.Minute), 2)
		if v = f.View(first.IncidentID, at); v.RemindersCreated != 1 {
			t.Fatal("budget reset", v)
		}
		ack := f.Request(first.IncidentID, "acknowledge", at.Add(3*time.Minute))
		f.Act(first.IncidentID, ack, at.Add(3*time.Minute))
		if event := f.Observe(at.Add(4*time.Minute), true, ""); event.EventID != "" {
			t.Fatal("acknowledgement bypassed", event)
		}
		f.Observe(at.Add(5*time.Minute), false, "")
		f.Complete(f.Claim(at.Add(5*time.Minute), true), at.Add(5*time.Minute), state.AlertDeliveryDelivered)
		next := f.Observe(at.Add(6*time.Minute), true, "")
		v = f.View(next.IncidentID, at.Add(6*time.Minute))
		if v.ReminderPolicy.MaxReminders != 0 || v.ReminderDefaultsVersion == nil || *v.ReminderDefaultsVersion != 3 || v.ReminderPolicySource != "rule_default" || v.RemindersCreated != 0 {
			t.Fatal("disabled inheritance", v)
		}
		// Enabling defaults never backfills the current incident.
		set(3, p)
		if v = f.View(next.IncidentID, at); v.ReminderPolicy.MaxReminders != 0 || *v.ReminderDefaultsVersion != 3 {
			t.Fatal(v)
		}
		other := state.NewAttentionFixture(t, s)
		if _, e := source.GetMCPReadinessReminderDefaults(t.Context(), (*other.Account()).ID, (*other.App()).ID, (*f.Rule()).ID); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("ownership", e)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessReminderDefaultsUnconfigured(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		f := state.NewAttentionFixture(t, s)
		first := f.Observe((*f.Now()), true, "")
		v := f.View(first.IncidentID, (*f.Now()))
		if v.ReminderPolicySource != "rule_default" || v.ReminderDefaultsVersion == nil || *v.ReminderDefaultsVersion != 0 || v.NextReminderAt != nil {
			t.Fatal(v)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessReminderDefaultsMaintenanceAndLegacy(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		f := state.NewAttentionFixture(t, s)
		at := (*f.Now())
		source := s.(state.MCPReadinessReminderDefaultsStore)
		policy := api.MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: 2}
		if _, e := source.SetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 0, policy); e != nil {
			t.Fatal(e)
		}
		if _, e := s.(state.MCPReadinessMaintenanceStore).CreateMCPReadinessMaintenance(t.Context(), (*f.Account()).ID, (*f.App()).ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", StartsAt: at, EndsAt: at.Add(3 * time.Minute), Reason: "Planned work"}, at); e != nil {
			t.Fatal(e)
		}
		first := f.Observe(at, true, "")
		f.Claim(at, false)
		if v := f.View(first.IncidentID, at); v.ReminderPolicy != policy || v.ReminderDefaultsVersion == nil || *v.ReminderDefaultsVersion != 1 {
			t.Fatal("opening during maintenance", v)
		}
		if _, e := source.SetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 1, api.MCPReadinessReminderPolicy{}); e != nil {
			t.Fatal(e)
		}
		continuation := f.Observe(at.Add(3*time.Minute), true, "")
		if continuation.IncidentID != first.IncidentID || f.Event(continuation.EventID).NotificationReason != "maintenance_ended" {
			t.Fatal(continuation)
		}
		f.Complete(f.Claim(at.Add(3*time.Minute), true), at.Add(3*time.Minute), state.AlertDeliveryDelivered)
		if e := f.Observe(at.Add(3*time.Minute+30*time.Second), true, ""); e.EventID != "" {
			t.Fatal("early reminder", e)
		}
		if e := f.Observe(at.Add(4*time.Minute), true, ""); e.EventID == "" {
			t.Fatal("inherited reminder lost")
		}
		f.Complete(f.Claim(at.Add(4*time.Minute), true), at.Add(4*time.Minute), state.AlertDeliveryDelivered)
		// Simulate a pre-upgrade attention snapshot without provenance.
		switch v := s.(type) {
		case *state.MemStore:
			(*v.Mu()).Lock()
			r := (*v.ReadinessAttention())[first.IncidentID]
			r.PolicySource = ""
			r.DefaultsVersion = nil
			(*v.ReadinessAttention())[first.IncidentID] = r
			(*v.Mu()).Unlock()
		case *state.PgStore:
			if _, e := (*v.Pool()).Exec(t.Context(), `UPDATE mcp_readiness_incident_attention SET snapshot=snapshot-'policy_source'-'defaults_version' WHERE incident_id=$1`, first.IncidentID); e != nil {
				t.Fatal(e)
			}
		}
		f.Observe(at.Add(4*time.Minute+30*time.Second), true, "")
		if v := f.View(first.IncidentID, at); v.ReminderPolicySource != "legacy" || v.ReminderDefaultsVersion != nil || v.ReminderPolicy != policy || v.RemindersCreated != 1 {
			t.Fatal("legacy backfilled", v)
		}
		f.Configure(first.IncidentID, at.Add(4*time.Minute+40*time.Second), 0)
		if v := f.View(first.IncidentID, at); v.ReminderPolicySource != "incident_override" || v.ReminderDefaultsVersion != nil || v.RemindersCreated != 1 {
			t.Fatal("legacy override", v)
		}
		enabled := false
		if _, e := s.UpdateAlertRule(t.Context(), (*f.Rule()).ID, state.UpdateAlertRuleParams{Enabled: &enabled}); e != nil {
			t.Fatal(e)
		}
		if _, e := source.SetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, (*f.Rule()).ID, 2, policy); e != nil {
			t.Fatal("disabled config", e)
		}
		ordinary, e := s.CreateAlertRule(t.Context(), state.AlertRule{AccountID: (*f.Account()).ID, AppID: (*f.App()).ID, Name: "Ordinary alert", Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("fixture"), CooldownMinutes: 5})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = source.GetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, ordinary.ID); !errors.Is(e, state.ErrInvalidArgument) {
			t.Fatal("ordinary rule", e)
		}
		if _, e = source.SetMCPReadinessReminderDefaults(t.Context(), (*f.Account()).ID, (*f.App()).ID, ordinary.ID, 0, policy); !errors.Is(e, state.ErrInvalidArgument) {
			t.Fatal("ordinary rule mutation", e)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
