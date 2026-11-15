package main_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func writeDevFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The watcher must use the upload's selection: a write the upload ignores
// must not start a build, and a file the upload ships must be watched.
func TestDevSourceFingerprintMatchesUploadSelection(t *testing.T) {
	root := t.TempDir()
	writeDevFiles(t, root, map[string]string{
		"Dockerfile":    "FROM scratch\n",
		".dockerignore": "logs/\n",
		"app.js":        "x",
		"dist/app.js":   "built",
		"logs/out.log":  "1",
		".env":          "A=1",
	})
	before, err := main.DevSourceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	writeDevFiles(t, root, map[string]string{"logs/out.log": "12", ".env": "A=22"})
	after, err := main.DevSourceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("a .dockerignore'd log write or a local .env edit changed the watched fingerprint")
	}
	writeDevFiles(t, root, map[string]string{"dist/app.js": "rebuilt!"})
	changed, err := main.DevSourceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed == after {
		t.Fatal("dist/ ships with a Dockerfile build but its edit was not seen")
	}
}

func TestDevLocalEnvExcludes(t *testing.T) {
	root := t.TempDir()
	writeDevFiles(t, root, map[string]string{
		"app.js": "x", ".env": "A=1", ".env.local": "B=2", ".env.example": "A=", "api/.env.production": "C=3",
	})
	got := main.DevLocalEnvExcludes(root, false)
	var rel []string
	resolved, _ := filepath.EvalSymlinks(root)
	for _, path := range got {
		r, _ := filepath.Rel(resolved, path)
		rel = append(rel, filepath.ToSlash(r))
	}
	if want := []string{".env", ".env.local", "api/.env.production"}; !slices.Equal(rel, want) {
		t.Fatalf("excluded = %v, want %v", rel, want)
	}

	writeDevFiles(t, root, map[string]string{".gregaleignore": "!.env\n"})
	got = main.DevLocalEnvExcludes(root, false)
	for _, path := range got {
		if filepath.Base(path) == ".env" && filepath.Dir(path) == resolved {
			t.Fatalf("a .env re-included by !.env was still excluded: %v", got)
		}
	}
}

func TestPackExcludesPythonVirtualenvByMarker(t *testing.T) {
	root := t.TempDir()
	writeDevFiles(t, root, map[string]string{
		"venv/pyvenv.cfg": "home = /usr/bin", "venv/lib/x.py": "x",
		"venv2/notes.py": "keep", // no marker: source
		".nuxt/a.js":     "gen",
	})
	if !main.ShouldExcludeWithRules(root, "venv", true, false) {
		t.Fatal("a directory with pyvenv.cfg must be excluded")
	}
	if main.ShouldExcludeWithRules(root, "venv2", true, false) {
		t.Fatal("a directory without pyvenv.cfg is source")
	}
	if !main.ShouldExcludeWithRules(root, ".nuxt", true, false) {
		t.Fatal(".nuxt is regenerated build output")
	}
	if main.ShouldExcludeWithRules(root, ".nuxt", true, true) {
		t.Fatal("Dockerfile builds ship build output directories")
	}
}

func TestDevAuthProxyAddsCredentialAndGuardsBrowserOrigins(t *testing.T) {
	var gotAuth, gotHost, gotOrigin string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotHost, gotOrigin = r.Header.Get("Authorization"), r.Host, r.Header.Get("Origin")
		if r.URL.Path == "/redirect" {
			http.SetCookie(w, &http.Cookie{Name: "s", Value: "1", Domain: "dev-x.example.test", Secure: true})
			http.Redirect(w, r, "https://dev-x.example.test/next?a=1", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	handler := main.DevAuthProxyHandler(target, "fp_live_token", 8788)

	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	call := func(method, path, host, origin string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(method, proxy.URL+path, nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}

	if resp := call(http.MethodGet, "/", "localhost:8788", "http://localhost:8788"); resp.StatusCode != http.StatusOK {
		t.Fatalf("local GET status = %d", resp.StatusCode)
	}
	if gotAuth != "Bearer fp_live_token" || gotHost != target.Host || gotOrigin != "http://"+target.Host {
		t.Fatalf("upstream saw auth=%q host=%q origin=%q", gotAuth, gotHost, gotOrigin)
	}
	if resp := call(http.MethodGet, "/", "evil.example:8788", ""); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("DNS-rebinding host status = %d, want 421", resp.StatusCode)
	}
	if resp := call(http.MethodPost, "/", "localhost:8788", "https://evil.example"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST status = %d, want 403", resp.StatusCode)
	}

	resp := call(http.MethodGet, "/redirect", "localhost:8788", "")
	if loc := resp.Header.Get("Location"); loc != "https://dev-x.example.test/next?a=1" {
		// The redirect names a different host than the upstream test
		// server, so it must be left alone.
		t.Fatalf("foreign redirect rewritten to %q", loc)
	}
	for _, cookie := range resp.Header.Values("Set-Cookie") {
		lower := strings.ToLower(cookie)
		if strings.Contains(lower, "domain=") || strings.Contains(lower, "secure") {
			t.Fatalf("cookie keeps attributes that block loopback: %q", cookie)
		}
	}
}

func TestDevNeedsAuthProxyOnlyForGregaleKeys(t *testing.T) {
	if !main.DevNeedsAuthProxy(api.AppResponse{RequireAuthn: true}) {
		t.Fatal("require_authn environments need the proxy")
	}
	if main.DevNeedsAuthProxy(api.AppResponse{PublicAuth: api.PublicAuthStatus{Mode: "basic"}}) {
		t.Fatal("basic auth is the browser's own prompt")
	}
}

func TestIsDevSourceRouteMissing(t *testing.T) {
	bare := &api.APIError{Problem: api.Problem{Status: 404}}
	if !main.IsDevSourceRouteMissing(bare) {
		t.Fatal("a bare 404 is an old apid without the route")
	}
	coded := &api.APIError{Problem: api.Problem{Status: 404, Code: "not_found"}}
	if main.IsDevSourceRouteMissing(coded) {
		t.Fatal("a coded 404 (no such developer environment) must not turn deltas off")
	}
}

type fakeDevSecrets struct {
	keys   map[string]bool
	writes []string
}

func (f *fakeDevSecrets) ListSecretsWithScope(context.Context, string, string) (api.AppSecretListResponse, error) {
	var out api.AppSecretListResponse
	for key := range f.keys {
		out.Secrets = append(out.Secrets, api.AppSecretResponse{Key: key})
	}
	return out, nil
}

func (f *fakeDevSecrets) SetSecretWithScope(_ context.Context, _, key, _, _ string) error {
	f.keys[key] = true
	f.writes = append(f.writes, key)
	return nil
}

func TestDevEnvSyncWritesOnlyChangedValues(t *testing.T) {
	client := &fakeDevSecrets{keys: map[string]bool{}}
	state := &main.DevEnvSyncState{}
	ctx := context.Background()
	if n, err := main.SyncDevEnvPairs(ctx, state, client, "dev-x", "/p/.env.dev", map[string]string{"A": "1", "B": "2"}, 1); err != nil || n != 2 {
		t.Fatalf("first sync wrote %d (%v), want 2", n, err)
	}
	if n, err := main.SyncDevEnvPairs(ctx, state, client, "dev-x", "/p/.env.dev", map[string]string{"A": "1", "B": "3"}, 2); err != nil || n != 1 {
		t.Fatalf("second sync wrote %d (%v), want only the changed key", n, err)
	}
	if got := client.writes[len(client.writes)-1]; got != "B" {
		t.Fatalf("rewrote %q, want B", got)
	}
}

func TestDevLogFollowerDedupesReplayAndRecognisesParked(t *testing.T) {
	line := func(seq int) api.Event {
		return api.Event{Event: "log", Data: `{"seq":` + strconv.Itoa(seq) + `,"instance":"i1","stream":"stdout","line":"hello"}`}
	}
	parked := main.DevLogFollowerRender([]api.Event{
		line(1), line(2), line(1), line(2), line(3),
		{Event: "end", Data: `{"reason":"not_found"}`},
	})
	if !parked[len(parked)-1] {
		t.Fatal("end not_found must be read as parked")
	}
	if slices.Contains(parked[:len(parked)-1], true) {
		t.Fatal("log lines must not end the stream")
	}
}

func TestDevSeedRunnerSingleFlight(t *testing.T) {
	r := main.NewDevSeedRunner(true, true)
	force, ok := r.Start()
	if !ok || !force {
		t.Fatalf("first start = (%v, %v)", force, ok)
	}
	if _, again := r.Start(); again {
		t.Fatal("a second seed started while one runs")
	}
	r.Finish(main.DevSeedOutcomeFailed)
	if _, ok := r.Start(); !ok {
		t.Fatal("a failed seed must be retried")
	}
	r.Finish(main.DevSeedOutcomeDone)
	if _, ok := r.Start(); ok {
		t.Fatal("a completed seed must not run again")
	}
}

func TestDevSetupSaveKeepsIndentAndSkipsEmptyKeys(t *testing.T) {
	before := []byte("dev:\n    ttl: 24h\n    watch:\n        command: npm run dev\n")
	out, err := main.MergeDevSetupYAML(before, map[string]any{"ttl": "72h", "env_file": "", "postgres": false, "buckets": []api.DevBucketRequest(nil)})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "        command: npm run dev") || !strings.Contains(text, "    ttl: 72h") {
		t.Fatalf("indentation not kept:\n%s", text)
	}
	for _, unwanted := range []string{"env_file", "postgres", "buckets"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("empty %s written:\n%s", unwanted, text)
		}
	}
}

func TestOnDiskPathCase(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("case-insensitive file systems only")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "MixedCase"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(root)
	got := main.OnDiskPathCase(filepath.Join(resolved, "mixedcase"))
	if got != filepath.Join(resolved, "MixedCase") {
		t.Fatalf("got %q", got)
	}
}

func TestRenewDevSessionWhileWatching(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		main.RenewDevSessionWhileWatching(ctx, 5*time.Millisecond, func() error {
			if calls.Add(1) == 1 {
				return errors.New("transient")
			}
			return nil
		})
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if calls.Load() < 3 {
		t.Fatalf("renewed %d times; a failure must not stop renewals", calls.Load())
	}
}
