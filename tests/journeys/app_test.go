package journeys

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAppLifecycle walks one source-deployed app through a customer's
// first day: deploy, serve, stream, logs, secrets, crons, maintenance,
// park and wake, redeploy without downtime, rollback, delete and restore
// (inventory rows 1, 2, 3, 24, 38, 42, 46, 60, 61, 64, 92, 118, 121).
func TestAppLifecycle(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	full := tg.tier == tierFull
	slug := tg.app(t, "probe")
	base := tg.appURL(slug)
	s := newStory(t)

	var v1 map[string]any
	s.step("deploy_v1", func(t *testing.T) {
		v1 = tg.deploy(t, slug, "--path", probeSource(t, "v1"))
		if u := str(v1, "app_url"); u != "" {
			base = strings.TrimRight(u, "/")
		}
	})
	s.step("serve", func(t *testing.T) {
		expectVersion(t, base, "v1", time.Minute)
		if r := get(base + "/healthz"); r.status != http.StatusOK {
			t.Fatalf("GET /healthz: %s", r)
		}
	})
	s.check("server_sent_events", func(t *testing.T) {
		r := fetch(http.MethodGet, base+"/sse", map[string]string{"Accept": "text/event-stream"})
		if r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") ||
			!strings.Contains(string(r.body), "data: tick 3") {
			t.Fatalf("GET /sse: %s (content-type %q)", r, r.header.Get("Content-Type"))
		}
	})

	marker := "m-" + tg.runID + "-" + randomHex(t, 8)
	s.check("runtime_logs", func(t *testing.T) {
		if r := get(base + "/log?marker=" + marker); r.status != http.StatusOK {
			t.Fatalf("GET /log: %s", r)
		}
		eventually(t, "log line in `gregale logs`", 3*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "logs", slug, "--grep", marker, "--since", "15m")
			if res.exit != 0 {
				return false, res.problem()
			}
			return strings.Contains(string(res.stdout), marker), "marker not in output: " + tail(string(res.stdout), 300)
		})
	})
	s.check("http_request_logs", func(t *testing.T) {
		eventually(t, "request in `gregale logs --source http`", 3*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "logs", slug, "--source", "http", "--since", "15m", "--limit", "50")
			if res.exit != 0 {
				return false, res.problem()
			}
			return strings.Contains(string(res.stdout), "/log"), "no /log request in output: " + tail(string(res.stdout), 300)
		})
	})

	s.step("secrets", func(t *testing.T) {
		value := randomHex(t, 32)
		sum := sha256.Sum256([]byte(value))
		want := hex.EncodeToString(sum[:])
		tg.ok(t, 5*time.Minute, "secrets", "set", "--app", slug, "E2E_SECRET="+value, "--restart")
		eventually(t, "secret delivered to the app", 3*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/env?key=E2E_SECRET")
			if r.status != http.StatusOK {
				return false, r.String()
			}
			doc := jsonBody(t, r)
			return str(doc, "sha256") == want, "app sees " + compact(doc)
		})
		listed := tg.ok(t, time.Minute, "secrets", "list", "--app", slug)
		if !strings.Contains(string(listed.stdout), "E2E_SECRET") {
			t.Errorf("secrets list does not show E2E_SECRET: %s", tail(string(listed.stdout), 300))
		}
		if strings.Contains(string(listed.stdout), value) {
			t.Errorf("secrets list printed the secret value")
		}
	})

	if full {
		s.check("cron_delivery", func(t *testing.T) {
			created := object(t, tg.ok(t, time.Minute, "crons", "add", "--app", slug,
				"--schedule", "0 0 1 1 *", "--path", "/cron").stdout)
			id := str(created, "id")
			if id == "" {
				t.Fatalf("crons add printed no id: %s", compact(created))
			}
			t.Cleanup(func() { tg.run(t, time.Minute, "crons", "rm", id) })
			tg.ok(t, 2*time.Minute, "crons", "run", id)
			eventually(t, "manual cron fire reaches the app", 2*time.Minute, 3*time.Second, func() (bool, string) {
				r := get(base + "/cron-hits")
				if r.status != http.StatusOK {
					return false, r.String()
				}
				doc := jsonBody(t, r)
				return num(doc, "hits") >= 1, "app counted " + compact(doc)
			})
			runs := tg.run(t, time.Minute, "crons", "runs", id)
			t.Logf("crons runs: %s", tail(string(runs.stdout), 600))
		})
		s.check("maintenance_mode", func(t *testing.T) {
			tg.ok(t, time.Minute, "app", slug, "--maintenance")
			t.Cleanup(func() { tg.run(t, time.Minute, "app", slug, "--no-maintenance") })
			eventually(t, "maintenance 503", time.Minute, 2*time.Second, func() (bool, string) {
				r := get(base + "/")
				return r.status == http.StatusServiceUnavailable && strings.Contains(string(r.body), "app_maintenance_mode"), r.String()
			})
			tg.ok(t, time.Minute, "app", slug, "--no-maintenance")
			expectVersion(t, base, "v1", time.Minute)
		})
		s.check("metrics", func(t *testing.T) {
			res := tg.ok(t, time.Minute, "metrics", slug, "--range", "15m")
			if len(objects(t, res.stdout)) == 0 {
				t.Fatal("gregale metrics printed nothing")
			}
		})
	}

	s.step("park_and_wake_on_request", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "park", slug)
		waitParked(t, tg, slug)
		r := get(base + "/")
		if r.status != http.StatusOK || str(jsonBody(t, r), "version") != "v1" {
			t.Fatalf("first request after park: %s", r)
		}
		t.Logf("metric wake_first_request_ms=%d", r.elapsed.Milliseconds())
		for k, v := range r.header {
			if strings.HasPrefix(strings.ToLower(k), "x-faas-") || strings.HasPrefix(strings.ToLower(k), "x-gregale-") {
				t.Logf("wake response header %s: %s", k, strings.Join(v, ","))
			}
		}
		if n := liveInstances(t, tg, slug); n == 0 {
			t.Fatal("a request answered but `gregale ps` shows no live instance")
		}
	})
	if full {
		s.check("manual_wake", func(t *testing.T) {
			tg.ok(t, 2*time.Minute, "park", slug)
			waitParked(t, tg, slug)
			tg.ok(t, 3*time.Minute, "wake", slug, "--wait", "--timeout", "2m")
			if n := liveInstances(t, tg, slug); n == 0 {
				t.Fatal("`gregale wake --wait` returned but `gregale ps` shows no live instance")
			}
		})
	}

	s.step("redeploy_v2", func(t *testing.T) {
		var probe *continuity
		if full {
			probe = watchContinuity(base + "/")
		}
		tg.deploy(t, slug, "--path", probeSource(t, "v2"))
		if probe != nil {
			if failures := probe.stop(); len(failures) > 0 {
				t.Errorf("app was unavailable during redeploy (%d of %d probes failed): %s",
					len(failures), probe.count(), strings.Join(failures, "; "))
			}
		}
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
		tg.ok(t, 12*time.Minute, "rollback", slug, "--to", str(v1, "id"),
			"--reason", "e2e-journeys run "+tg.runID, "--wait", "--timeout", "10m")
		expectVersion(t, base, "v1", 2*time.Minute)
	})

	if full {
		s.check("delete_and_restore", func(t *testing.T) {
			tg.ok(t, 2*time.Minute, "apps", "rm", slug, "--yes")
			eventually(t, "deleted app stops serving", 2*time.Minute, 3*time.Second, func() (bool, string) {
				r := get(base + "/")
				return r.status == http.StatusNotFound, r.String()
			})
			tg.ok(t, 2*time.Minute, "apps", "restore", slug)
			shown := object(t, tg.ok(t, time.Minute, "app", slug).stdout)
			if str(shown, "slug") != slug {
				t.Fatalf("restored app reads back as %s", compact(shown))
			}
			t.Logf("after restore: status=%s availability=%s", str(shown, "status"), str(shown, "deployment_availability"))
		})
	}
}

func expectVersion(t *testing.T, base, version string, timeout time.Duration) {
	t.Helper()
	eventually(t, "GET / serves "+version, timeout, 2*time.Second, func() (bool, string) {
		r := get(base + "/")
		if r.status != http.StatusOK {
			return false, r.String()
		}
		doc := jsonBody(t, r)
		return str(doc, "version") == version, "serving " + compact(doc)
	})
}

// liveInstances counts `gregale ps` rows in a live state. ps lists only
// live instances, so a row without a recognised state still counts.
func liveInstances(t *testing.T, tg *target, slug string) int {
	t.Helper()
	live := 0
	for _, row := range objects(t, tg.ok(t, time.Minute, "ps", slug).stdout) {
		state := strings.ToLower(str(row, "state") + str(row, "status"))
		switch {
		case state == "", strings.Contains(state, "running"), strings.Contains(state, "waking"),
			strings.Contains(state, "cold_booting"), strings.Contains(state, "ready"):
			live++
		}
	}
	return live
}

func waitParked(t *testing.T, tg *target, slug string) {
	t.Helper()
	eventually(t, "no live instance after park", 2*time.Minute, 3*time.Second, func() (bool, string) {
		n := liveInstances(t, tg, slug)
		return n == 0, fmt.Sprintf("%d live instance(s)", n)
	})
}

// continuity polls a URL once a second and records every non-200 answer.
type continuity struct {
	mu       sync.Mutex
	probes   int
	failures []string
	done     chan struct{}
	wg       sync.WaitGroup
}

func watchContinuity(target string) *continuity {
	c := &continuity{done: make(chan struct{})}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-tick.C:
				r := get(target)
				c.mu.Lock()
				c.probes++
				if r.status != http.StatusOK && len(c.failures) < 10 {
					c.failures = append(c.failures, time.Now().UTC().Format("15:04:05")+" "+r.String())
				}
				c.mu.Unlock()
			}
		}
	}()
	return c
}

func (c *continuity) stop() []string {
	close(c.done)
	c.wg.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failures
}

func (c *continuity) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.probes
}
