package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The subscription testdata/event-worker declares. It is fixed so every run
// deploys the same source; each run publishes its own event ids.
const (
	eventJourneySource = "e2ej.journeys"
	eventJourneyType   = "journey.ping"
)

// TestEventDelivery publishes events to a subscribed function and follows
// them through routing, the handler, idempotent republish, an identity
// conflict, a filtered event and the dead-letter queue (inventory row 98,
// the event GA gate, ADR-20261029093000). It skips while events are not
// enabled for the account.
func TestEventDelivery(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	requireCapabilities(t, tg, "events", "event-subscriptions")
	slug := tg.app(t, "events")
	prefix := "e2ej-" + tg.runID + "-"
	s := newStory(t)

	s.step("deploy_subscriber", func(t *testing.T) {
		tg.deployWith(t, slug, false, "--path", fixtureSource(t, "event-worker"))
	})
	s.check("subscription_listed", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "event", "subscription", "list", slug)
		subs := items(object(t, res.stdout), "subscriptions")
		for _, sub := range subs {
			if str(sub, "source") == eventJourneySource && str(sub, "type") == eventJourneyType && field(sub, "enabled") == true {
				return
			}
		}
		t.Fatalf("no enabled %s/%s subscription on %s: %s", eventJourneySource, eventJourneyType, slug, compact(subs))
	})

	deliverID := prefix + "deliver"
	var acceptedAt, invocationID string
	s.step("publish_and_deliver", func(t *testing.T) {
		start := time.Now()
		out := object(t, tg.ok(t, time.Minute, "event", "publish", eventJourneySource, eventJourneyType,
			"--id", deliverID, "--data", `{"deliver":true}`).stdout)
		acceptedAt = str(out, "accepted_at")
		if str(out, "id") != deliverID || acceptedAt == "" {
			t.Fatalf("event publish: %s", compact(out))
		}
		recipient := waitRecipient(t, tg, deliverID, slug, 5*time.Minute, func(r map[string]any) bool {
			return str(r, "routing.state") == "enqueued" && str(r, "execution.state") == "completed"
		})
		invocationID = str(recipient, "execution.invocation_id")
		t.Logf("metric event_publish_to_handler_completed_ms=%d", time.Since(start).Milliseconds())
	})
	s.check("handler_saw_envelope", func(t *testing.T) {
		line := "e2e-event id=" + deliverID
		eventually(t, "handler log line in `gregale logs`", 3*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "logs", slug, "--grep", deliverID, "--since", "30m")
			if res.exit != 0 {
				return false, res.problem()
			}
			return strings.Contains(string(res.stdout), line), "no " + line + " in output: " + tail(string(res.stdout), 300)
		})
	})
	s.check("republish_is_idempotent", func(t *testing.T) {
		out := object(t, tg.ok(t, time.Minute, "event", "publish", eventJourneySource, eventJourneyType,
			"--id", deliverID, "--data", `{"deliver":true}`).stdout)
		if str(out, "accepted_at") != acceptedAt {
			t.Errorf("republish answered accepted_at %q, want the original %q", str(out, "accepted_at"), acceptedAt)
		}
		receipt := eventReceipt(t, tg, deliverID)
		var mine []map[string]any
		for _, r := range items(receipt, "recipients") {
			if str(r, "app_slug") == slug {
				mine = append(mine, r)
			}
		}
		if len(mine) != 1 || str(mine[0], "execution.invocation_id") != invocationID {
			t.Errorf("republish changed the delivery: want one recipient with invocation %s, got %s", invocationID, compact(mine))
		}
	})
	s.check("identity_conflict_refused", func(t *testing.T) {
		res := tg.run(t, time.Minute, "event", "publish", eventJourneySource, eventJourneyType,
			"--id", deliverID, "--data", `{"deliver":true,"changed":true}`)
		if res.exit == 0 || res.problemCode() != "conflict" {
			t.Fatalf("publishing different data under an accepted id: exit %d, %s; want a conflict", res.exit, res.problem())
		}
	})
	s.check("filter_skips_event", func(t *testing.T) {
		id := prefix + "filtered"
		tg.ok(t, time.Minute, "event", "publish", eventJourneySource, eventJourneyType, "--id", id, "--data", `{"deliver":false}`)
		recipient := waitRecipient(t, tg, id, slug, 5*time.Minute, func(r map[string]any) bool {
			return str(r, "routing.state") == "filtered"
		})
		if field(recipient, "execution") != nil {
			t.Errorf("a filtered event reached the handler: %s", compact(recipient))
		}
	})
	s.check("dead_letter_and_replay", func(t *testing.T) {
		requireCapabilities(t, tg, "dead-letter-queues")
		id := prefix + "fail"
		tg.ok(t, time.Minute, "event", "publish", eventJourneySource, eventJourneyType,
			"--id", id, "--data", `{"deliver":true,"fail":true}`)
		// Every attempt fails, so the delivery spends the plan's retry
		// budget (25 attempts on Scale, 5 s apart) and is dead-lettered.
		var dead map[string]any
		eventually(t, "failed delivery in `gregale dlq list`", 15*time.Minute, 10*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "dlq", "list", slug, "--source", "invocation", "--limit", "200")
			if res.exit != 0 {
				return false, res.problem()
			}
			for _, event := range items(object(t, res.stdout), "events") {
				if strings.Contains(compact(field(event, "payload")), id) {
					dead = event
					return true, ""
				}
			}
			return false, "receipt: " + compact(recipientOf(eventReceipt(t, tg, id), slug))
		})
		if origin := str(dead, "origin"); origin != "event_subscription" {
			t.Errorf("dead letter origin %q, want event_subscription: %s", origin, compact(dead))
		}
		replayed := object(t, tg.ok(t, time.Minute, "dlq", "replay", slug, str(dead, "id")).stdout)
		if str(replayed, "replayed_at") == "" {
			t.Errorf("dlq replay did not record replayed_at: %s", compact(replayed))
		}
		t.Logf("receipt after replay: %s", compact(recipientOf(eventReceipt(t, tg, id), slug)))
	})
}

// requireCapabilities skips unless every capability is enabled for the
// account, quoting the server's reason.
func requireCapabilities(t *testing.T, tg *target, keys ...string) {
	t.Helper()
	view := object(t, tg.ok(t, time.Minute, "capabilities").stdout)
	byKey := map[string]map[string]any{}
	for _, capability := range items(view, "capabilities") {
		byKey[str(capability, "key")] = capability
	}
	for _, key := range keys {
		capability, ok := byKey[key]
		if !ok {
			t.Skipf("capability %s is not in this installation's catalog", key)
		}
		if field(capability, "enabled") != true {
			t.Skipf("capability %s is not enabled for this account (%s): %s", key,
				str(capability, "unavailable_reason"), str(capability, "unavailable_detail"))
		}
	}
}

// eventReceipt reads an event's receipt through `gregale event get`.
func eventReceipt(t *testing.T, tg *target, id string) map[string]any {
	t.Helper()
	return object(t, tg.ok(t, time.Minute, "event", "get", "--source", eventJourneySource, "--id", id).stdout)
}

// waitRecipient waits until the receipt's recipient for slug satisfies done.
func waitRecipient(t *testing.T, tg *target, id, slug string, timeout time.Duration, done func(map[string]any) bool) map[string]any {
	t.Helper()
	var recipient map[string]any
	eventually(t, "event "+id+" recipient "+slug, timeout, 3*time.Second, func() (bool, string) {
		recipient = recipientOf(eventReceipt(t, tg, id), slug)
		return recipient != nil && done(recipient), "recipient: " + compact(recipient)
	})
	return recipient
}

func recipientOf(receipt map[string]any, slug string) map[string]any {
	for _, r := range items(receipt, "recipients") {
		if str(r, "app_slug") == slug {
			return r
		}
	}
	return nil
}

// items reads a JSON array of objects at path.
func items(v any, path string) []map[string]any {
	list, _ := field(v, path).([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if doc, ok := item.(map[string]any); ok {
			out = append(out, doc)
		}
	}
	return out
}

// fixtureSource copies testdata/<name> into a fresh directory, so a deploy
// always sees the same files (and gets a build-cache hit).
func fixtureSource(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("testdata", name)
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	return dir
}
