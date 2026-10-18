package state

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type attentionFixture struct {
	t       *testing.T
	store   Store
	source  MCPReadinessAttentionStore
	outbox  MCPReadinessOutboxStore
	account Account
	app     App
	rule    AlertRule
	now     time.Time
}

func newAttentionFixture(t *testing.T, s Store) *attentionFixture {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@attention.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: a.ID, Slug: "attention-" + uuid.NewString()[:8], Type: AppTypeApp, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, AlertRule{AccountID: a.ID, AppID: app.ID, Name: "attention", Enabled: true, Metric: AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: AlertGt, WindowSpec: AlertWindow5m, Action: AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("fixture"), MCPReadinessScope: "staging", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	return &attentionFixture{t: t, store: s, source: s.(MCPReadinessAttentionStore), outbox: s.(MCPReadinessOutboxStore), account: a, app: app, rule: rule, now: time.Now().UTC().Truncate(time.Microsecond)}
}
func (f *attentionFixture) observe(at time.Time, breach bool, coverage string) MCPReadinessTransition {
	f.t.Helper()
	if coverage == "" {
		coverage = strings.Repeat("a", 64)
	}
	p, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": coverage, "mcp_readiness_scopes": []string{"staging"}})
	v, err := f.outbox.RecordMCPReadinessObservation(f.t.Context(), MCPReadinessObservation{Rule: f.rule, At: at, Breached: breach, Coverage: coverage, Payload: p, ObservedValue: 1})
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *attentionFixture) view(id string, at time.Time) api.MCPReadinessIncidentAttention {
	f.t.Helper()
	p, err := f.source.ListMCPReadinessAttention(f.t.Context(), f.account.ID, f.app.ID, "staging", "", 50, true, at)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, v := range p.Incidents {
		if v.IncidentID == id {
			return v
		}
	}
	f.t.Fatal("missing attention", id, p)
	return api.MCPReadinessIncidentAttention{}
}
func (f *attentionFixture) request(id, action string, at time.Time) api.MCPReadinessAttentionAction {
	v := f.view(id, at).Version
	return api.MCPReadinessAttentionAction{RequestID: uuid.NewString(), ExpectedVersion: &v, Action: action, Reason: "Investigating provider outage"}
}
func (f *attentionFixture) act(id string, r api.MCPReadinessAttentionAction, at time.Time) api.MCPReadinessAttentionReceipt {
	f.t.Helper()
	v, err := f.source.ActMCPReadinessAttention(f.t.Context(), f.account.ID, f.app.ID, id, r, at)
	if err != nil {
		f.t.Fatal(r, err)
	}
	return v
}
func (f *attentionFixture) configure(id string, at time.Time, maximum int) {
	r := f.request(id, "configure_reminders", at)
	r.ReminderPolicy = &api.MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: maximum}
	if maximum == 0 {
		r.ReminderPolicy.IntervalSeconds = 0
	}
	f.act(id, r, at)
}
func (f *attentionFixture) claim(at time.Time, want bool) MCPReadinessDeliveryClaim {
	f.t.Helper()
	v, won, err := f.outbox.ClaimMCPReadinessDelivery(f.t.Context(), f.rule.ID, at)
	if err != nil || won != want {
		f.t.Fatal("claim", won, want, err)
	}
	return v
}
func (f *attentionFixture) complete(c MCPReadinessDeliveryClaim, at time.Time, status AlertDeliveryStatus) {
	f.t.Helper()
	if err := f.store.UpdateAlertDeliveryStatus(f.t.Context(), c.Delivery.ID, status, 1, 200, "", nil); err != nil {
		f.t.Fatal(err)
	}
	if err := f.outbox.CompleteMCPReadinessDelivery(f.t.Context(), c.EventID, c.Token, at); err != nil {
		f.t.Fatal(err)
	}
}
func (f *attentionFixture) event(id string) api.MCPReadinessIncidentEvent {
	f.t.Helper()
	p, err := f.store.(MCPReadinessIncidentStore).ListMCPReadinessIncidents(f.t.Context(), f.account.ID, f.app.ID, "staging", "", 50)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, v := range p.Events {
		if v.EventID == id {
			return v
		}
	}
	f.t.Fatal("missing event", id, p)
	return api.MCPReadinessIncidentEvent{}
}
func TestMCPReadinessAttention(t *testing.T) {
	check := func(t *testing.T, s Store) {
		t.Run("budget and no catchup", func(t *testing.T) {
			f := newAttentionFixture(t, s)
			at := f.now
			first := f.observe(at, true, "")
			if v := f.view(first.IncidentID, at); v.Version != 0 || v.ReminderPolicy.MaxReminders != 0 || v.State != "unacknowledged" {
				t.Fatal(v)
			}
			f.observe(at.Add(time.Minute), true, "")
			if v := f.view(first.IncidentID, at); v.RemindersCreated != 0 {
				t.Fatal("default enabled", v)
			}
			f.configure(first.IncidentID, at.Add(time.Minute), 3)
			if v := f.observe(at.Add(2*time.Minute), true, ""); v.EventID != "" {
				t.Fatal("passed initial pending", v)
			}
			f.complete(f.claim(at.Add(2*time.Minute), true), at.Add(2*time.Minute), AlertDeliveryDelivered)
			var wg sync.WaitGroup
			var created atomic.Int32
			// All concurrent evaluators see the same observation cursor.
			for range 8 {
				wg.Go(func() {
					if f.observe(at.Add(4*time.Hour), true, "").EventID != "" {
						created.Add(1)
					}
				})
			}
			wg.Wait()
			if created.Load() != 1 {
				t.Fatal("duplicate or catch-up", created.Load())
			}
			v := f.view(first.IncidentID, at.Add(4*time.Hour))
			if v.RemindersCreated != 1 || v.Version != 2 || !v.NextReminderAt.Equal(at.Add(4*time.Hour+time.Minute)) {
				t.Fatal(v)
			}
			c := f.claim(at.Add(4*time.Hour), true)
			e := f.event(c.EventID)
			if e.ReminderNumber != 1 || e.NotificationReason != "reminder" || e.IncidentID != first.IncidentID {
				t.Fatal(e)
			}
			var payload map[string]any
			_ = json.Unmarshal(c.Delivery.Payload, &payload)
			if payload["event_id"] != c.EventID || payload["notification_reason"] != "reminder" || payload["reminder_number"] != float64(1) {
				t.Fatal(payload)
			}
			f.complete(c, at.Add(4*time.Hour), AlertDeliveryDelivered)
			if o := f.observe(at.Add(4*time.Hour), true, ""); o.EventID != "" {
				t.Fatal("equal observation", o)
			}
			if o := f.observe(at.Add(3*time.Hour), true, ""); o.EventID != "" {
				t.Fatal("stale observation", o)
			}
			for n := 2; n <= 3; n++ {
				when := at.Add(4*time.Hour + time.Duration(n-1)*time.Minute)
				o := f.observe(when, true, "")
				if o.EventID == "" {
					t.Fatal("missing reminder", n)
				}
				if e := f.event(o.EventID); e.ReminderNumber != n {
					t.Fatal(e)
				}
				f.complete(f.claim(when, true), when, AlertDeliveryDelivered)
			}
			f.configure(first.IncidentID, at.Add(5*time.Hour), 0)
			f.configure(first.IncidentID, at.Add(6*time.Hour), 3)
			if o := f.observe(at.Add(7*time.Hour), true, ""); o.EventID != "" {
				t.Fatal("budget reset", o)
			}
			if v := f.view(first.IncidentID, at.Add(7*time.Hour)); v.RemindersCreated != 3 || v.NextReminderAt != nil {
				t.Fatal(v)
			}
			recovered := f.observe(at.Add(8*time.Hour), false, "")
			if recovered.Event != "alert.resolved" {
				t.Fatal(recovered)
			}
			f.complete(f.claim(at.Add(8*time.Hour), true), at.Add(8*time.Hour), AlertDeliveryDelivered)
			next := f.observe(at.Add(9*time.Hour), true, "")
			if next.IncidentID == first.IncidentID || f.view(next.IncidentID, at).RemindersCreated != 0 || f.view(next.IncidentID, at).ReminderPolicy.MaxReminders != 0 {
				t.Fatal("new incident inherited policy", next)
			}
			p, err := f.source.ListMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, "staging", "", 1, true, at)
			if err != nil || p.NextAfter == "" || len(p.Incidents) != 1 {
				t.Fatal(p, err)
			}
			p2, err := f.source.ListMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, "staging", p.NextAfter, 1, true, at)
			if err != nil || len(p2.Incidents) != 1 || p2.Incidents[0].IncidentID == p.Incidents[0].IncidentID {
				t.Fatal(p2, err)
			}
			p, err = f.source.ListMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, "staging", "", 20, false, at)
			if err != nil || len(p.Incidents) != 1 || p.Incidents[0].IncidentID != next.IncidentID {
				t.Fatal(p, err)
			}
		})
		t.Run("acknowledgement replay and expiry", func(t *testing.T) {
			f := newAttentionFixture(t, s)
			at := f.now
			first := f.observe(at, true, "")
			f.configure(first.IncidentID, at, 5)
			f.complete(f.claim(at, true), at, AlertDeliveryDelivered)
			reminder := f.observe(at.Add(time.Minute), true, "")
			r := f.request(first.IncidentID, "acknowledge", at.Add(time.Minute))
			expiry := at.Add(3 * time.Minute)
			r.ExpiresAt = &expiry
			receipts := make(chan api.MCPReadinessAttentionReceipt, 8)
			failures := make(chan error, 8)
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					v, err := f.source.ActMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, first.IncidentID, r, at.Add(time.Minute))
					if err != nil {
						failures <- err
					} else {
						receipts <- v
					}
				})
			}
			wg.Wait()
			close(receipts)
			close(failures)
			for err := range failures {
				t.Fatal(err)
			}
			var receipt api.MCPReadinessAttentionReceipt
			for v := range receipts {
				if receipt.RequestID != "" && !reflect.DeepEqual(v, receipt) {
					t.Fatal("replay changed", v, receipt)
				}
				receipt = v
			}
			if receipt.CreatedBy != f.account.ID || receipt.Version != 3 {
				t.Fatal(receipt)
			}
			if e := f.event(reminder.EventID); e.DeliveryStatus != "suppressed" || e.SuppressionReason != "acknowledged" {
				t.Fatal(e)
			}
			f.claim(at.Add(2*time.Minute), false)
			if v := f.observe(at.Add(2*time.Minute), true, ""); v.EventID != "" {
				t.Fatal(v)
			}
			changed := r
			changed.Reason = "Different decision"
			if _, err := f.source.ActMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, first.IncidentID, changed, at); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			stale := r
			stale.RequestID = uuid.NewString()
			if _, err := f.source.ActMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, first.IncidentID, stale, at.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			f.claim(expiry, false)
			if v := f.view(first.IncidentID, expiry); v.State != "expired" {
				t.Fatal(v)
			}
			v := f.observe(expiry, true, "")
			if v.EventID == "" {
				t.Fatal("expiry without fresh reminder")
			}
			f.complete(f.claim(expiry, true), expiry, AlertDeliveryDelivered)
			indefinite := f.request(first.IncidentID, "acknowledge", expiry)
			f.act(first.IncidentID, indefinite, expiry)
			if v := f.observe(at.Add(24*time.Hour), true, ""); v.EventID != "" {
				t.Fatal("indefinite expired", v)
			}
			release := f.request(first.IncidentID, "release", at.Add(24*time.Hour))
			f.act(first.IncidentID, release, at.Add(24*time.Hour))
			if v := f.observe(at.Add(24*time.Hour+30*time.Second), true, ""); v.EventID != "" {
				t.Fatal("release skipped interval", v)
			}
			if v := f.observe(at.Add(24*time.Hour+time.Minute), true, ""); v.EventID == "" {
				t.Fatal("release no reminder")
			}
			f.act(first.IncidentID, f.request(first.IncidentID, "acknowledge", at.Add(25*time.Hour)), at.Add(25*time.Hour))
			recovered := f.observe(at.Add(26*time.Hour), false, "")
			if recovered.Event != "alert.resolved" {
				t.Fatal(recovered)
			}
			f.complete(f.claim(at.Add(26*time.Hour), true), at.Add(26*time.Hour), AlertDeliveryDelivered)
			if got := f.act(first.IncidentID, r, at.Add(27*time.Hour)); !reflect.DeepEqual(got, receipt) {
				t.Fatal("replay after expiry and recovery", got, receipt)
			}
			if _, err := f.source.ActMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, first.IncidentID, f.request(first.IncidentID, "release", at.Add(27*time.Hour)), at.Add(27*time.Hour)); !errors.Is(err, ErrConflict) {
				t.Fatal("closed incident", err)
			}
			h, err := f.source.ListMCPReadinessAttentionHistory(t.Context(), f.account.ID, f.app.ID, first.IncidentID, "", 2)
			if err != nil || len(h.Actions) != 2 || h.NextBefore == "" {
				t.Fatal(h, err)
			}
			h2, err := f.source.ListMCPReadinessAttentionHistory(t.Context(), f.account.ID, f.app.ID, first.IncidentID, h.NextBefore, 50)
			if err != nil || len(h2.Actions) != 3 {
				t.Fatal(h2, err)
			}
			if _, err := f.source.ListMCPReadinessAttention(t.Context(), uuid.NewString(), f.app.ID, "", "", 20, true, at); !errors.Is(err, ErrNotFound) {
				t.Fatal("owner read", err)
			}
			if _, err := f.source.ActMCPReadinessAttention(t.Context(), uuid.NewString(), f.app.ID, first.IncidentID, r, at); !errors.Is(err, ErrNotFound) {
				t.Fatal("owner replay", err)
			}
			if _, err := f.source.ListMCPReadinessAttentionHistory(t.Context(), f.account.ID, f.app.ID, uuid.NewString(), "", 20); !errors.Is(err, ErrNotFound) {
				t.Fatal("unknown history", err)
			}
			if err := s.DeleteAlertRule(t.Context(), f.rule.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.source.ActMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, first.IncidentID, r, at); !errors.Is(err, ErrNotFound) {
				t.Fatal("deleted history", err)
			}
		})
		for _, mode := range []string{"resolved", "coverage", "stale", "disabled policy", "lower budget", "ack leased", "expired lease"} {
			t.Run(mode, func(t *testing.T) {
				f := newAttentionFixture(t, s)
				at := f.now
				first := f.observe(at, true, "")
				f.complete(f.claim(at, true), at, AlertDeliveryDelivered)
				f.configure(first.IncidentID, at, 5)
				reminder := f.observe(at.Add(time.Minute), true, "")
				reason := "reminder_obsolete"
				switch mode {
				case "resolved":
					f.observe(at.Add(61*time.Second), false, "")
					reason = "incident_resolved"
				case "coverage":
					f.observe(at.Add(61*time.Second), false, strings.Repeat("b", 64))
					if f.view(first.IncidentID, at).ResolvedAt != nil {
						t.Fatal("changed coverage recovered")
					}
				case "stale":
					f.claim(at.Add(4*time.Minute), false)
				case "disabled policy":
					f.configure(first.IncidentID, at.Add(61*time.Second), 0)
					reason = "reminders_disabled"
				case "lower budget":
					f.complete(f.claim(at.Add(time.Minute), true), at.Add(time.Minute), AlertDeliveryDelivered)
					reminder = f.observe(at.Add(2*time.Minute), true, "")
					f.configure(first.IncidentID, at.Add(121*time.Second), 1)
				case "ack leased", "expired lease":
					c := f.claim(at.Add(time.Minute), true)
					when := at.Add(61 * time.Second)
					if mode == "expired lease" {
						when = c.LeaseUntil.Add(time.Second)
					}
					f.act(first.IncidentID, f.request(first.IncidentID, "acknowledge", when), when)
					reason = "acknowledged"
					if mode == "ack leased" {
						if f.event(c.EventID).DeliveryStatus == "suppressed" {
							t.Fatal("cancelled live request")
						}
						f.complete(c, when, AlertDeliveryDelivered)
						if f.event(c.EventID).DeliveryStatus != "delivered" {
							t.Fatal("lease could not finish")
						}
						return
					}
					if err := f.outbox.CompleteMCPReadinessDelivery(t.Context(), c.EventID, c.Token, when); !errors.Is(err, ErrConflict) {
						t.Fatal("expired completion was not fenced", err)
					}
				}
				e := f.event(reminder.EventID)
				if e.DeliveryStatus != "suppressed" || e.SuppressionReason != reason || e.NextAttemptAt != nil {
					t.Fatal("suppression", e, reason)
				}
				if mode == "resolved" {
					c := f.claim(at.Add(62*time.Second), true)
					if f.event(c.EventID).Event != "alert.resolved" {
						t.Fatal("obsolete blocked recovery")
					}
				}
				if f.view(first.IncidentID, at).RemindersCreated < 1 {
					t.Fatal("suppression reset budget")
				}
			})
		}
		t.Run("restart and validation", func(t *testing.T) {
			f := newAttentionFixture(t, s)
			at := f.now
			first := f.observe(at, true, "")
			f.complete(f.claim(at, true), at, AlertDeliveryDelivered)
			f.configure(first.IncidentID, at, 3)
			r := f.request(first.IncidentID, "acknowledge", at)
			for _, expiry := range []time.Time{at, at.Add(-time.Second), at.Add(30*24*time.Hour + time.Second)} {
				r.ExpiresAt = &expiry
				if _, err := f.source.ActMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, first.IncidentID, r, at); !errors.Is(err, ErrInvalidArgument) {
					t.Fatal("expiry", expiry, err)
				}
			}
			r.ExpiresAt = nil
			receipt := f.act(first.IncidentID, r, at)
			if pg, ok := s.(*PgStore); ok {
				restarted := NewPgStore(pg.pool)
				f.source = restarted
				f.outbox = restarted
			}
			if replay := f.act(first.IncidentID, r, at.Add(time.Hour)); !reflect.DeepEqual(replay, receipt) {
				t.Fatal("restart lost receipt", replay, receipt)
			}
			if v := f.observe(at.Add(time.Hour), true, ""); v.EventID != "" {
				t.Fatal("restart lost ack", v)
			}
			v := f.view(first.IncidentID, at)
			v.Acknowledgement.Reason = "mutated returned snapshot"
			if f.view(first.IncidentID, at).Acknowledgement.Reason != r.Reason {
				t.Fatal("mutable snapshot")
			}
			p, err := f.source.ListMCPReadinessAttention(t.Context(), f.account.ID, f.app.ID, "production", "", 20, true, at)
			if err != nil || len(p.Incidents) != 0 {
				t.Fatal("environment isolation", p, err)
			}
		})
		t.Run("maintenance and disabled rule", func(t *testing.T) {
			f := newAttentionFixture(t, s)
			at := f.now
			first := f.observe(at, true, "")
			f.complete(f.claim(at, true), at, AlertDeliveryDelivered)
			f.configure(first.IncidentID, at, 3)
			windows := s.(MCPReadinessMaintenanceStore)
			_, err := windows.CreateMCPReadinessMaintenance(t.Context(), f.account.ID, f.app.ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", StartsAt: at.Add(time.Minute), EndsAt: at.Add(3 * time.Minute), Reason: "Planned work"}, at)
			if err != nil {
				t.Fatal(err)
			}
			if v := f.observe(at.Add(time.Minute), true, ""); v.EventID != "" {
				t.Fatal("reminder in maintenance", v)
			}
			f.act(first.IncidentID, f.request(first.IncidentID, "acknowledge", at.Add(2*time.Minute)), at.Add(2*time.Minute))
			continuation := f.observe(at.Add(3*time.Minute), true, "")
			if continuation.EventID == "" || f.event(continuation.EventID).NotificationReason != "maintenance_ended" {
				t.Fatal("ack blocked continuation", continuation)
			}
			f.complete(f.claim(at.Add(3*time.Minute), true), at.Add(3*time.Minute), AlertDeliveryDelivered)
			f.rule.Enabled = false
			if _, err = s.UpdateAlertRule(t.Context(), f.rule.ID, UpdateAlertRuleParams{Enabled: &f.rule.Enabled}); err != nil {
				t.Fatal(err)
			}
			f.act(first.IncidentID, f.request(first.IncidentID, "release", at.Add(4*time.Minute)), at.Add(4*time.Minute))
			f.configure(first.IncidentID, at.Add(4*time.Minute), 5)
			f.claim(at.Add(5*time.Minute), false)
			if _, err = f.outbox.RecordMCPReadinessObservation(t.Context(), MCPReadinessObservation{Rule: f.rule, At: at.Add(5 * time.Minute), Coverage: strings.Repeat("a", 64), Payload: json.RawMessage(`{"metric":"mcp_readiness_unhealthy","mcp_readiness_coverage_fingerprint":"` + strings.Repeat("a", 64) + `"}`)}); !errors.Is(err, ErrConflict) {
				t.Fatal("disabled observation", err)
			}
		})
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
