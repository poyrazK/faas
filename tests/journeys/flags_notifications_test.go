package journeys

import (
	"testing"
	"time"
)

// flagNotifyWait bounds the wait for a flag's expiry to reach a channel: apid
// checks each environment's flags once an hour (featureFlagLifecycleInterval)
// and meterd delivers within a minute, plus headroom for a slow pass.
const flagNotifyWait = 70 * time.Minute

// TestFlagNotifications is a customer sending flag notifications to a
// channel (ADR-20261125085913): the environment's channels can be set, read
// and cleared, and a flag that has expired is sent to them by the hourly
// lifecycle check. The channel is a Slack webhook no workspace owns, so the
// send shows as the channel's last error without posting anywhere.
//
// It skips when Flags or flag notifications are not released to the account.
// The expiry can take up to an hour to arrive.
func TestFlagNotifications(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	hidden := hiddenCapabilities(t, tg)
	skipHidden(t, hidden, "notification-channels")
	skipHidden(t, hidden, "flag-notifications")
	skipFlagsUnreleased(t, tg)
	j := &flagsJourney{tg: tg, runtime: "node", project: tg.slug("flagnotify")}
	j.app = j.project + "-web"
	tg.ownApp(t, j.app)
	t.Cleanup(func() {
		if res := tg.run(t, 2*time.Minute, "project", "delete", j.project, "--yes"); res.exit != 0 {
			t.Logf("warning: cleanup could not delete project %s (%s)", j.project, res.problem())
		}
	})
	var channelID string
	// Registered on the journey so the channel outlives the steps that use it.
	t.Cleanup(func() {
		if channelID != "" {
			tg.run(t, time.Minute, "alert", "channel", "delete", channelID)
		}
	})
	where := []string{"--project", j.project, "--environment", "production"}
	s := newStory(t)
	if !s.step("deploy_project", j.deploy) {
		return
	}
	if !s.step("add_channel", func(t *testing.T) {
		res := tg.execStdin(t, fakeSlackWebhook+"\n", time.Minute, "alert", "channel", "create",
			"--name", tg.slug("flagnotify"), "--kind", "slack", "--secret-stdin")
		skipUnreleased(t, res.problemCode(), res.problem())
		if res.exit != 0 {
			t.Fatalf("alert channel create: %s", res.problem())
		}
		if channelID = str(object(t, res.stdout), "id"); channelID == "" {
			t.Fatalf("alert channel create printed no id: %s", tail(string(res.stdout), 400))
		}
	}) {
		return
	}
	var since time.Time
	if !s.step("notify_set", func(t *testing.T) {
		tg.ok(t, time.Minute, append([]string{"flag", "notify", "set", "--channel", channelID}, where...)...)
		doc := object(t, tg.ok(t, time.Minute, append([]string{"flag", "notify", "get"}, where...)...).stdout)
		ids, _ := field(doc, "channel_ids").([]any)
		if len(ids) != 1 || ids[0] != channelID {
			t.Fatalf("flag notify get = %s; want channel %s", compact(doc), channelID)
		}
		since = time.Now().Add(-time.Minute)
	}) {
		return
	}
	if !s.step("expired_flag", func(t *testing.T) {
		yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
		tg.ok(t, time.Minute, "flag", "create", "--project", j.project, "notify-expired",
			"--owner", "e2e-journeys", "--expires", yesterday)
	}) {
		return
	}
	s.check("expiry_reaches_channel", func(t *testing.T) {
		eventually(t, "the hourly lifecycle check sends the expiry to the channel", flagNotifyWait, time.Minute, func() (bool, string) {
			ch, why := findChannel(t, tg, channelID)
			if ch == nil {
				return false, why
			}
			at, err := time.Parse(time.RFC3339, str(ch, "last_error_at"))
			if err != nil {
				at, err = time.Parse(time.RFC3339, str(ch, "last_delivered_at"))
			}
			return err == nil && at.After(since), compact(ch)
		})
	})
	s.check("notify_clear", func(t *testing.T) {
		tg.ok(t, time.Minute, append([]string{"flag", "notify", "clear"}, where...)...)
		doc := object(t, tg.ok(t, time.Minute, append([]string{"flag", "notify", "get"}, where...)...).stdout)
		if ids, _ := field(doc, "channel_ids").([]any); len(ids) != 0 {
			t.Fatalf("flag notify get after clear = %s; want no channels", compact(doc))
		}
	})
}
