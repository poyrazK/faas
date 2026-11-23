package journeys

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Managed Redis (ADR-20261116192126) is dark-launched: apid serves it only
// to accounts the managed-redis capability admits. The journey skips while
// the account's capabilities report it hidden.

// redisJourney is the state TestManagedRedis's steps share.
type redisJourney struct {
	tg      *target
	cache   string
	app     string
	base    string
	deleted bool
}

// TestManagedRedis is a customer's managed Redis journey through the CLI and
// an app's public URL: create a cache, bind it to an app, write through the
// app, then check the data survives the cache parking and waking, a
// restart, and a resize (which copies the data to a new VM).
func TestManagedRedis(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	skipHidden(t, hiddenCapabilities(t, tg), "managed-redis")
	j := &redisJourney{tg: tg}
	j.app = tg.app(t, "redis-app")
	j.base = tg.appURL(j.app)
	// The cache is an app of the account, so it takes an app slot too.
	tg.acquireAppSlot(t)
	j.cache = tg.slug("redis")
	t.Cleanup(func() {
		defer func() { <-tg.appSlots }()
		j.cleanupCache(t)
	})
	s := newStory(t)
	s.step("create_cache", j.createCache)
	s.step("bind_and_deploy", j.bindAndDeploy)
	s.step("write", j.write)
	s.check("data_kept_across_park_and_wake", j.parkAndWake)
	s.check("data_kept_across_restart", j.restart)
	s.check("data_kept_across_resize", j.resize)
}

func (j *redisJourney) createCache(t *testing.T) {
	res := j.tg.run(t, 5*time.Minute, "redis", "create", j.cache, "--memory", "64")
	if res.exit != 0 {
		if code := res.problemCode(); code == "managed_redis_not_in_plan" {
			t.Skipf("the journey account's plan has no managed Redis (%s)", res.problem())
		}
		t.Fatalf("redis create %s failed (exit %d): %s", j.cache, res.exit, res.problem())
	}
	doc := object(t, res.stdout)
	if str(doc, "host") == "" || num(doc, "port") != 6379 || num(doc, "memory_mb") != 64 {
		t.Fatalf("redis create returned an unexpected cache: %s", compact(doc))
	}
}

// bindAndDeploy deploys the probe app, binds the cache to it and redeploys,
// so the app starts with REDIS_URL set.
func (j *redisJourney) bindAndDeploy(t *testing.T) {
	src := redisProbeSource(t)
	j.tg.deploy(t, j.app, "--path", src)
	res := j.tg.ok(t, 2*time.Minute, "redis", "bind", j.cache, "--app", j.app)
	if doc := object(t, res.stdout); str(doc, "env_key") != "REDIS_URL" {
		t.Fatalf("redis bind returned an unexpected binding: %s", compact(doc))
	}
	j.tg.deploy(t, j.app, "--path", src, "--rollout", "immediate")
	eventually(t, "the app sees REDIS_URL", 3*time.Minute, 5*time.Second, func() (bool, string) {
		r := get(j.base + "/")
		if r.status != http.StatusOK {
			return false, r.String()
		}
		return strings.Contains(string(r.body), `"bound":true`), string(r.body)
	})
}

func (j *redisJourney) write(t *testing.T) {
	got := j.redis(t, "first write", "SET greeting hello", "SET counter 41", "INCR counter", "GET greeting")
	if got[0] != "+OK\r\n" || got[2] != ":42\r\n" || got[3] != "$5\r\nhello\r\n" {
		t.Fatalf("first write: %q", got)
	}
}

// parkAndWake parks the cache and reads through it: the next connection
// wakes it from its memory snapshot with the data in place.
func (j *redisJourney) parkAndWake(t *testing.T) {
	j.tg.ok(t, 2*time.Minute, "app", "park", j.cache)
	waitParked(t, j.tg, j.cache)
	j.expectKept(t, "after park and wake")
}

func (j *redisJourney) restart(t *testing.T) {
	j.tg.ok(t, 2*time.Minute, "redis", "restart", j.cache)
	j.expectKept(t, "after restart")
}

// resize grows the cache: a new VM copies the data from the running one
// before it serves, so the data is kept and the new size is in force.
func (j *redisJourney) resize(t *testing.T) {
	res := j.tg.ok(t, 3*time.Minute, "redis", "resize", j.cache, "--memory", "96")
	doc := object(t, res.stdout)
	if num(doc, "cache.memory_mb") != 96 || str(doc, "deployment_id") == "" {
		t.Fatalf("redis resize returned an unexpected change: %s", compact(doc))
	}
	want := "maxmemory:" + strconv.Itoa(96<<20)
	// The whole handoff (build, prime, copy, cutover) bounds this wait. On
	// prod it reached live 24 s after the resize; a gradual rollout kept the
	// old VM serving for 7m40s, past the handoff's 300 s write pause
	// (2026-11-23), so a handoff slower than 3 minutes fails.
	eventually(t, "the cache runs at 96 MB", 3*time.Minute, 5*time.Second, func() (bool, string) {
		replies, problem := j.try("INFO memory")
		if problem != "" {
			return false, problem
		}
		return strings.Contains(replies[0], want+"\r\n"), "no " + want + " yet"
	})
	j.expectKept(t, "after resize")
}

// expectKept reads both keys the write step set.
func (j *redisJourney) expectKept(t *testing.T, step string) {
	got := j.redis(t, step, "GET greeting", "GET counter")
	if got[0] != "$5\r\nhello\r\n" || got[1] != "$2\r\n42\r\n" {
		t.Fatalf("%s: %q, want greeting=hello and counter=42 kept", step, got)
	}
}

// redis runs commands through the probe app, retrying while the app or the
// cache wakes or switches VMs.
func (j *redisJourney) redis(t *testing.T, step string, cmds ...string) []string {
	t.Helper()
	var replies []string
	eventually(t, step, 5*time.Minute, 5*time.Second, func() (bool, string) {
		got, problem := j.try(cmds...)
		replies = got
		return problem == "", problem
	})
	return replies
}

func (j *redisJourney) try(cmds ...string) ([]string, string) {
	r := get(j.base + "/redis?" + url.Values{"cmd": cmds}.Encode())
	if r.status != http.StatusOK {
		return nil, r.String()
	}
	var doc struct {
		Replies []string `json:"replies"`
	}
	if err := json.Unmarshal(r.body, &doc); err != nil || len(doc.Replies) != len(cmds) {
		return nil, "unexpected probe reply: " + r.String()
	}
	return doc.Replies, ""
}

func (j *redisJourney) cleanupCache(t *testing.T) {
	if j.deleted || (j.tg.keep && t.Failed()) {
		return
	}
	if res := j.tg.run(t, 5*time.Minute, "redis", "delete", j.cache, "--yes"); res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
		t.Logf("warning: cleanup could not delete the cache %s (%s); the next run's sweeper removes it", j.cache, res.problem())
	}
	j.deleted = true
}

// redisProbeSource copies testdata/redis-probe into a fresh directory, so
// its source digest is fixed and later runs hit the build cache.
func redisProbeSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("testdata", "redis-probe")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read redis probe source: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read redis probe source: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatalf("write redis probe source: %v", err)
		}
	}
	return dir
}
