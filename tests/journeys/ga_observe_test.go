package journeys

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// GA inventory section G (observability) journeys beyond the core
// lifecycle: live tail, request analytics, account metrics, the log
// archive of a parked instance, the wake timeline and per-app costs.

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
