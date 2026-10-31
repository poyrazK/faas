package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestTrafficSplitAndRollback is the traffic split journey (row 25): stage
// v2 with no production traffic, split traffic 50/50 with v1, keep one
// caller on one revision with a version key, refuse a promotion whose
// serving release changed, promote v2, and, on a plan with automatic 5xx
// rollback, watch a v3 that fails its requests roll back to v2.
func TestTrafficSplitAndRollback(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "split")
	base := tg.appURL(slug)
	s := newStory(t)

	var v1, v2 string
	s.step("deploy_v1", func(t *testing.T) {
		receipt := tg.deployVersion(t, slug, "v1")
		v1 = str(receipt, "id")
		if u := str(receipt, "app_url"); u != "" {
			base = strings.TrimRight(u, "/")
		}
		expectVersion(t, base, "v1", time.Minute)
	})
	var staged map[string]any
	s.step("stage_v2_without_traffic", func(t *testing.T) {
		// Parked first, as an idle app would be: production then has to wake
		// v1 beside the staged v2's instance.
		tg.ok(t, 2*time.Minute, "app", "park", slug)
		staged = tg.deployVersion(t, slug, "v2", "--no-traffic")
		v2 = str(staged, "id")
	})
	s.check("production_wakes_v1_while_staged", func(t *testing.T) {
		if seen := servedVersions(base, 10, nil); seen["v1"] != 10 {
			t.Fatalf("production served %v while v2 was staged with no traffic", seen)
		}
	})
	s.check("staged_preview_serves_v2", func(t *testing.T) {
		preview := strings.TrimRight(str(staged, "preview_url"), "/")
		if preview == "" {
			t.Skip("the staged receipt has no preview_url (the platform's preview zone is off)")
		}
		expectVersion(t, preview, "v2", 2*time.Minute)
	})
	s.step("split_50_50", func(t *testing.T) {
		args := []string{"release", "traffic", "set", slug, "--deployment", v2, "--percent", "50"}
		res := tg.run(t, time.Minute, args...)
		if res.problemCode() == "capacity_unavailable" {
			// Reported, not hidden, like a deploy aborted for capacity.
			t.Logf("warning: traffic set answered capacity_unavailable (%s); retrying once in 30s", res.problem())
			time.Sleep(30 * time.Second)
			res = tg.run(t, time.Minute, args...)
		}
		if res.exit != 0 {
			t.Fatalf("traffic set failed (exit %d): %s", res.exit, res.problem())
		}
		set := object(t, res.stdout)
		if num(set, "traffic_percent") != 50 {
			t.Fatalf("traffic set answered %s", compact(map[string]any{"id": field(set, "id"), "traffic_percent": field(set, "traffic_percent")}))
		}
		if weights := trafficWeights(t, tg, slug); weights[v1] != 50 || weights[v2] != 50 {
			t.Fatalf("traffic status after a 50/50 split: %v (v1 %s, v2 %s)", weights, v1, v2)
		}
	})
	s.check("split_serves_both", func(t *testing.T) {
		total := map[string]int{}
		eventually(t, "both revisions serve production", 3*time.Minute, 2*time.Second, func() (bool, string) {
			for version, n := range servedVersions(base, 10, nil) {
				total[version] += n
			}
			return total["v1"] > 0 && total["v2"] > 0, fmt.Sprint(total)
		})
		t.Logf("metric split_responses_v1=%d", total["v1"])
		t.Logf("metric split_responses_v2=%d", total["v2"])
	})
	s.check("version_key_keeps_one_revision", func(t *testing.T) {
		header := map[string]string{"Gregale-Version-Key": "e2ej-" + tg.runID + "-caller"}
		if seen := servedVersions(base, 12, header); len(seen) != 1 {
			t.Fatalf("one version key was served by %v", seen)
		}
	})
	s.check("promote_refuses_changed_serving", func(t *testing.T) {
		res := tg.run(t, time.Minute, "release", "traffic", "promote", slug, "--deployment", v2, "--if-serving", v1)
		if res.exit == 0 || res.problemCode() != "traffic_serving_changed" {
			t.Fatalf("promotion while v1 serves only half the traffic: exit %d, %s", res.exit, res.problem())
		}
	})
	s.step("promote_v2", func(t *testing.T) {
		receipt := object(t, tg.ok(t, time.Minute, "release", "traffic", "promote", slug, "--deployment", v2).stdout)
		if num(receipt, "to_percent") != 100 {
			t.Fatalf("promote answered %s", compact(receipt))
		}
		eventually(t, "every request serves v2", 2*time.Minute, 2*time.Second, func() (bool, string) {
			seen := servedVersions(base, 10, nil)
			return seen["v2"] == 10, fmt.Sprint(seen)
		})
	})
	s.step("rollback_on_5xx", func(t *testing.T) {
		plan := strings.ToLower(str(object(t, tg.ok(t, time.Minute, "whoami").stdout), "plan"))
		if plan != "pro" && plan != "scale" {
			t.Skipf("automatic 5xx rollback needs a Pro or Scale plan; the account is on %q", plan)
		}
		// --rollout cannot be combined with --rollback-on-5xx, so the app's
		// release policy makes v3 take all traffic at once.
		tg.ok(t, time.Minute, "app", slug, "--release-policy", "immediate")
		tg.deployVersion(t, slug, "v3", "--rollback-on-5xx")
		expectVersion(t, base, "v3", 2*time.Minute)
		// The guard rolls back once at least 5 of the release's responses
		// are 5xx and they are at least half its requests, so nothing else
		// may reach v3 until the rollback is seen in the traffic status.
		for i := range 20 {
			if r := get(base + "/status?code=500"); r.status != http.StatusInternalServerError {
				t.Fatalf("request %d to /status?code=500: %s", i+1, r)
			}
		}
		eventually(t, "v2 serves all traffic again", 5*time.Minute, 10*time.Second, func() (bool, string) {
			weights := trafficWeights(t, tg, slug)
			return weights[v2] == 100, fmt.Sprint(weights)
		})
		expectVersion(t, base, "v2", 2*time.Minute)
	})
}

// servedVersions sends n requests to base and counts the version each
// answer names; a failed request counts under its status and error code.
func servedVersions(base string, n int, header map[string]string) map[string]int {
	seen := map[string]int{}
	for range n {
		r := fetch(http.MethodGet, base+"/", header)
		var doc struct {
			Version string `json:"version"`
		}
		switch {
		case r.err != nil:
			seen["transport error"]++
		case r.status != http.StatusOK || json.Unmarshal(r.body, &doc) != nil || doc.Version == "":
			seen[strings.TrimSpace(fmt.Sprintf("HTTP %d %s", r.status, r.header.Get("X-Faas-Error-Code")))]++
		default:
			seen[doc.Version]++
		}
	}
	return seen
}

// trafficWeights reads `release traffic status`: the traffic percent of
// each live deployment, by id.
func trafficWeights(t *testing.T, tg *target, slug string) map[string]int {
	t.Helper()
	doc := object(t, tg.ok(t, time.Minute, "release", "traffic", "status", slug).stdout)
	weights := map[string]int{}
	deployments, _ := field(doc, "deployments").([]any)
	for _, d := range deployments {
		weights[str(d, "id")] = int(num(d, "traffic_percent"))
	}
	return weights
}
