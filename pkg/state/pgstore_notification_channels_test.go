package state_test

// adr: 20261029203000 — a channel that is an alert rule's only destination
// cannot be deleted; the refusal names the rule and deletes nothing.

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStore_NotificationChannelDeleteKeepsRulesReachable(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	channel := func(name string) state.NotificationChannel {
		t.Helper()
		ch, err := s.CreateNotificationChannel(ctx, state.NotificationChannel{
			AccountID: acct, Name: name, Kind: "slack", TargetSealed: []byte("sealed"), TargetHint: "hooks.slack.com/T/B/…",
		}, 10)
		if err != nil {
			t.Fatalf("CreateNotificationChannel %s: %v", name, err)
		}
		return ch
	}
	ops, backup := channel("ops"), channel("backup")

	only := pgSampleAlertRule(acct, app)
	only.Name, only.WebhookURL, only.WebhookSecretSealed = "only-ops", "", []byte{}
	rule, err := s.CreateAlertRule(ctx, only)
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}
	if err := s.SetAlertRuleChannels(ctx, rule.ID, []string{ops.ID}); err != nil {
		t.Fatalf("SetAlertRuleChannels: %v", err)
	}
	// A webhook rule on the same channel never blocks the delete.
	hooked, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, app))
	if err != nil {
		t.Fatalf("CreateAlertRule hooked: %v", err)
	}
	if err := s.SetAlertRuleChannels(ctx, hooked.ID, []string{ops.ID}); err != nil {
		t.Fatalf("SetAlertRuleChannels hooked: %v", err)
	}

	var inUse *state.NotificationChannelInUseError
	err = s.DeleteNotificationChannel(ctx, acct, ops.ID)
	if !errors.As(err, &inUse) || len(inUse.Rules) != 1 || inUse.Rules[0].ID != rule.ID || inUse.Rules[0].Name != "only-ops" {
		t.Fatalf("delete of the rule's only channel = %v (%+v), want an in-use error naming only-ops", err, inUse)
	}
	if _, err := s.GetNotificationChannel(ctx, acct, ops.ID); err != nil {
		t.Fatalf("a refused delete must keep the channel: %v", err)
	}

	if err := s.SetAlertRuleChannels(ctx, rule.ID, []string{ops.ID, backup.ID}); err != nil {
		t.Fatalf("SetAlertRuleChannels both: %v", err)
	}
	if err := s.DeleteNotificationChannel(ctx, acct, ops.ID); err != nil {
		t.Fatalf("delete with another channel left: %v", err)
	}
	if err := s.DeleteNotificationChannel(ctx, acct, ops.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}
