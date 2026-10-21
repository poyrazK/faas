package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessMaintenanceLifecycle(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		ctx := t.Context()
		account, err := s.CreateAccount(ctx, "maintenance@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "maintenance-app", Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		source := s.(state.MCPReadinessOutboxStore)
		windows := s.(state.MCPReadinessMaintenanceStore)
		timeline := s.(state.MCPReadinessIncidentStore)
		makeRule := func(name, scope string) state.AlertRule {
			t.Helper()
			r, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: account.ID, AppID: app.ID, Name: name, Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), MCPReadinessScope: scope, Comparison: state.AlertGt, Threshold: 0, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("fixture"), CooldownMinutes: 5})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		observe := func(rule state.AlertRule, when time.Time, breach bool, scopes ...string) state.MCPReadinessTransition {
			t.Helper()
			coverage := strings.Repeat("a", 64)
			p, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": coverage, "mcp_readiness_scopes": scopes, "mcp_readiness": []api.MCPReadinessMonitorStatus{}})
			out, err := source.RecordMCPReadinessObservation(ctx, state.MCPReadinessObservation{Rule: rule, At: when, Breached: breach, Coverage: coverage, Payload: p, ObservedValue: 1})
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		create := func(scope string, start, end time.Time) api.MCPReadinessMaintenanceWindow {
			t.Helper()
			w, err := windows.CreateMCPReadinessMaintenance(ctx, account.ID, app.ID, api.CreateMCPReadinessMaintenanceRequest{Scope: scope, StartsAt: start, EndsAt: end, Reason: "planned upgrade"}, at)
			if err != nil {
				t.Fatal(err)
			}
			return w
		}
		noClaim := func(rule state.AlertRule, when time.Time) {
			t.Helper()
			if _, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, when); err != nil || won {
				t.Fatal("unexpected notification", won, err)
			}
		}
		stage := makeRule("staging", "staging")
		prod := makeRule("production", "production")
		mixed := makeRule("all", "")
		first := create("staging", at, at.Add(10*time.Minute))
		overlap := create("staging", at.Add(5*time.Minute), at.Add(20*time.Minute))
		fire := observe(stage, at.Add(time.Second), true, "staging")
		observe(prod, at.Add(time.Second), true, "production")
		observe(mixed, at.Add(time.Second), true, "staging", "production")
		noClaim(stage, at.Add(2*time.Second))
		for _, rule := range []state.AlertRule{prod, mixed} {
			if _, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, at.Add(2*time.Second)); err != nil || !won {
				t.Fatal("staging muted production", won, err)
			}
		}
		// Recovery and another breach remain separate incidents during silence.
		recovery := observe(stage, at.Add(3*time.Second), false, "staging")
		second := observe(stage, at.Add(4*time.Second), true, "staging")
		if recovery.IncidentID != fire.IncidentID || second.IncidentID == fire.IncidentID {
			t.Fatal("incident lifecycle collapsed")
		}
		observe(stage, at.Add(11*time.Minute), true, "staging")
		noClaim(stage, at.Add(11*time.Minute))
		page, err := timeline.ListMCPReadinessIncidents(ctx, account.ID, app.ID, "staging", "", 50)
		if err != nil {
			t.Fatal(err)
		}
		suppressed := 0
		for _, e := range page.Events {
			if e.RuleID == stage.ID {
				suppressed++
				if e.DeliveryStatus != "suppressed" || e.AttemptCount != 0 || e.NextAttemptAt != nil || len(e.MaintenanceWindowIDs) == 0 {
					t.Fatal("suppression hidden", e)
				}
			}
		}
		if suppressed != 3 {
			t.Fatal("lost muted history", suppressed)
		}
		// Cancellation preserves attribution and ends only this overlapping window.
		cancelled, err := windows.CancelMCPReadinessMaintenance(ctx, account.ID, app.ID, overlap.ID, "work complete", at.Add(12*time.Minute))
		if err != nil || cancelled.CancelledBy != account.ID || cancelled.CancelReason != "work complete" {
			t.Fatal(cancelled, err)
		}
		again, err := windows.CancelMCPReadinessMaintenance(ctx, account.ID, app.ID, overlap.ID, "retry", at.Add(13*time.Minute))
		if err != nil || again.CancelReason != "work complete" {
			t.Fatal("cancellation mutated", again, err)
		}
		resumed := observe(stage, at.Add(12*time.Minute), true, "staging")
		if resumed.EventID == "" || resumed.IncidentID != second.IncidentID {
			t.Fatal("missing still unhealthy notification", resumed)
		}
		// A worker delayed behind the newer observation must not retroactively
		// mute its continuation using an older, still-in-maintenance timestamp.
		noClaim(stage, at.Add(11*time.Minute))
		if repeat := observe(stage, at.Add(13*time.Minute), true, "staging"); repeat.EventID != "" {
			t.Fatal("repeated expiry notification")
		}
		// Concurrent workers cannot emit the resume event twice.
		var wg sync.WaitGroup
		claims := make(chan state.MCPReadinessDeliveryClaim, 6)
		errs := make(chan error, 6)
		for range 6 {
			wg.Go(func() {
				c, won, e := source.ClaimMCPReadinessDelivery(ctx, stage.ID, at.Add(13*time.Minute))
				if e != nil {
					errs <- e
				}
				if won {
					claims <- c
				}
			})
		}
		wg.Wait()
		close(claims)
		close(errs)
		for e := range errs {
			t.Fatal(e)
		}
		if len(claims) != 1 {
			t.Fatal("duplicate resume claim", len(claims))
		}
		claim := <-claims
		var p map[string]any
		_ = json.Unmarshal(claim.Delivery.Payload, &p)
		if claim.EventID != resumed.EventID || p["notification_reason"] != "maintenance_ended" {
			t.Fatal("old notifications replayed", claim.EventID, p)
		}
		done := at.Add(13 * time.Minute)
		if err = s.UpdateAlertDeliveryStatus(ctx, claim.Delivery.ID, state.AlertDeliveryDelivered, 1, 200, "", &done); err != nil {
			t.Fatal(err)
		}
		if err = source.CompleteMCPReadinessDelivery(ctx, claim.EventID, claim.Token, done); err != nil {
			t.Fatal(err)
		}
		noClaim(stage, done)
		observe(stage, at.Add(14*time.Minute), false, "staging")
		if _, won, err := source.ClaimMCPReadinessDelivery(ctx, stage.ID, at.Add(14*time.Minute)); err != nil || !won {
			t.Fatal("paired recovery missing", won, err)
		}
		// Fresh healthy after expiry doesn't send an orphan recovery for a muted fire.
		healed := makeRule("healed", "staging")
		observe(healed, at.Add(time.Minute), true, "staging")
		observe(healed, at.Add(22*time.Minute), false, "staging")
		noClaim(healed, at.Add(23*time.Minute))
		// A complete evaluator outage across the window still reconciles on restart.
		restart := makeRule("restart", "staging")
		observe(restart, at.Add(-time.Minute), true, "staging")
		if v := observe(restart, at.Add(25*time.Minute), true, "staging"); v.EventID == "" {
			t.Fatal("missed window after restart")
		}
		if v := observe(restart, at.Add(26*time.Minute), true, "staging"); v.EventID != "" {
			t.Fatal("restart repeated reminder")
		}
		// A window added between observation and dispatch suppresses queued work.
		late := makeRule("late", "staging")
		observe(late, at.Add(30*time.Minute), true, "staging")
		create("staging", at.Add(30*time.Minute), at.Add(40*time.Minute))
		noClaim(late, at.Add(31*time.Minute))
		// Stable tied-time pagination and account/app isolation.
		list, err := windows.ListMCPReadinessMaintenance(ctx, account.ID, app.ID, "staging", "", 1)
		if err != nil || len(list.Windows) != 1 || list.NextBefore == "" {
			t.Fatal(list, err)
		}
		next, err := windows.ListMCPReadinessMaintenance(ctx, account.ID, app.ID, "staging", list.NextBefore, 50)
		if err != nil || len(next.Windows) != 2 || next.Windows[0].ID == list.Windows[0].ID {
			t.Fatal(next, err)
		}

		// Half-open expiry sends only the fresh continuation, never the backlog.
		natural := observe(late, at.Add(40*time.Minute), true, "staging")
		c, won, err := source.ClaimMCPReadinessDelivery(ctx, late.ID, at.Add(40*time.Minute))
		if err != nil || !won || c.EventID != natural.EventID || natural.EventID == "" {
			t.Fatal("natural expiry replayed backlog", c.EventID, natural, won, err)
		}
		// A lease already handed to transport can finish during maintenance.
		inFlight := makeRule("in-flight", "staging")
		initial := observe(inFlight, at.Add(45*time.Minute), true, "staging")
		lease, won, err := source.ClaimMCPReadinessDelivery(ctx, inFlight.ID, at.Add(45*time.Minute))
		if err != nil || !won {
			t.Fatal("in-flight setup", won, err)
		}
		create("staging", at.Add(46*time.Minute), at.Add(48*time.Minute))
		observe(inFlight, at.Add(46*time.Minute), true, "staging")
		done = at.Add(46 * time.Minute)
		if err = s.UpdateAlertDeliveryStatus(ctx, lease.Delivery.ID, state.AlertDeliveryDelivered, 1, 200, "", &done); err != nil {
			t.Fatal(err)
		}
		if err = source.CompleteMCPReadinessDelivery(ctx, lease.EventID, lease.Token, done); err != nil {
			t.Fatal("live lease invalidated", err)
		}
		resumedLease := observe(inFlight, at.Add(48*time.Minute), true, "staging")
		if resumedLease.EventID == "" || resumedLease.IncidentID != initial.IncidentID {
			t.Fatal("no reconciliation after live send", resumedLease)
		}

		// A lease spanning the end of a short window must not rearm an
		// already recorded continuation when the old lease finally expires.
		spanning := makeRule("spanning", "staging")
		spanningFire := observe(spanning, at.Add(70*time.Minute), true, "staging")
		oldLease, won, err := source.ClaimMCPReadinessDelivery(ctx, spanning.ID, at.Add(70*time.Minute))
		if err != nil || !won {
			t.Fatal("spanning lease setup", won, err)
		}
		create("staging", at.Add(71*time.Minute), at.Add(71*time.Minute+30*time.Second))
		observe(spanning, at.Add(71*time.Minute), true, "staging")
		continuation := observe(spanning, at.Add(71*time.Minute+30*time.Second), true, "staging")
		if continuation.EventID == "" || continuation.IncidentID != spanningFire.IncidentID {
			t.Fatal("no continuation while old lease lives", continuation)
		}
		noClaim(spanning, at.Add(71*time.Minute+45*time.Second))
		continuationLease, won, err := source.ClaimMCPReadinessDelivery(ctx, spanning.ID, at.Add(72*time.Minute+time.Second))
		if err != nil || !won || continuationLease.EventID != continuation.EventID {
			t.Fatal("spanning lease repeated old fire", continuationLease, won, err)
		}
		done = at.Add(72*time.Minute + time.Second)
		if err = s.UpdateAlertDeliveryStatus(ctx, continuationLease.Delivery.ID, state.AlertDeliveryDelivered, 1, 200, "", &done); err != nil {
			t.Fatal(err)
		}
		if err = source.CompleteMCPReadinessDelivery(ctx, continuationLease.EventID, continuationLease.Token, done); err != nil {
			t.Fatal(err)
		}
		if err = source.CompleteMCPReadinessDelivery(ctx, oldLease.EventID, oldLease.Token, done); !errors.Is(err, state.ErrConflict) {
			t.Fatal("late old completion was accepted", err)
		}
		if v := observe(spanning, at.Add(72*time.Minute+2*time.Second), true, "staging"); v.EventID != "" {
			t.Fatal("expired lease rearmed continuation", v)
		}
		// A recently backdated start can precede the observation cursor;
		// creation time still makes this new window visible after downtime.
		preview := makeRule("preview", "preview")
		previewAt := at.Add(80*time.Minute + time.Second)
		previewFire := observe(preview, previewAt, true, "preview")
		sent, won, err := source.ClaimMCPReadinessDelivery(ctx, preview.ID, previewAt)
		if err != nil || !won {
			t.Fatal("preview setup", won, err)
		}
		if err = s.UpdateAlertDeliveryStatus(ctx, sent.Delivery.ID, state.AlertDeliveryDelivered, 1, 200, "", &previewAt); err != nil {
			t.Fatal(err)
		}
		if err = source.CompleteMCPReadinessDelivery(ctx, sent.EventID, sent.Token, previewAt); err != nil {
			t.Fatal(err)
		}
		_, err = windows.CreateMCPReadinessMaintenance(ctx, account.ID, app.ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "preview", StartsAt: at.Add(80 * time.Minute), EndsAt: at.Add(81 * time.Minute), Reason: "recently started work"}, at.Add(80*time.Minute+2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if v := observe(preview, at.Add(82*time.Minute), true, "preview"); v.EventID == "" || v.IncidentID != previewFire.IncidentID {
			t.Fatal("new backdated window was missed", v)
		}
		foreign, err := s.CreateAccount(ctx, "maintenance-other@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = windows.ListMCPReadinessMaintenance(ctx, foreign.ID, app.ID, "staging", "", 20); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign history", err)
		}
		if _, err = windows.CancelMCPReadinessMaintenance(ctx, foreign.ID, app.ID, first.ID, "foreign", at); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign cancel", err)
		}
		if _, err = windows.CreateMCPReadinessMaintenance(ctx, foreign.ID, app.ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", EndsAt: at.Add(time.Hour), Reason: "foreign"}, at); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign create", err)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPReadinessMaintenanceMatching(t *testing.T) {
	at := time.Now().UTC()
	stop := at.Add(time.Minute)
	windows := []api.MCPReadinessMaintenanceWindow{{ID: "stage", Scope: "staging", StartsAt: at, EndsAt: stop}, {ID: "prod", Scope: "production", StartsAt: at.Add(30 * time.Second), EndsAt: stop}}
	payload := json.RawMessage(`{"mcp_readiness_scopes":["staging","production"]}`)
	rule := state.AlertRule{}
	if ids := state.ReadinessMaintenanceIDs(rule, payload, windows, at); len(ids) != 0 {
		t.Fatal("partial mixed coverage muted", ids)
	}
	if ids := state.ReadinessMaintenanceIDs(rule, payload, windows, at.Add(30*time.Second)); len(ids) != 2 {
		t.Fatal("complete mixed coverage did not mute", ids)
	}
	if ids := state.ReadinessMaintenanceIDs(rule, payload, windows, stop); len(ids) != 0 {
		t.Fatal("end boundary still muted", ids)
	}
	windows[1].CancelledAt = &at
	if ids := state.ReadinessMaintenanceIDs(rule, payload, windows, at.Add(30*time.Second)); len(ids) != 0 {
		t.Fatal("cancelled future window became active", ids)
	}
	for _, raw := range []string{`{}`, `{"mcp_readiness_scopes":[]}`, `{"mcp_readiness_scopes":[""]}`, `{"mcp_readiness_scopes":["staging","bad_scope"]}`, `bad`} {
		if ids := state.ReadinessMaintenanceIDs(rule, json.RawMessage(raw), windows, at); len(ids) != 0 {
			t.Fatal("unknown coverage muted", raw, ids)
		}
	}
}
