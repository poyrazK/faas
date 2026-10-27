package journeys

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestReleaseAndRollback redeploys with an immediate rollout, checks the
// history lists both revisions, and rolls back to the first (rows 1, 24).
// The default safe rollout has its own journey because it takes ~20 min on
// a low-traffic app.
func TestReleaseAndRollback(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	slug := tg.app(t, "release")
	base := tg.appURL(slug)
	s := newStory(t)

	var v1 map[string]any
	s.step("deploy_v1", func(t *testing.T) {
		v1 = tg.deployVersion(t, slug, "v1")
		if u := str(v1, "app_url"); u != "" {
			base = strings.TrimRight(u, "/")
		}
		expectVersion(t, base, "v1", time.Minute)
	})
	s.step("redeploy_v2_immediate", func(t *testing.T) {
		tg.deployVersion(t, slug, "v2", "--rollout", "immediate")
		expectVersion(t, base, "v2", 2*time.Minute)
	})
	s.check("deployment_history", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "deployments", "--app", slug).stdout)
		items, _ := field(doc, "items").([]any)
		if len(items) < 2 {
			t.Fatalf("deployments lists %d revisions after two deploys", len(items))
		}
	})
	s.step("rollback_to_v1", func(t *testing.T) {
		tg.ok(t, 27*time.Minute, "rollback", slug, "--to", str(v1, "id"),
			"--reason", "e2e-journeys run "+tg.runID, "--wait", "--timeout", "25m")
		expectVersion(t, base, "v1", 2*time.Minute)
	})
}

// TestSafeReleaseZeroDowntime redeploys under the default safe rollout
// while probing the app every second: no request may fail (rows 24, 38).
func TestSafeReleaseZeroDowntime(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "safe")
	probe := watchContinuity(base + "/")
	s := newStory(t)

	s.step("safe_redeploy_v2", func(t *testing.T) {
		defer probe.stop()
		tg.deployVersion(t, slug, "v2")
		expectVersion(t, base, "v2", 2*time.Minute)
	})
	// Its own check so an availability gap is reported separately from
	// whether the release completed.
	s.check("no_failed_requests", func(t *testing.T) {
		failed, total, byCode, samples := probe.result()
		t.Logf("metric redeploy_probe_failures=%d", failed)
		if failed > 0 {
			t.Errorf("app was unavailable during the safe redeploy: %d of %d probes failed %v; first: %s",
				failed, total, byCode, strings.Join(samples, "; "))
		}
	})
}

// TestHealthGate deploys a release whose health check never passes: the
// deploy must fail and the previous release keep serving (row 47).
func TestHealthGate(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "gate")
	s := newStory(t)

	s.step("unhealthy_release_refused", func(t *testing.T) {
		watch := watchContinuity(base + "/")
		tg.deploySlots <- struct{}{}
		res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--name", slug,
			"--path", probeSource(t, "broken"), "--wait", "--timeout", deployLimit.String(),
			"--yes", "--no-require-authn", "--reason", "e2e-journeys run "+tg.runID)
		<-tg.deploySlots
		watch.stop()
		if res.exit == 0 {
			t.Fatalf("a release whose /healthz answers 503 deployed: %s", tail(string(res.stdout), 800))
		}
		if docs, err := decodeObjects(res.stdout); err == nil && len(docs) == 1 {
			t.Logf("refused: status=%s rollout_state=%s reason=%s", str(docs[0], "status"),
				str(docs[0], "rollout_state"), str(docs[0], "rollout_aborted_reason"))
		} else {
			t.Logf("refused: %s", res.problem())
		}
		if failed, total, byCode, samples := watch.result(); failed > 0 {
			t.Errorf("v1 stopped serving while the unhealthy release was refused: %d of %d probes failed %v; first: %s",
				failed, total, byCode, strings.Join(samples, "; "))
		}
	})
	s.check("previous_release_serves", func(t *testing.T) {
		expectVersion(t, base, "v1", time.Minute)
	})
}

// TestDeleteAndRestore deletes an app, checks its URL stops serving, and
// restores it within the grace window (row 1).
func TestDeleteAndRestore(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "restore")
	s := newStory(t)

	s.step("delete", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "apps", "rm", slug, "--yes")
		eventually(t, "deleted app stops serving", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/")
			return r.status == http.StatusNotFound, r.String()
		})
	})
	s.step("restore", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "apps", "restore", slug)
		shown := object(t, tg.ok(t, time.Minute, "app", slug).stdout)
		if str(shown, "slug") != slug {
			t.Fatalf("restored app reads back as %s", compact(shown))
		}
		t.Logf("after restore: status=%s availability=%s", str(shown, "status"), str(shown, "deployment_availability"))
	})
	s.check("restored_app_serves", func(t *testing.T) {
		expectVersion(t, base, "v1", 3*time.Minute)
	})
}
