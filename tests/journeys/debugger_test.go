package journeys

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// debuggerPlanted prefixes every synthetic secret the debugger probe puts in
// its spans and logs (testdata/probe-debugger/debug-entry.mjs). No
// `gregale debug` output may ever contain it.
const debuggerPlanted = "e2e-planted"

// TestDebugger is the production debugger journey (ADR-127): a traced app
// serves traffic and a few server errors, and the customer reads it back
// through `gregale debug` — status, the request list, one request's
// metadata, span tree, evidence and explanation, the trace lookup,
// dependencies with scrubbed query patterns, coverage, grouped issues, the
// metadata-only export and the redacted incident bundle. None of it shows a
// planted secret. A second release that is slower on the same route must be
// reported as a latency regression within a few detector passes; the
// customer compares the two releases and acknowledges it.
func TestDebugger(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "debugger")
	first := tg.deploy(t, slug, "--path", debuggerProbe(t, "v1", 0))
	base := strings.TrimRight(str(first, "app_url"), "/")
	if base == "" {
		base = tg.appURL(slug)
	}
	fast := str(first, "id")
	j := &debuggerJourney{tg: tg, slug: slug}
	s := newStory(t)
	var requestID, traceID, slow string

	s.step("traffic", func(t *testing.T) {
		load(t, base+"/", 120, 4, http.StatusOK)
		load(t, base+"/status?code=500", 5, 1, http.StatusInternalServerError)
	})
	if !s.step("spans_arrive", func(t *testing.T) {
		// Spans travel guest → vmmd broker → apid; telemetry rows a little
		// behind the gateway. A tracing outage shows up here.
		eventually(t, "span evidence for the probe's requests", 3*time.Minute, 10*time.Second, func() (bool, string) {
			doc := j.read(t, "debug", "coverage", slug)
			return num(doc, "span_evidence.requests") >= 100 && num(doc, "error_requests") >= 5, compact(doc)
		})
	}) {
		return
	}
	s.check("status", func(t *testing.T) {
		doc := j.read(t, "debug", "status", slug)
		if num(doc, "health.server_errors") < 5 || str(doc, "verdict") == "healthy" {
			t.Fatalf("status does not report the 5xx responses: %s", compact(doc))
		}
		failures, _ := field(doc, "recent_failures").([]any)
		if len(failures) == 0 || str(failures[0], "route") != "GET /status" {
			t.Fatalf("recent failures: %s", compact(failures))
		}
	})
	if !s.step("find_request", func(t *testing.T) {
		doc := j.read(t, "debug", "requests", "list", slug, "--route", "/", "--status", "200", "--since", "30m")
		rows, _ := field(doc, "requests").([]any)
		for _, row := range rows {
			if str(row, "route") == "GET /" && str(row, "trace_id") != "" {
				requestID, traceID = str(row, "id"), str(row, "trace_id")
				return
			}
		}
		t.Fatalf("no traced GET / request listed: %s", compact(doc))
	}) {
		return
	}
	s.check("request_get", func(t *testing.T) {
		doc := j.read(t, "debug", "requests", "get", slug, requestID)
		if str(doc, "id") != requestID || str(doc, "deployment_id") != fast || num(doc, "status") != 200 {
			t.Fatalf("request: %s", compact(doc))
		}
	})
	s.check("span_tree", func(t *testing.T) {
		doc := j.read(t, "debug", "trace", traceID)
		spans, _ := field(doc, "spans").([]any)
		names := map[string]bool{}
		for _, sp := range spans {
			names[str(sp, "name")] = true
		}
		for _, want := range []string{"SELECT accounts", "GET api.example.com"} {
			if !names[want] {
				t.Fatalf("trace %s has no %q span: %s", traceID, want, compact(spans))
			}
		}
		matches, _ := field(doc, "matches").([]any)
		if len(matches) != 1 || str(matches[0], "app") != slug || str(matches[0], "request.id") != requestID {
			t.Fatalf("trace lookup matches: %s", compact(matches))
		}
	})
	s.check("evidence_and_explain", func(t *testing.T) {
		evidence := j.read(t, "debug", "requests", "evidence", slug, requestID)
		if str(evidence, "request.id") != requestID {
			t.Fatalf("evidence: %s", compact(evidence))
		}
		explained := j.read(t, "debug", "requests", "explain", slug, requestID)
		if findings, _ := field(explained, "findings").([]any); len(findings) == 0 || str(explained, "headline") == "" {
			t.Fatalf("explain: %s", compact(explained))
		}
	})
	s.check("dependencies", func(t *testing.T) {
		doc := j.read(t, "debug", "dependencies", slug)
		deps, _ := field(doc, "dependencies").([]any)
		var pattern string
		hosts := map[string]bool{}
		for _, d := range deps {
			hosts[str(d, "name")] = true
			if queries, _ := field(d, "queries").([]any); str(d, "name") == "SELECT accounts" && len(queries) > 0 {
				pattern = str(queries[0], "pattern")
			}
		}
		if !hosts["api.example.com"] {
			t.Fatalf("no api.example.com dependency: %s", compact(deps))
		}
		if pattern != "SELECT * FROM accounts WHERE email = ? AND token = ?" {
			t.Fatalf("query pattern %q: literals must be replaced with ?", pattern)
		}
	})
	s.check("issues", func(t *testing.T) {
		doc := j.read(t, "debug", "issue", "list", slug)
		items, _ := field(doc, "items").([]any)
		for _, it := range items {
			if strings.Contains(str(it, "title"), "/status") && num(it, "event_count") >= 1 {
				return
			}
		}
		t.Fatalf("the /status failures are not grouped into an issue: %s", compact(items))
	})
	s.check("export", func(t *testing.T) {
		res := j.cli(t, "debug", "requests", "export", slug, "--since", "30m", "--format", "ndjson", "--output", "-")
		// Similar requests collapse into one row carrying their count.
		represented := 0.0
		for _, row := range objects(t, res.stdout) {
			if str(row, "route") == "GET /" {
				represented += max(num(row, "count"), 1)
			}
		}
		if represented < 120 {
			t.Fatalf("export represents %.0f GET / requests, want the probe's 120", represented)
		}
	})
	s.check("bundle", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "bundle.json")
		j.read(t, "debug", "bundle", slug, requestID, "--output", out)
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read bundle: %v", err)
		}
		j.record(raw)
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil || str(doc, "schema_version") != "gregale.debug.bundle/v2" ||
			str(doc, "request.id") != requestID {
			t.Fatalf("bundle (%v): %s", err, tail(string(raw), 600))
		}
	})
	s.check("no_planted_values", func(t *testing.T) {
		if at := j.planted(); at != "" {
			t.Fatalf("a debugger output shows a planted secret: …%s…", at)
		}
	})

	s.step("slow_release", func(t *testing.T) {
		receipt := tg.deploy(t, slug, "--path", debuggerProbe(t, "v2", 400), "--rollout", "immediate")
		slow = str(receipt, "id")
		load(t, base+"/", 120, 4, http.StatusOK)
	})
	if !s.step("regression_detected", func(t *testing.T) {
		start := time.Now()
		// The detector runs every 5 minutes over the last 30; telemetry
		// lands within a minute, so two passes and margin.
		eventually(t, "a latency regression of GET / in the slow release", 13*time.Minute, 30*time.Second, func() (bool, string) {
			doc := j.read(t, "debug", "regressions", slug, "--since", "1h")
			rows, _ := field(doc, "regressions").([]any)
			for _, r := range rows {
				if str(r, "deployment_id") == slow && str(r, "route") == "GET /" && str(r, "kind") == "latency" {
					return str(r, "state") == "active" && num(r, "factor") >= 1.2, compact(r)
				}
			}
			return false, compact(rows)
		})
		t.Logf("metric debugger_regression_detect_s=%.0f", time.Since(start).Seconds())
	}) {
		return
	}
	s.check("compare", func(t *testing.T) {
		doc := j.read(t, "debug", "compare", slug, "--source", fast, "--mirror", slow, "--route", "/", "--since", "1h")
		if !strings.Contains(compact(doc), "GET /") {
			t.Fatalf("compare has no GET / row: %s", compact(doc))
		}
	})
	s.check("acknowledge", func(t *testing.T) {
		j.read(t, "debug", "regressions", "ack", slug, "--release", slow, "--route", "GET /")
		doc := j.read(t, "debug", "regressions", slug, "--since", "1h")
		rows, _ := field(doc, "regressions").([]any)
		for _, r := range rows {
			if str(r, "deployment_id") == slow && str(r, "route") == "GET /" {
				if state := str(r, "state"); state != "acknowledged" {
					t.Fatalf("regression state after ack = %q", state)
				}
				return
			}
		}
		t.Fatalf("acknowledged regression no longer listed: %s", compact(rows))
	})
	s.check("no_planted_values_after_rollout", func(t *testing.T) {
		if at := j.planted(); at != "" {
			t.Fatalf("a debugger output shows a planted secret: …%s…", at)
		}
	})
}

// TestDevDebugger is the developer debugger journey (ADR-741): `gregale dev
// --debug` syncs the probe into a developer environment and exposes its
// Node inspector on a local port; a CDP client evaluates code in the remote
// process; the session leaves dev.debugger.attached and .detached audit
// rows; and the raw tunnel refuses an app that is not a developer
// environment.
func TestDevDebugger(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	s := newStory(t)
	name := tg.slug("devdbg")
	src := debuggerProbe(t, "dev", 0)
	port := freePort(t)
	inspector := "http://127.0.0.1:" + strconv.Itoa(port)
	started := time.Now().UTC().Add(-time.Minute)
	var devSlug string
	var targetWS string

	stop := tg.startDev(t, src, "dev", "--path", src, "--name", name, "--debug", "--debug-port", strconv.Itoa(port), "--no-logs", "--ttl", "1h")
	t.Cleanup(func() {
		stop()
		tg.run(t, 3*time.Minute, "dev", "--path", src, "--name", name, "--stop")
	})

	if !s.step("inspector_up", func(t *testing.T) {
		// The first sync builds the environment (a few minutes); the local
		// port answers only once the tunnel reaches the inspector.
		eventually(t, "the local inspector port", 12*time.Minute, 5*time.Second, func() (bool, string) {
			r := get(inspector + "/json/list")
			if r.status != http.StatusOK {
				return false, r.String()
			}
			var targets []map[string]any
			if err := json.Unmarshal(r.body, &targets); err != nil || len(targets) == 0 {
				return false, r.String()
			}
			targetWS = str(targets[0], "webSocketDebuggerUrl")
			return targetWS != "", r.String()
		})
	}) {
		return
	}
	s.check("cdp_evaluate", func(t *testing.T) {
		conn, code, status, err := wsDial(&websocket.Dialer{HandshakeTimeout: 30 * time.Second}, targetWS, nil)
		if err != nil {
			t.Fatalf("dial inspector %s: %d %s: %v", targetWS, code, status, err)
		}
		defer conn.Close()
		for i, c := range []struct{ expr, want string }{
			{"6*7", "42"},
			{"process.env.FAAS_DEVELOPER_ENVIRONMENT", "1"},
		} {
			if got := cdpEvaluate(t, conn, i+1, c.expr); got != c.want {
				t.Fatalf("evaluate %s = %q, want %q", c.expr, got, c.want)
			}
		}
	})
	s.check("discovery_polls", func(t *testing.T) {
		// chrome://inspect polls target discovery about once a second.
		for range 15 {
			if r := get(inspector + "/json/list"); r.status != http.StatusOK {
				t.Fatalf("discovery poll: %s", r.String())
			}
			time.Sleep(time.Second)
		}
	})
	if !s.step("attached_audited", func(t *testing.T) {
		eventually(t, "a dev.debugger.attached audit row", 2*time.Minute, 10*time.Second, func() (bool, string) {
			rows := tg.debuggerAudit(t, "dev.debugger.attached", started)
			for _, r := range rows {
				// The environment's slug is dev-<project>-<hash>, cut to the
				// slug length limit, so match the run id it starts with.
				if slug := str(r, "data.app_slug"); strings.HasPrefix(slug, "dev-") && strings.Contains(slug, tg.runID) &&
					strings.Contains(slug, "devdbg") {
					devSlug = slug
				}
			}
			return devSlug != "", fmt.Sprintf("%d rows", len(rows))
		})
	}) {
		return
	}
	s.check("audit_not_flooded", func(t *testing.T) {
		// One session per gateway process the tunnels land on, not one per
		// tunnel (about 20 tunnels were opened above).
		n := 0
		for _, r := range tg.debuggerAudit(t, "dev.debugger.attached", started) {
			if str(r, "data.app_slug") == devSlug {
				n++
			}
		}
		if n > 4 {
			t.Fatalf("%d dev.debugger.attached rows for one debugging session", n)
		}
	})
	s.check("detached_audited", func(t *testing.T) {
		stop()
		// detached is written once no tunnel has been open for 60 s.
		eventually(t, "a dev.debugger.detached audit row with a tunnel count", 4*time.Minute, 15*time.Second, func() (bool, string) {
			for _, r := range tg.debuggerAudit(t, "dev.debugger.detached", started) {
				if str(r, "data.app_slug") == devSlug && num(r, "data.tunnels") >= 1 {
					return true, compact(r)
				}
			}
			return false, "none yet"
		})
	})
	s.check("refuses_non_developer_app", func(t *testing.T) {
		appSlug, _ := tg.sharedProbe(t)
		key := tg.scopedKey(t, "devdbg", "deploy:write")
		u := "wss" + strings.TrimPrefix(tg.api, "https") + "/v1/apps/" + appSlug + "/debug"
		conn, code, status, err := wsDial(&websocket.Dialer{HandshakeTimeout: 30 * time.Second}, u,
			http.Header{"Authorization": {"Bearer " + key}})
		if conn != nil {
			conn.Close()
			t.Fatalf("the debugger tunnel opened to %s, which is not a developer environment", appSlug)
		}
		if code != http.StatusConflict {
			t.Fatalf("tunnel to a regular app: %d %s (%v), want 409", code, status, err)
		}
	})
}

// debuggerJourney runs debugger commands and keeps every output, so the
// journey can check that none shows a planted secret.
type debuggerJourney struct {
	tg   *target
	slug string
	mu   sync.Mutex
	out  []byte
}

func (j *debuggerJourney) record(b []byte) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.out = append(append(j.out, b...), '\n')
}

func (j *debuggerJourney) planted() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := string(j.out)
	i := strings.Index(s, debuggerPlanted)
	if i < 0 {
		return ""
	}
	return s[max(0, i-80):min(len(s), i+80)]
}

// cli runs a debugger command, skipping when the production debugger is
// not released to the account.
func (j *debuggerJourney) cli(t *testing.T, args ...string) cliResult {
	t.Helper()
	res := j.tg.run(t, 2*time.Minute, args...)
	j.record(res.stdout)
	j.record(res.stderr)
	if res.exit != 0 && strings.Contains(string(res.stderr), "part of the production debugger") {
		t.Skip("the production debugger is not released for this account: " + res.problem())
	}
	if res.exit != 0 {
		t.Fatalf("gregale %s failed (exit %d): %s", strings.Join(args, " "), res.exit, j.tg.redact(res.problem()))
	}
	return res
}

func (j *debuggerJourney) read(t *testing.T, args ...string) map[string]any {
	t.Helper()
	return object(t, j.cli(t, args...).stdout)
}

// debuggerProbe is the probe app behind testdata/probe-debugger's wrapper,
// which exports spans for every traced request and holds GET / for
// delayMs. Each (version, delay) pair is a fixed source digest.
func debuggerProbe(t *testing.T, version string, delayMs int) string {
	t.Helper()
	extra := map[string]string{"debug-delay.txt": strconv.Itoa(delayMs) + "\n"}
	src := filepath.Join("testdata", "probe-debugger")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read debugger probe: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read debugger probe: %v", err)
		}
		extra[e.Name()] = string(b)
	}
	return probeVariant(t, version, extra)
}

// load sends total GETs to url from conc workers and fails on any answer
// other than want.
func load(t *testing.T, url string, total, conc int, want int) {
	t.Helper()
	jobs := make(chan struct{}, total)
	for range total {
		jobs <- struct{}{}
	}
	close(jobs)
	var mu sync.Mutex
	var bad []string
	var wg sync.WaitGroup
	for range conc {
		wg.Go(func() {
			for range jobs {
				if r := get(url); r.status != want {
					mu.Lock()
					bad = append(bad, r.String())
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	if len(bad) > total/20 {
		t.Fatalf("%d of %d requests to %s did not answer %d; first: %s", len(bad), total, url, want, bad[0])
	}
}

// startDev runs a long-lived `gregale dev` in the background and returns
// the function that stops it (safe to call twice).
func (tg *target) startDev(t *testing.T, dir string, args ...string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, tg.cli, append([]string{"--non-interactive"}, args...)...)
	cmd.Env = tg.cliEnv(tg.token)
	cmd.Dir = filepath.Dir(dir)
	logPath := filepath.Join(t.TempDir(), "dev.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("dev log: %v", err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gregale dev: %v", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			_ = cmd.Wait()
			_ = logFile.Close()
			if t.Failed() {
				b, _ := os.ReadFile(logPath)
				t.Logf("gregale dev output:\n%s", tg.redact(tail(string(b), 3000)))
			}
		})
	}
}

// debuggerAudit lists the account's audit rows of kind since a time.
func (tg *target) debuggerAudit(t *testing.T, kind string, since time.Time) []map[string]any {
	t.Helper()
	res := tg.ok(t, time.Minute, "audit", "list", "--kind-prefix", kind, "--since", since.Format(time.RFC3339), "--limit", "100")
	return listOut(t, res.stdout)
}

// cdpEvaluate runs one Runtime.evaluate over a Chrome DevTools Protocol
// connection and returns the value as a string.
func cdpEvaluate(t *testing.T, conn *websocket.Conn, id int, expr string) string {
	t.Helper()
	msg := map[string]any{"id": id, "method": "Runtime.evaluate", "params": map[string]any{"expression": expr, "returnByValue": true}}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("send %s: %v", expr, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		var reply map[string]any
		if err := conn.ReadJSON(&reply); err != nil {
			t.Fatalf("read reply to %s: %v", expr, err)
		}
		if num(reply, "id") != float64(id) {
			continue // an event, not our reply
		}
		v := field(reply, "result.result.value")
		if v == nil {
			t.Fatalf("evaluate %s: %s", expr, compact(reply))
		}
		return fmt.Sprint(v)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
