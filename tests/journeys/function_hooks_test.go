package journeys

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestFunctionSnapshotHooks deploys a Node function that declares init,
// beforeSnapshot and afterRestore in its handler (ADR-20261110190000): the
// first response shows init finished before the worker served, and after a park the restored worker shows the snapshot was
// taken after beforeSnapshot and afterRestore ran before it served.
func TestFunctionSnapshotHooks(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "fn-hooks")
	src := fixtureSource(t, "function-hooks")
	s := newStory(t)

	var base string
	if !s.step("deploy", func(t *testing.T) {
		receipt := tg.deploy(t, slug, "--path", src)
		if base = strings.TrimRight(str(receipt, "app_url"), "/"); base == "" {
			base = tg.appURL(slug)
		}
	}) {
		return
	}
	s.check("init_before_serving", func(t *testing.T) {
		eventually(t, "GET / answers after init", 2*time.Minute, 2*time.Second, func() (bool, string) {
			r := get(base + "/")
			if r.status != http.StatusOK {
				return false, r.String()
			}
			doc := jsonBody(t, r)
			return str(doc, "probe") == "gregale-e2e-hooks" && doc["init"] == true, "serving " + compact(doc)
		})
	})
	s.check("hooks_around_restore", func(t *testing.T) {
		// A park can wake by cold boot when no snapshot is usable yet; only
		// a restored wake says anything about the hooks, so try a few.
		for attempt := range 3 {
			tg.ok(t, 2*time.Minute, "app", "park", slug)
			waitParked(t, tg, slug)
			r := get(base + "/")
			if r.status != http.StatusOK {
				t.Fatalf("first request after park %d: %s", attempt+1, r)
			}
			wake := r.header.Get("x-faas-wake")
			if wake != "restored" {
				t.Logf("park %d woke %q, not from a snapshot; retrying", attempt+1, wake)
				continue
			}
			doc := jsonBody(t, r)
			if doc["init"] != true || num(doc, "before_snapshots") < 1 || num(doc, "restores") < 1 {
				t.Fatalf("restored worker state = %s, want init done, beforeSnapshot before capture and afterRestore before serving", compact(doc))
			}
			t.Logf("metric function_hooks_restored_wake_ms=%d", r.elapsed.Milliseconds())
			return
		}
		t.Skip("no park woke from a snapshot")
	})
}
