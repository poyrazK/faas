package journeys

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestServeAndObserve serves, streams and observes the shared probe app:
// HTTP, server-sent events, runtime and HTTP request logs, and metrics
// (inventory rows 2, 3, 64, 118, 121).
func TestServeAndObserve(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	slug, base := tg.sharedProbe(t)
	s := newStory(t)

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
	if tg.tier == tierFull {
		s.check("metrics", func(t *testing.T) {
			res := tg.ok(t, time.Minute, "metrics", slug, "--range", "15m")
			if len(objects(t, res.stdout)) == 0 {
				t.Fatal("gregale metrics printed nothing")
			}
		})
	}
}

// TestCronDelivery fires a cron on the shared probe app and checks the app
// received the POST (inventory row 92).
func TestCronDelivery(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.sharedProbe(t)
	created := object(t, tg.ok(t, 2*time.Minute, "crons", "add", "--app", slug,
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
}

// TestSecrets seals a secret, delivers it with a restart and checks the
// app sees it while no CLI listing prints its value (rows 60, 61).
func TestSecrets(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "secrets")
	value := randomHex(t, 32)
	sum := sha256.Sum256([]byte(value))
	want := hex.EncodeToString(sum[:])
	s := newStory(t)

	s.step("set_and_deliver", func(t *testing.T) {
		tg.ok(t, 5*time.Minute, "secrets", "set", "--app", slug, "E2E_SECRET="+value, "--restart")
		eventually(t, "secret delivered to the app", 3*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/env?key=E2E_SECRET")
			if r.status != http.StatusOK {
				return false, r.String()
			}
			doc := jsonBody(t, r)
			return str(doc, "sha256") == want, "app sees " + compact(doc)
		})
	})
	s.check("list_hides_value", func(t *testing.T) {
		listed := tg.ok(t, time.Minute, "secrets", "list", "--app", slug)
		if !strings.Contains(string(listed.stdout), "E2E_SECRET") {
			t.Errorf("secrets list does not show E2E_SECRET: %s", tail(string(listed.stdout), 300))
		}
		if strings.Contains(string(listed.stdout), value) {
			t.Errorf("secrets list printed the secret value")
		}
	})
	if tg.tier == tierFull {
		s.check("env_history", func(t *testing.T) {
			res := tg.ok(t, time.Minute, "env", "history", "--app", slug)
			if !strings.Contains(string(res.stdout), "E2E_SECRET") {
				t.Errorf("env history does not mention E2E_SECRET: %s", tail(string(res.stdout), 400))
			}
			if strings.Contains(string(res.stdout), value) {
				t.Errorf("env history printed the secret value")
			}
		})
	}
}

// TestScaleToZero parks an app and wakes it by request and by command,
// checks `gregale ps` agrees, and exercises min instances and a fresh
// restart (rows 38, 42, 43, 46).
func TestScaleToZero(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "scale")
	s := newStory(t)

	s.step("park_and_wake_on_request", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "park", slug)
		waitParked(t, tg, slug)
		r := get(base + "/")
		if r.status != http.StatusOK || str(jsonBody(t, r), "version") != "v1" {
			t.Fatalf("first request after park: %s", r)
		}
		t.Logf("metric wake_first_request_ms=%d", r.elapsed.Milliseconds())
	})
	s.check("ps_shows_woken_instance", func(t *testing.T) {
		eventually(t, "`gregale ps` lists the woken instance", 30*time.Second, 2*time.Second, func() (bool, string) {
			if get(base+"/").status != http.StatusOK {
				return false, "app stopped answering"
			}
			n := liveInstances(t, tg, slug)
			return n > 0, fmt.Sprintf("app answers but ps lists %d live instance(s)", n)
		})
	})
	if tg.tier != tierFull {
		return
	}
	s.check("manual_wake", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "park", slug)
		waitParked(t, tg, slug)
		tg.ok(t, 3*time.Minute, "wake", slug, "--wait", "--timeout", "2m")
		eventually(t, "`gregale ps` lists the woken instance", 30*time.Second, 2*time.Second, func() (bool, string) {
			n := liveInstances(t, tg, slug)
			return n > 0, fmt.Sprintf("`wake --wait` returned but ps lists %d live instance(s)", n)
		})
	})
	s.check("min_instances", func(t *testing.T) {
		shown := object(t, tg.ok(t, time.Minute, "app", slug, "--min", "1").stdout)
		t.Cleanup(func() { tg.run(t, time.Minute, "app", slug, "--min", "0") })
		if num(shown, "min_instances") != 1 {
			t.Fatalf("--min 1 reads back as %s", compact(field(shown, "min_instances")))
		}
		eventually(t, "a warm instance under min 1", 2*time.Minute, 3*time.Second, func() (bool, string) {
			n := liveInstances(t, tg, slug)
			return n > 0, fmt.Sprintf("ps lists %d live instance(s)", n)
		})
		if back := object(t, tg.ok(t, time.Minute, "app", slug, "--min", "0").stdout); num(back, "min_instances") != 0 {
			t.Fatalf("--min 0 reads back as %s", compact(field(back, "min_instances")))
		}
	})
	s.check("fresh_restart", func(t *testing.T) {
		tg.ok(t, 12*time.Minute, "app", slug, "restart", "--fresh", "--wait", "--timeout", "10m")
		expectVersion(t, base, "v1", 2*time.Minute)
	})
}

// TestMaintenanceAndAccess turns on maintenance mode, per-app token
// authentication and basic auth, checks the edge enforces each, and turns
// them off again (row 77).
func TestMaintenanceAndAccess(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "access")
	t.Cleanup(func() {
		tg.run(t, time.Minute, "app", slug, "--no-maintenance", "--no-require-authn", "--public-auth", "open")
	})
	s := newStory(t)

	s.check("maintenance_mode", func(t *testing.T) {
		tg.ok(t, time.Minute, "app", slug, "--maintenance")
		eventually(t, "maintenance 503", time.Minute, 2*time.Second, func() (bool, string) {
			r := get(base + "/")
			return r.status == http.StatusServiceUnavailable && strings.Contains(string(r.body), "app_maintenance_mode"), r.String()
		})
		tg.ok(t, time.Minute, "app", slug, "--no-maintenance")
		expectVersion(t, base, "v1", time.Minute)
	})
	s.check("require_authn", func(t *testing.T) {
		tg.ok(t, time.Minute, "app", slug, "--require-authn")
		eventually(t, "anonymous request refused", time.Minute, 2*time.Second, func() (bool, string) {
			r := get(base + "/")
			return r.status == http.StatusUnauthorized, r.String()
		})
		tg.ok(t, time.Minute, "app", slug, "--no-require-authn")
		expectVersion(t, base, "v1", time.Minute)
	})
	s.check("basic_auth", func(t *testing.T) {
		user, pass := "e2e", randomHex(t, 16)
		tg.ok(t, time.Minute, "app", slug, "--public-auth", "basic", "--basic-user", user, "--basic-pass", pass)
		eventually(t, "anonymous request refused", time.Minute, 2*time.Second, func() (bool, string) {
			r := get(base + "/")
			return r.status == http.StatusUnauthorized, r.String()
		})
		creds := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		eventually(t, "basic credentials admitted", time.Minute, 2*time.Second, func() (bool, string) {
			r := fetch(http.MethodGet, base+"/", map[string]string{"Authorization": "Basic " + creds})
			return r.status == http.StatusOK, r.String()
		})
		tg.ok(t, time.Minute, "app", slug, "--public-auth", "open")
		expectVersion(t, base, "v1", time.Minute)
	})
}

// TestImageDeploy deploys a digest-pinned public image without a build
// (row 4). The image is the one production already serves for nginx.
func TestImageDeploy(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "image")
	receipt := tg.deploy(t, slug, "--image", probeImage)
	appURL := strings.TrimRight(str(receipt, "app_url"), "/")
	if appURL == "" {
		appURL = tg.appURL(slug)
	}
	eventually(t, "the image serves its page", time.Minute, 2*time.Second, func() (bool, string) {
		r := get(appURL + "/")
		return r.status == http.StatusOK && strings.Contains(strings.ToLower(string(r.body)), "nginx"), r.String()
	})
}

// probeImage is a digest-pinned public image with a known-good deployment
// on production (an unprivileged nginx listening on 8080).
const probeImage = "docker.io/nginxinc/nginx-unprivileged@sha256:65e3e85dbaed8ba248841d9d58a899b6197106c23cb0ff1a132b7bfe0547e4c0"

// deployProbe creates an app of its own for a journey that changes it and
// deploys probe v1, returning its slug and base URL.
func (tg *target) deployProbe(t *testing.T, name string) (slug, base string) {
	t.Helper()
	slug = tg.app(t, name)
	receipt := tg.deployVersion(t, slug, "v1")
	base = strings.TrimRight(str(receipt, "app_url"), "/")
	if base == "" {
		base = tg.appURL(slug)
	}
	return slug, base
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
	mu      sync.Mutex
	probes  int
	failed  int
	byCode  map[string]int // problem code (or HTTP status) -> count
	samples []string       // the first few failures, verbatim
	done    chan struct{}
	stopped sync.Once
	wg      sync.WaitGroup
}

func watchContinuity(target string) *continuity {
	c := &continuity{done: make(chan struct{}), byCode: map[string]int{}}
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
				if r.status != http.StatusOK {
					c.failed++
					c.byCode[failureCode(r)]++
					if len(c.samples) < 5 {
						c.samples = append(c.samples, time.Now().UTC().Format("15:04:05")+" "+r.String())
					}
				}
				c.mu.Unlock()
			}
		}
	}()
	return c
}

func (c *continuity) stop() {
	c.stopped.Do(func() { close(c.done) })
	c.wg.Wait()
}

// result reports failed and total probes, failures by code, and samples.
// A nil continuity (the redeploy never started) reports nothing.
func (c *continuity) result() (failed, total int, byCode map[string]int, samples []string) {
	if c == nil {
		return 0, 0, nil, nil
	}
	c.stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed, c.probes, c.byCode, c.samples
}

// failureCode names a failed probe by its RFC 7807 code, else its status.
func failureCode(r httpResult) string {
	if r.err != nil {
		return "transport_error"
	}
	var p struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(r.body, &p) == nil && p.Code != "" {
		return p.Code
	}
	return fmt.Sprintf("http_%d", r.status)
}
