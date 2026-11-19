package journeys

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// devEditToLiveBound fails edit_to_live when an edit went live through a
// full build instead of a live patch. It leaves headroom over the target
// for public-network distance and the poll interval.
const devEditToLiveBound = 4 * api.DevEditToLiveTarget

// TestDevLoop is the remote developer loop: `gregale dev` builds a
// developer environment from a local checkout, an edit is live without a
// redeploy, `gregale dev list` finds the environment, and `--stop` removes
// it. The checkout carries a 1 MiB virtualenv file, which the sync must
// skip rather than upload on every edit. Without an API key (cli-login
// runs) the journey reads the protected environment through the CLI's
// localhost browser proxy, as a developer's browser would.
func TestDevLoop(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	s := newStory(t)
	name := tg.slug("devloop")
	src := probeSource(t, "dev-v1")
	venv := filepath.Join(src, ".venv", "lib")
	if err := os.MkdirAll(venv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venv, "big.bin"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	proxy := "http://localhost:" + strconv.Itoa(port)
	stop := tg.startDev(t, src, "dev", "--path", src, "--name", name, "--no-logs", "--ttl", "1h", "--proxy-port", strconv.Itoa(port))
	t.Cleanup(func() {
		stop()
		tg.run(t, 3*time.Minute, "dev", "--path", src, "--name", name, "--stop", "--yes")
	})

	var devURL string
	if !s.step("first_sync", func(t *testing.T) {
		// The first sync builds the environment, which takes a few minutes.
		eventually(t, "the developer environment serving dev-v1", 12*time.Minute, 10*time.Second, func() (bool, string) {
			devURL = tg.devSessionURL(t, name)
			if devURL == "" {
				return false, "not listed by `gregale dev list`"
			}
			r := tg.devGet(devURL, proxy, "/")
			return r.status == http.StatusOK && strings.Contains(string(r.body), "dev-v1"), r.String()
		})
	}) {
		return
	}
	if !s.step("edit_to_live", func(t *testing.T) {
		start := time.Now()
		if err := os.WriteFile(filepath.Join(src, "version.txt"), []byte("dev-v2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// A live patch serves the edit in seconds (6.8 s on prod
		// 2026-11-19). When the patch cannot hold, the edit lands only with
		// the full build (349 s on prod 2026-11-16), so the wait allows a
		// build and the step then fails on the live-patch bound below.
		eventually(t, "the edit serving without a redeploy", 10*time.Minute, 2*time.Second, func() (bool, string) {
			r := tg.devGet(devURL, proxy, "/")
			return r.status == http.StatusOK && strings.Contains(string(r.body), "dev-v2"), r.String()
		})
		elapsed := time.Since(start)
		t.Logf("metric dev_edit_to_live_s=%.1f", elapsed.Seconds())
		if elapsed > devEditToLiveBound {
			t.Fatalf("edit took %s to go live, over %s (4x the %s target): it landed with a full build, not a live patch",
				elapsed.Round(100*time.Millisecond), devEditToLiveBound, api.DevEditToLiveTarget)
		}
	}) {
		return
	}
	s.step("stop", func(t *testing.T) {
		stop()
		tg.ok(t, 3*time.Minute, "dev", "--path", src, "--name", name, "--stop", "--yes")
		eventually(t, "the environment gone from `gregale dev list`", 2*time.Minute, 5*time.Second, func() (bool, string) {
			return tg.devSessionURL(t, name) == "", "still listed"
		})
	})
}

// devSessionURL returns the URL `gregale dev list` shows for project, or ""
// when it is not listed.
func (tg *target) devSessionURL(t *testing.T, project string) string {
	t.Helper()
	res := tg.run(t, time.Minute, "dev", "list")
	if res.exit != 0 {
		return ""
	}
	sessions, _ := field(object(t, res.stdout), "sessions").([]any)
	for _, session := range sessions {
		if str(session, "project") == project && (str(session, "state") == "" || str(session, "state") == "open") {
			return strings.TrimRight(str(session, "url"), "/")
		}
	}
	return ""
}

// devGet reads path from a developer environment. With the journey's API
// key it calls the environment directly. Without one it goes through the
// CLI's localhost proxy, which signs requests for environments that require
// a key; an environment without that requirement starts no proxy and is
// read directly.
func (tg *target) devGet(devURL, proxy, path string) httpResult {
	if tg.token != "" {
		return fetch(http.MethodGet, devURL+path, map[string]string{"Authorization": "Bearer " + tg.token})
	}
	if r := get(proxy + path); r.err == nil {
		return r
	}
	return get(devURL + path)
}
