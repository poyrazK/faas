package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

const flagNotificationsPath = "/v1/projects/flags-guard/environments/production/flags/notifications"

func (f flagExperimentTestEnv) channel(t *testing.T, name string) string {
	t.Helper()
	ch, err := f.mem.CreateNotificationChannel(context.Background(), state.NotificationChannel{AccountID: f.scope.AccountID, Name: name,
		Kind: "slack", TargetSealed: []byte("sealed")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	return ch.ID
}

func (f flagExperimentTestEnv) notifications(kind string) []state.FlagNotification {
	var out []state.FlagNotification
	for _, n := range f.mem.FlagNotificationsForTest() {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

// ADR-20261125085913 §1, §5: the settings routes follow the platform flag,
// accept only the account's channels, and experiments may name their own.
func TestFlagNotificationSettings(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	ch := f.channel(t, "ops")
	if code, _ := f.do(t, http.MethodGet, flagNotificationsPath, nil); code != http.StatusForbidden {
		t.Fatalf("settings without flag_notifications = %d; want 403", code)
	}
	if code, body := f.do(t, http.MethodPost, f.base, api.FlagExperimentCreateRequest{Key: "early", Flag: "checkout", RuleID: "weighted",
		Control: "legacy", MetricEvent: "order_completed", NotifyChannelIDs: []string{ch}}); code != http.StatusForbidden {
		t.Fatalf("experiment channels without flag_notifications = %d %s; want 403", code, body)
	}
	(*f.e.S()).EnablePlatformFlagForAll(platformflags.FlagNotifications)

	for name, ids := range map[string][]string{
		"not an id":    {"ops"},
		"foreign":      {uuid.NewString()},
		"twice":        {ch, ch},
		"over the cap": {ch, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()},
	} {
		if code, body := f.do(t, http.MethodPut, flagNotificationsPath, api.FlagNotificationSettingsRequest{ChannelIDs: ids}); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s; want 422", name, code, body)
		}
	}
	if code, body := f.do(t, http.MethodPut, flagNotificationsPath, api.FlagNotificationSettingsRequest{ChannelIDs: []string{ch}}); code != http.StatusOK {
		t.Fatalf("set = %d %s", code, body)
	}
	code, body := f.do(t, http.MethodGet, flagNotificationsPath, nil)
	var got api.FlagNotificationSettings
	if err := json.Unmarshal([]byte(body), &got); code != http.StatusOK || err != nil || len(got.ChannelIDs) != 1 || got.ChannelIDs[0] != ch || got.UpdatedAt == nil {
		t.Fatalf("get = %d %s", code, body)
	}

	x := f.create(t, api.FlagExperimentCreateRequest{Key: "own", Flag: "checkout", RuleID: "weighted", Control: "legacy",
		MetricEvent: "order_completed", NotifyChannelIDs: []string{ch}})
	if len(x.NotifyChannelIDs) != 1 || x.NotifyChannelIDs[0] != ch {
		t.Fatalf("experiment channels = %v", x.NotifyChannelIDs)
	}
	if code, body := f.do(t, http.MethodGet, f.base+"/own", nil); code != http.StatusOK || !strings.Contains(body, `"notify_channel_ids":["`+ch+`"]`) {
		t.Fatalf("experiment = %d %s", code, body)
	}
}

// §2: a guardrail rollback queues one problem to the experiment's own
// channels; later passes do not queue it again.
func TestFlagExperimentRollbackNotifies(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	(*f.e.S()).EnablePlatformFlagForAll(platformflags.FlagNotifications)
	envCh, ownCh := f.channel(t, "env"), f.channel(t, "own")
	if code, body := f.do(t, http.MethodPut, flagNotificationsPath, api.FlagNotificationSettingsRequest{ChannelIDs: []string{envCh}}); code != http.StatusOK {
		t.Fatalf("set = %d %s", code, body)
	}
	x := f.create(t, api.FlagExperimentCreateRequest{Key: "guarded", Flag: "checkout", RuleID: "weighted", Control: "legacy",
		MetricEvent: "order_completed", Start: true, NotifyChannelIDs: []string{ownCh}})
	now := time.Now()
	for i := 0; i < 150; i++ {
		f.mem.SeedFlagExperimentUnitForTest(x.ID, uuid.NewString(), "legacy", now, 20, int64(i%2), 0, 0, 100)
		f.mem.SeedFlagExperimentUnitForTest(x.ID, uuid.NewString(), "new", now, 20, int64(8+i%5), 0, 0, 100)
	}
	apidserver.RunFlagExperimentPass(*f.e.S(), now)
	apidserver.RunFlagExperimentPass(*f.e.S(), now.Add(5*time.Minute))

	problems := f.notifications(state.FlagNotifyExperimentProblem)
	if len(problems) != 1 {
		t.Fatalf("problems = %+v; want one", problems)
	}
	p := problems[0]
	if p.Subject != "Experiment guarded: rolled back by a guardrail" || len(p.ChannelIDs) != 1 || p.ChannelIDs[0] != ownCh ||
		!strings.Contains(p.Summary, "--project flags-guard --environment production") {
		t.Fatalf("problem = %+v", p)
	}
}

// §2: an experiment whose own channels were all deleted notifies the
// environment's, and the message says how to change them.
func TestFlagExperimentDeletedChannelsFallBack(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	(*f.e.S()).EnablePlatformFlagForAll(platformflags.FlagNotifications)
	envCh, ownCh := f.channel(t, "env"), f.channel(t, "own")
	if code, body := f.do(t, http.MethodPut, flagNotificationsPath, api.FlagNotificationSettingsRequest{ChannelIDs: []string{envCh}}); code != http.StatusOK {
		t.Fatalf("set = %d %s", code, body)
	}
	x := f.create(t, api.FlagExperimentCreateRequest{Key: "orphaned", Flag: "checkout", RuleID: "weighted", Control: "legacy",
		MetricEvent: "order_completed", Start: true, NotifyChannelIDs: []string{ownCh}})
	if err := f.mem.DeleteNotificationChannel(context.Background(), f.scope.AccountID, ownCh); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 150; i++ {
		f.mem.SeedFlagExperimentUnitForTest(x.ID, uuid.NewString(), "legacy", now, 20, int64(i%2), 0, 0, 100)
		f.mem.SeedFlagExperimentUnitForTest(x.ID, uuid.NewString(), "new", now, 20, int64(8+i%5), 0, 0, 100)
	}
	apidserver.RunFlagExperimentPass(*f.e.S(), now)
	problems := f.notifications(state.FlagNotifyExperimentProblem)
	if len(problems) != 1 || len(problems[0].ChannelIDs) != 1 || problems[0].ChannelIDs[0] != envCh ||
		!strings.Contains(problems[0].Summary, "gregale flag notify set --channel ID --project flags-guard --environment production") {
		t.Fatalf("problems = %+v; want one to the environment's channel", problems)
	}
}

// §2: a running experiment without a decision queues nothing.
func TestFlagExperimentKeepRunningDoesNotNotify(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	(*f.e.S()).EnablePlatformFlagForAll(platformflags.FlagNotifications)
	ch := f.channel(t, "env")
	if code, body := f.do(t, http.MethodPut, flagNotificationsPath, api.FlagNotificationSettingsRequest{ChannelIDs: []string{ch}}); code != http.StatusOK {
		t.Fatalf("set = %d %s", code, body)
	}
	f.create(t, api.FlagExperimentCreateRequest{Key: "calm", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed", Start: true})
	apidserver.RunFlagExperimentPass(*f.e.S(), time.Now())
	if got := f.mem.FlagNotificationsForTest(); len(got) != 0 {
		t.Fatalf("notifications = %+v; want none", got)
	}
}

// §3: the lifecycle check queues a weekly digest and a flag's expiry to the
// environment's channels, once each.
func TestFlagLifecycleNotifies(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	(*f.e.S()).EnablePlatformFlagForAll(platformflags.FlagNotifications)
	ch := f.channel(t, "env")
	if code, body := f.do(t, http.MethodPut, flagNotificationsPath, api.FlagNotificationSettingsRequest{ChannelIDs: []string{ch}}); code != http.StatusOK {
		t.Fatalf("set = %d %s", code, body)
	}
	one := int64(1)
	cfg := flags.Config{Flags: []flags.Flag{{Key: "old-export", Enabled: true, Owner: "payments", ExpiresAt: "2020-01-01"}}}
	if code, body := f.do(t, http.MethodPut, "/v1/projects/flags-guard/environments/production/flags",
		apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &one, Config: cfg}); code != http.StatusOK {
		t.Fatalf("publish = %d %s", code, body)
	}
	candidate := state.FeatureFlagAutoRolloutCandidate{Scope: f.scope, ProjectSlug: "flags-guard", EnvironmentSlug: "production"}
	now := time.Now()
	apidserver.RunFeatureFlagLifecycleCheck(*f.e.S(), candidate, now)
	apidserver.RunFeatureFlagLifecycleCheck(*f.e.S(), candidate, now.Add(time.Hour))

	expired, digests := f.notifications(state.FlagNotifyFlagExpired), f.notifications(state.FlagNotifyLifecycleDigest)
	if len(expired) != 1 || !strings.Contains(expired[0].Summary, "gregale flag delete old-export --project flags-guard --environment production") {
		t.Fatalf("expired = %+v", expired)
	}
	if len(digests) != 1 || !strings.Contains(digests[0].Summary, "old-export (expired; payments)") {
		t.Fatalf("digests = %+v", digests)
	}
}
