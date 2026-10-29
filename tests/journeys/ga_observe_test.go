package journeys

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// GA inventory section G (observability) journeys beyond the core
// lifecycle: live tail, request analytics, account metrics, the log
// archive of a parked instance, the wake timeline and per-app costs, and
// Gregale Observe: alerts and notification channels, custom metrics and
// OTLP ingest, SLOs and synthetic checks.

// TestTailAnalyticsAndMetrics streams `gregale tail` while traffic flows to
// the shared probe, then reads request analytics and account metrics
// (rows 118, 121).
func TestTailAnalyticsAndMetrics(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.sharedProbe(t)
	s := newStory(t)

	s.check("tail", func(t *testing.T) {
		var wg sync.WaitGroup
		var res cliResult
		wg.Add(1)
		go func() {
			defer wg.Done()
			// tail streams until interrupted; the timeout ends it.
			res = tg.run(t, 45*time.Second, "tail", slug)
		}()
		time.Sleep(5 * time.Second)
		for range 10 {
			get(base + "/")
			time.Sleep(2 * time.Second)
		}
		wg.Wait()
		if len(strings.TrimSpace(string(res.stdout))) == 0 {
			t.Fatalf("gregale tail printed nothing during 10 requests: %s", res.problem())
		}
		t.Logf("tail: %s", tail(string(res.stdout), 400))
	})
	s.check("request_analytics", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "analytics", slug, "--since", "1h", "--by", "status")
		if len(objects(t, res.stdout)) == 0 {
			t.Fatal("gregale analytics printed nothing")
		}
		t.Logf("analytics: %s", tail(string(res.stdout), 400))
	})
	s.check("account_metrics", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "metrics", "--account", "--range", "1h")
		if len(objects(t, res.stdout)) == 0 {
			t.Fatal("gregale metrics --account printed nothing")
		}
	})
}

// TestLogArchive writes a log line, parks the app and reads the line back
// from the parked instance's retained logs: first through the parked-app
// fallback of `gregale logs`, then from the durable archive (row 118).
func TestLogArchive(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "archive")
	marker := "a-" + tg.runID + "-" + randomHex(t, 8)
	if r := get(base + "/log?marker=" + marker); r.status != http.StatusOK {
		t.Fatalf("GET /log: %s", r)
	}
	tg.ok(t, 2*time.Minute, "park", slug)
	waitParked(t, tg, slug)
	s := newStory(t)

	s.check("parked_app_logs", func(t *testing.T) {
		eventually(t, "marker in a parked app's logs", 6*time.Minute, 15*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "logs", slug, "--grep", marker, "--since", "1h")
			if res.exit != 0 {
				return false, res.problem()
			}
			return strings.Contains(string(res.stdout), marker), "not yet: " + tail(string(res.stdout), 200)
		})
	})
	s.check("archive", func(t *testing.T) {
		rows := objects(t, tg.ok(t, time.Minute, "ps", slug, "--all").stdout)
		if len(rows) == 0 {
			t.Fatal("ps --all lists no retained instance")
		}
		id, started := str(rows[0], "id"), str(rows[0], "started_at")
		day := time.Now().UTC().Format("2006-01-02")
		if len(started) >= 10 {
			day = started[:10]
		}
		eventually(t, "marker in the log archive", 6*time.Minute, 15*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "logs", slug, "--archive", "--instance", id, "--date", day)
			if res.exit != 0 {
				return false, res.problem()
			}
			return strings.Contains(string(res.stdout), marker), "not yet: " + tail(string(res.stdout), 200)
		})
	})
}

// TestWakeTimelineAndCosts wakes a parked app, reads the step-by-step
// timeline of that wake, and reads the app's attributed costs (rows 136,
// 140).
func TestWakeTimelineAndCosts(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "timeline")
	s := newStory(t)

	var wakeID string
	s.step("wake", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "park", slug)
		waitParked(t, tg, slug)
		r := get(base + "/")
		if r.status != http.StatusOK {
			t.Fatalf("wake request: %s", r)
		}
		wakeID = r.header.Get("x-faas-wake-id")
		eventually(t, "ps names the wake", 30*time.Second, 2*time.Second, func() (bool, string) {
			if wakeID != "" {
				return true, ""
			}
			for _, row := range objects(t, tg.ok(t, time.Minute, "ps", slug).stdout) {
				wakeID = str(row, "wake_id")
			}
			return wakeID != "", "no wake_id yet"
		})
	})
	s.check("wake_timeline", func(t *testing.T) {
		eventually(t, "wake timeline has events", 2*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "wake-timeline", slug, wakeID, "--all")
			if res.exit != 0 {
				return false, res.problem()
			}
			n := len(strings.TrimSpace(string(res.stdout)))
			return n > 0, fmt.Sprintf("%d bytes", n)
		})
	})
	s.check("app_costs", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "app", slug, "costs", "--json")
		doc := object(t, res.stdout)
		if len(doc) == 0 {
			t.Fatal("app costs printed an empty report")
		}
		t.Logf("costs: %s", tail(compact(doc), 400))
	})
}

// fakeSlackWebhook is a well-formed Slack incoming webhook no workspace
// owns: Slack refuses every post to it, so a journey's channel records its
// deliveries as failed without posting anywhere.
const fakeSlackWebhook = "https://hooks.slack.com/services/T00000000/B00000000/e2ejourneysnotarealhook"

// hiddenCapabilities is the account's hidden capabilities, read from
// `gregale capabilities`. A hidden capability's commands are unknown to
// the CLI and its API routes answer 404 (ADR-20261023115741), so a journey
// skips its steps instead of failing them.
func hiddenCapabilities(t *testing.T, tg *target) map[string]bool {
	t.Helper()
	doc := object(t, tg.ok(t, time.Minute, "capabilities").stdout)
	ids, _ := field(doc, "hidden_capabilities").([]any)
	hidden := make(map[string]bool, len(ids))
	for _, id := range ids {
		if s, ok := id.(string); ok {
			hidden[s] = true
		}
	}
	return hidden
}

// skipHidden skips a step whose capability is hidden for the account.
func skipHidden(t *testing.T, hidden map[string]bool, capability string) {
	t.Helper()
	if hidden[capability] {
		t.Skipf("%s is not released to this account (`gregale capabilities` lists it hidden)", capability)
	}
}

// skipUnreleased skips a step whose capability is not released to the
// account, or whose operator switch is off on this installation: that is
// the documented answer, not a platform failure.
func skipUnreleased(t *testing.T, code, problem string) {
	t.Helper()
	switch code {
	case "capability_not_released", "custom_metric_history_unavailable":
		t.Skipf("not available to this account: %s", problem)
	}
}

// listOut decodes a CLI list that prints as one JSON array (`alerts
// deliveries`, `channels list`), or as one object per line.
func listOut(t *testing.T, out []byte) []map[string]any {
	t.Helper()
	if !bytes.HasPrefix(bytes.TrimSpace(out), []byte("[")) {
		return objects(t, out)
	}
	var docs []map[string]any
	if err := json.Unmarshal(out, &docs); err != nil {
		t.Fatalf("decode CLI JSON array: %v\noutput: %s", err, tail(string(out), 2000))
	}
	return docs
}

// httpProblemCode is the RFC 7807 code of an API response, if any.
func httpProblemCode(r httpResult) string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(r.body, &p)
	return p.Code
}

// TestAlertsAndChannels adds an alert rule that sends a signed webhook to
// the probe's /hook and notifies a Slack channel, drives traffic past its
// threshold, and reads the rule firing, the delivery the probe verified
// and the channel's delivery record; then sends a channel test (row 125).
func TestAlertsAndChannels(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	hidden := hiddenCapabilities(t, tg)
	skipHidden(t, hidden, "alerts")
	slug, base := tg.deployProbe(t, "alerts")
	secret := randomHex(t, 16)
	s := newStory(t)

	s.step("hook_secret", func(t *testing.T) {
		tg.ok(t, 5*time.Minute, "secrets", "set", "--app", slug, "E2E_HOOK_SECRET="+secret, "--restart")
		eventually(t, "the probe reads the hook secret", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/secret?key=E2E_HOOK_SECRET")
			return r.status == http.StatusOK && strings.Contains(string(r.body), `"present":true`), r.String()
		})
	})
	var channelID string
	// Registered on the journey: a step's cleanup would remove the
	// channel before the rule notifies it.
	t.Cleanup(func() {
		if channelID != "" {
			tg.run(t, time.Minute, "channels", "rm", channelID)
		}
	})
	s.step("add_channel", func(t *testing.T) {
		skipHidden(t, hidden, "notification-channels")
		res := tg.execStdin(t, fakeSlackWebhook+"\n", time.Minute, "channels", "add",
			"--name", tg.slug("slack"), "--kind", "slack", "--secret-stdin")
		skipUnreleased(t, res.problemCode(), res.problem())
		if res.exit != 0 {
			t.Fatalf("channels add: %s", res.problem())
		}
		if channelID = str(object(t, res.stdout), "id"); channelID == "" {
			t.Fatalf("channels add printed no id: %s", tail(string(res.stdout), 400))
		}
	})
	var ruleID string
	s.step("add_rule", func(t *testing.T) {
		// Rule names are unique per account, and a deleted app's rules
		// keep theirs, so each run names its own.
		args := []string{"alerts", "add", "--app", slug, "--name", tg.slug("requests"),
			"--metric", "request_count", "--comparison", "gt", "--threshold", "5", "--window-spec", "5m",
			"--webhook-url", base + "/hook", "--webhook-secret-stdin"}
		if channelID != "" {
			args = append(args, "--channel", channelID)
		}
		res := tg.execStdin(t, secret+"\n", time.Minute, args...)
		skipUnreleased(t, res.problemCode(), res.problem())
		if res.exit != 0 {
			t.Fatalf("alerts add: %s", res.problem())
		}
		if ruleID = str(object(t, res.stdout), "id"); ruleID == "" {
			t.Fatalf("alerts add printed no id: %s", tail(string(res.stdout), 400))
		}
	})
	s.step("rule_fires", func(t *testing.T) {
		if ruleID == "" {
			t.Skip("no rule: alerts are not released to this account")
		}
		// 20 requests inside the rule's 5-minute window: request_count > 5.
		for range 20 {
			if r := get(base + "/"); r.status != http.StatusOK {
				t.Logf("warning: GET /: %s", r)
			}
		}
		eventually(t, "the rule fires", 8*time.Minute, 15*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "alerts", "info", "--app", slug, ruleID)
			if res.exit != 0 {
				return false, res.problem()
			}
			doc := object(t, res.stdout)
			return str(doc, "state") == "firing" || str(doc, "last_fired_at") != "", "state " + str(doc, "state")
		})
	})
	s.check("webhook_verified", func(t *testing.T) {
		if ruleID == "" {
			t.Skip("no rule: alerts are not released to this account")
		}
		// The probe answers 200 only when the signature verifies against
		// the rule's secret, 401 when it does not.
		eventually(t, "a delivery the probe verified", 5*time.Minute, 10*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "alerts", "deliveries", "--app", slug, "--limit", "10", ruleID)
			if res.exit != 0 {
				return false, res.problem()
			}
			var seen []string
			for _, d := range listOut(t, res.stdout) {
				if str(d, "status") == "delivered" && num(d, "last_status_code") == http.StatusOK {
					return true, ""
				}
				seen = append(seen, fmt.Sprintf("%s (HTTP %s) %s", str(d, "status"), str(d, "last_status_code"), str(d, "last_error")))
			}
			return false, "deliveries: [" + strings.Join(seen, "; ") + "]"
		})
	})
	s.check("channel_notified", func(t *testing.T) {
		if channelID == "" || ruleID == "" {
			t.Skip("no channel or rule: not released to this account")
		}
		// Slack refuses the fake destination, so the fire is recorded on
		// the channel as a failed delivery.
		eventually(t, "the fire reaches the channel", 5*time.Minute, 10*time.Second, func() (bool, string) {
			ch, why := findChannel(t, tg, channelID)
			if ch == nil {
				return false, why
			}
			return str(ch, "last_error_at") != "" || str(ch, "last_delivered_at") != "", "no delivery recorded: " + compact(ch)
		})
	})
	s.check("channel_test_send", func(t *testing.T) {
		if channelID == "" {
			t.Skip("no channel: notification channels are not released to this account")
		}
		doc := object(t, tg.ok(t, time.Minute, "channels", "test", channelID).stdout)
		if field(doc, "delivered") != false || str(doc, "error") == "" {
			t.Fatalf("a test to a destination Slack refuses must report it undelivered, with Slack's reason: %s", compact(doc))
		}
		t.Logf("test send: %s", compact(doc))
	})
}

// findChannel reads one notification channel from `gregale channels list`.
func findChannel(t *testing.T, tg *target, id string) (map[string]any, string) {
	t.Helper()
	res := tg.run(t, time.Minute, "channels", "list")
	if res.exit != 0 {
		return nil, res.problem()
	}
	for _, ch := range listOut(t, res.stdout) {
		if str(ch, "id") == id {
			return ch, ""
		}
	}
	return nil, "channel " + id + " is not listed"
}

// TestCustomMetricsSLOsAndSynthetics pushes custom metrics through the API
// and over OTLP and reads them back, defines an availability SLO and reads
// its status, and schedules a synthetic check and reads its first run
// (rows 122, 128, 129). A capability not released to the account skips its
// step.
func TestCustomMetricsSLOsAndSynthetics(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	hidden := hiddenCapabilities(t, tg)
	if hidden["custom-metrics"] && hidden["custom-slos"] && hidden["synthetic-checks"] {
		t.Skip("custom metrics, SLOs and synthetic checks are all hidden for this account")
	}
	slug, base := tg.deployProbe(t, "observe")
	// Minted on the journey so it outlives the steps; revoked at the end.
	key := tg.scopedKey(t, "metrics", "metrics:write", "apps:read")
	s := newStory(t)

	s.check("custom_metric", func(t *testing.T) {
		skipHidden(t, hidden, "custom-metrics")
		r := tg.apiCall(http.MethodPut, "/v1/apps/"+slug+"/custom-metrics/e2e_queue_depth", map[string]float64{"value": 7}, key)
		skipUnreleased(t, httpProblemCode(r), r.String())
		if r.status != http.StatusNoContent {
			t.Fatalf("push a custom metric: %s", r)
		}
		m, why := customMetric(t, tg, slug, key, "e2e_queue_depth")
		if m == nil {
			t.Fatal(why)
		}
		if num(m, "value") != 7 || field(m, "stale") == true {
			t.Fatalf("e2e_queue_depth = %s, want a fresh 7", compact(m))
		}
	})
	s.check("otlp_metrics", func(t *testing.T) {
		skipHidden(t, hidden, "custom-metric-history")
		// An OTLP/HTTP JSON export of one gauge; the dotted name is stored
		// as e2e_otlp_gauge.
		export := map[string]any{"resourceMetrics": []any{map[string]any{
			"scopeMetrics": []any{map[string]any{"metrics": []any{map[string]any{
				"name": "e2e.otlp.gauge",
				"gauge": map[string]any{"dataPoints": []any{map[string]any{
					"asDouble":     3,
					"timeUnixNano": strconv.FormatInt(time.Now().UnixNano(), 10),
				}}},
			}}}},
		}}}
		r := tg.apiCall(http.MethodPost, "/v1/apps/"+slug+"/otlp/v1/metrics", export, key)
		skipUnreleased(t, httpProblemCode(r), r.String())
		if r.status != http.StatusOK || strings.Contains(string(r.body), "rejectedDataPoints") {
			t.Fatalf("OTLP export: %s", r)
		}
		m, why := customMetric(t, tg, slug, key, "e2e_otlp_gauge")
		if m == nil {
			t.Fatal(why)
		}
		if num(m, "value") != 3 || str(m, "kind") != "gauge" {
			t.Fatalf("e2e_otlp_gauge = %s, want gauge 3", compact(m))
		}
	})
	s.check("slo", func(t *testing.T) {
		skipHidden(t, hidden, "custom-slos")
		res := tg.run(t, time.Minute, "slos", "create", "--app", slug, "--name", "availability",
			"--objective", "99", "--sli", "availability", "--window-days", "7")
		skipUnreleased(t, res.problemCode(), res.problem())
		if res.exit != 0 {
			t.Fatalf("slos create: %s", res.problem())
		}
		id := str(object(t, res.stdout), "id")
		if id == "" {
			t.Fatalf("slos create printed no id: %s", tail(string(res.stdout), 400))
		}
		doc := object(t, tg.ok(t, time.Minute, "slos", "status", "--app", slug, id).stdout)
		if str(doc, "id") != id || num(doc, "objective_pct") != 99 || str(doc, "sli") != "availability" {
			t.Fatalf("slos status: %s", compact(doc))
		}
		t.Logf("slo status: %s", compact(field(doc, "status")))
	})
	s.check("synthetic_check", func(t *testing.T) {
		skipHidden(t, hidden, "synthetic-checks")
		res := tg.run(t, time.Minute, "synthetics", "create", "--app", slug, "--name", "home",
			"--path", "/", "--expect-status", "200", "--every-minutes", "5")
		skipUnreleased(t, res.problemCode(), res.problem())
		if res.exit != 0 {
			t.Fatalf("synthetics create: %s", res.problem())
		}
		id := str(object(t, res.stdout), "id")
		if id == "" {
			t.Fatalf("synthetics create printed no id: %s", tail(string(res.stdout), 400))
		}
		// An always-on check on a test app must not outlive this step.
		t.Cleanup(func() { tg.run(t, time.Minute, "synthetics", "rm", "--app", slug, "--yes", id) })
		eventually(t, "the check's first run", 6*time.Minute, 15*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "synthetics", "status", "--app", slug, id)
			if res.exit != 0 {
				return false, res.problem()
			}
			recent, _ := field(object(t, res.stdout), "results.recent").([]any)
			if len(recent) == 0 {
				return false, "no run yet"
			}
			if field(recent[0], "ok") != true {
				t.Fatalf("the first run of GET %s/ failed: %s", base, compact(recent[0]))
			}
			return true, ""
		})
	})
}

// customMetric reads one of an app's custom metrics through the API.
func customMetric(t *testing.T, tg *target, slug, key, name string) (map[string]any, string) {
	t.Helper()
	r := tg.apiCall(http.MethodGet, "/v1/apps/"+slug+"/custom-metrics", nil, key)
	if r.status != http.StatusOK {
		return nil, "list custom metrics: " + r.String()
	}
	metrics, _ := field(jsonBody(t, r), "metrics").([]any)
	for _, m := range metrics {
		if doc, ok := m.(map[string]any); ok && str(doc, "name") == name {
			return doc, ""
		}
	}
	return nil, name + " is not listed: " + tail(string(r.body), 400)
}
