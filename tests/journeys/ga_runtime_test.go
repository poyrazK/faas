package journeys

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// GA inventory section C (runtime and compute) and D (networking and edge)
// journeys beyond the core lifecycle: the browser wake page, resource
// sizing and autoscaling settings, the env lifecycle, WebSockets and IP
// allowlists.

// browserNav are the headers a browser sends for a top-level navigation.
var browserNav = map[string]string{
	"Accept":         "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	"Sec-Fetch-Mode": "navigate",
	"Sec-Fetch-Dest": "document",
	"User-Agent":     "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
}

// TestBrowserWakePage parks an app and visits it like a browser: when the
// wake takes longer than the grace period (api.WakePageAfterMs, 1.5 s) the
// gateway answers with a self-refreshing page that carries no app data,
// and the next visit reaches the app. A fetch() from a page must never get
// that HTML (row 39).
func TestBrowserWakePage(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "wakepage")
	s := newStory(t)

	s.check("navigation", func(t *testing.T) {
		shown := 0
		for attempt := range 3 {
			tg.ok(t, 2*time.Minute, "park", slug)
			waitParked(t, tg, slug)
			r := fetch(http.MethodGet, base+"/", browserNav)
			if r.status != http.StatusOK {
				t.Fatalf("browser visit %d to a parked app: %s", attempt+1, r)
			}
			if r.header.Get("x-faas-wake-page") != "1" {
				t.Logf("visit %d: the wake finished within the grace period in %s; no page needed", attempt+1, r.elapsed.Round(time.Millisecond))
				continue
			}
			shown++
			body := string(r.body)
			if !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") || !strings.Contains(body, `http-equiv="refresh"`) {
				t.Errorf("wake page is not a self-refreshing HTML page: %s", r)
			}
			if strings.Contains(body, "gregale-e2e-probe") {
				t.Errorf("the wake page leaked app content")
			}
			eventually(t, "the refreshed visit reaches the app", time.Minute, time.Second, func() (bool, string) {
				again := fetch(http.MethodGet, base+"/", browserNav)
				return again.status == http.StatusOK && again.header.Get("x-faas-wake-page") == "" &&
					strings.Contains(string(again.body), "gregale-e2e-probe"), again.String()
			})
		}
		t.Logf("metric wake_page_shown=%d", shown)
	})
	s.check("fetch_never_gets_html", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "park", slug)
		waitParked(t, tg, slug)
		r := fetch(http.MethodGet, base+"/", map[string]string{
			"Accept": "text/html,*/*", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty",
		})
		if r.header.Get("x-faas-wake-page") == "1" {
			t.Fatalf("a fetch() request got the browser wake page")
		}
		if r.status != http.StatusOK || !strings.Contains(string(r.body), "gregale-e2e-probe") {
			t.Fatalf("fetch to a parked app: %s", r)
		}
	})
}

// TestResourcesAndAutoscaling changes RAM, CPU, maximum instances and the
// autoscaling targets, reads each back, checks the app still serves, and
// restores the defaults (rows 43, 49).
func TestResourcesAndAutoscaling(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "sizing")
	t.Cleanup(func() {
		tg.run(t, time.Minute, "app", slug, "--autoscale-target-rps", "0", "--autoscale-target-cpu-pct", "0", "--max-concurrency", "1")
	})
	tests := []struct {
		name  string
		args  []string
		field string
		want  float64
	}{
		{"ram", []string{"--ram", "512"}, "ram_mb", 512},
		{"effective_memory_limit", nil, "effective_limits.memory_limit_mb", 512},
		{"cpu_millicores", []string{"--cpu-millicores", "500"}, "cpu_millicores", 500},
		{"max_concurrency", []string{"--max-concurrency", "2"}, "max_concurrency", 2},
		{"autoscale_target_rps", []string{"--autoscale-target-rps", "50"}, "autoscale_target_rps", 50},
		{"autoscale_target_cpu_pct", []string{"--autoscale-target-cpu-pct", "70"}, "autoscale_target_cpu_pct", 70},
	}
	s := newStory(t)
	for _, tc := range tests {
		s.check(tc.name, func(t *testing.T) {
			if tc.args != nil {
				tg.ok(t, 2*time.Minute, append([]string{"app", slug}, tc.args...)...)
			}
			eventually(t, tc.field+" reads back", time.Minute, 3*time.Second, func() (bool, string) {
				shown := object(t, tg.ok(t, time.Minute, "app", slug).stdout)
				return num(shown, tc.field) == tc.want, fmt.Sprintf("%s = %s", tc.field, compact(field(shown, tc.field)))
			})
		})
	}
	s.check("serves_after_resize", func(t *testing.T) {
		expectVersion(t, base, "v1", 3*time.Minute)
	})
	s.check("defaults_restored", func(t *testing.T) {
		tg.ok(t, time.Minute, "app", slug, "--autoscale-target-rps", "0", "--autoscale-target-cpu-pct", "0")
		shown := object(t, tg.ok(t, time.Minute, "app", slug).stdout)
		if num(shown, "autoscale_target_rps") != 0 || num(shown, "autoscale_target_cpu_pct") != 0 {
			t.Fatalf("autoscaling targets did not reset: %s", compact(map[string]any{
				"rps": field(shown, "autoscale_target_rps"), "cpu": field(shown, "autoscale_target_cpu_pct")}))
		}
	})
}

// TestEnvLifecycle pushes variables from a .env file, delivers them with a
// restart, pulls a value-free skeleton, diffs scopes, lists the config
// history and rolls back to an earlier revision (row 60).
func TestEnvLifecycle(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "env")
	one, two := randomHex(t, 16), randomHex(t, 16)
	dir := t.TempDir()
	envFile := filepath.Join(dir, "first.env")
	if err := os.WriteFile(envFile, []byte("E2E_ALPHA="+one+"\nE2E_BETA="+two+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hashOf := func(v string) string {
		sum := sha256.Sum256([]byte(v))
		return hex.EncodeToString(sum[:])
	}
	s := newStory(t)

	s.step("push_and_deliver", func(t *testing.T) {
		tg.ok(t, 5*time.Minute, "env", "push", "--app", slug, "-f", envFile, "--restart")
		eventually(t, "pushed values reach the app", 3*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/env?key=E2E_ALPHA")
			if r.status != http.StatusOK {
				return false, r.String()
			}
			doc := jsonBody(t, r)
			return str(doc, "sha256") == hashOf(one), "app sees " + compact(doc)
		})
	})
	s.check("pull_skeleton_has_no_values", func(t *testing.T) {
		out := filepath.Join(dir, "pulled.env")
		tg.ok(t, time.Minute, "env", "pull", "--app", slug, "-o", out)
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read pulled file: %v", err)
		}
		if !strings.Contains(string(b), "E2E_ALPHA") || !strings.Contains(string(b), "E2E_BETA") {
			t.Errorf("pulled skeleton lacks the pushed keys: %s", tail(string(b), 300))
		}
		if strings.Contains(string(b), one) || strings.Contains(string(b), two) {
			t.Errorf("env pull wrote secret values to disk")
		}
	})
	s.check("diff", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "env", "diff", "--app", slug)
		if !strings.Contains(string(res.stdout), "E2E_ALPHA") {
			t.Errorf("env diff does not list E2E_ALPHA: %s", tail(string(res.stdout), 300))
		}
	})
	var firstRevision string
	s.step("history", func(t *testing.T) {
		docs := objects(t, tg.ok(t, time.Minute, "env", "history", "--app", slug).stdout)
		firstRevision = latestRevision(docs)
		if firstRevision == "" {
			t.Fatalf("env history lists no revision: %s", compact(docs))
		}
	})
	s.check("rollback", func(t *testing.T) {
		changed := filepath.Join(dir, "second.env")
		if err := os.WriteFile(changed, []byte("E2E_ALPHA=changed-"+one+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		tg.ok(t, 5*time.Minute, "env", "push", "--app", slug, "-f", changed)
		res := tg.ok(t, 5*time.Minute, "env", "rollback", firstRevision, "--app", slug, "--yes")
		t.Logf("rollback to revision %s: %s", firstRevision, tail(string(res.stdout), 500))
		docs := objects(t, tg.ok(t, time.Minute, "env", "history", "--app", slug).stdout)
		if latest := latestRevision(docs); latest == firstRevision {
			t.Errorf("env history did not record the rollback as a new revision")
		}
	})
}

// latestRevision returns the newest revision id from `env history` output,
// whether it prints one document with items or one document per revision.
func latestRevision(docs []map[string]any) string {
	for _, doc := range docs {
		for _, key := range []string{"items", "revisions"} {
			if items, ok := field(doc, key).([]any); ok && len(items) > 0 {
				for _, k := range []string{"revision", "id", "number"} {
					if v := str(items[0], k); v != "" {
						return v
					}
				}
			}
		}
		for _, k := range []string{"revision", "id", "number"} {
			if v := str(doc, k); v != "" {
				return v
			}
		}
	}
	return ""
}

// TestWebSockets opens a WebSocket to the shared probe through the public
// edge and reads its greeting frame (row 64).
func TestWebSockets(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	_, base := tg.sharedProbe(t)
	msg, err := websocketGreeting(base + "/ws")
	if err != nil {
		t.Fatalf("WebSocket to %s/ws: %v", base, err)
	}
	if msg != "hello v1" {
		t.Fatalf("WebSocket greeting = %q, want %q", msg, "hello v1")
	}
}

// websocketGreeting performs an RFC 6455 handshake over TLS and returns the
// first text frame the server sends.
func websocketGreeting(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", host+":443", &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}})
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	key := base64.StdEncoding.EncodeToString([]byte("gregale-journeys"))
	req := "GET " + u.RequestURI() + " HTTP/1.1\r\nHost: " + host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\nUser-Agent: gregale-e2e-journeys\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		return "", err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("handshake answered HTTP %d: %s", resp.StatusCode, body)
	}
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		return "", fmt.Errorf("read frame header: %w", err)
	}
	if head[0]&0x0f != 0x1 {
		return "", fmt.Errorf("first frame has opcode %d, want text", head[0]&0x0f)
	}
	size := int(head[1] & 0x7f)
	if size > 125 {
		return "", fmt.Errorf("greeting frame is %d bytes, want a short frame", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(br, payload); err != nil {
		return "", fmt.Errorf("read frame payload: %w", err)
	}
	return string(payload), nil
}
