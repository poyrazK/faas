package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// adr: 20261107210000
func TestMatchFunctionRoute(t *testing.T) {
	cases := []struct {
		pattern, path string
		ok            bool
	}{
		{"/users", "/users", true},
		{"/users", "/users/1", false},
		{"/users/*", "/users/1", true},
		{"/users/*", "/users/1/2", true},
		{"/users/*", "/users", false},
		{"/users/*", "/users/", false},
		{"/users/**", "/users", true},
		{"/users/**", "/users/1", true},
		{"/users/**", "/usersx", false},
	}
	for _, tc := range cases {
		if _, ok := main.MatchFunctionRoute(tc.pattern, tc.path); ok != tc.ok {
			t.Errorf("match(%q, %q) = %v, want %v", tc.pattern, tc.path, ok, tc.ok)
		}
	}
	short, _ := main.MatchFunctionRoute("/users/*", "/users/admin/x")
	long, _ := main.MatchFunctionRoute("/users/admin/*", "/users/admin/x")
	if long <= short {
		t.Errorf("specificity %d <= %d: the more specific route must win", long, short)
	}
}

func writeDevLocalProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func devLocalGet(t *testing.T, h http.Handler, host, path string, header http.Header) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// adr: 20261107210000
// Requests run through the production Python adapter and runner core, are
// routed like the platform's hosts, and carry only trusted platform headers.
func TestDevLocalServesThroughRunnerCore(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	handler := `import os

def handler(event, ctx):
    h = event["headers"]
    return {"status": 200, "body": {"fn": os.environ["FN"], "path": event["path"],
            "deadline": h.get("x-faas-deadline-ms"), "id": h.get("x-faas-invocation-id"),
            "source": h.get("x-faas-invocation-source"), "attempt": h.get("x-faas-invocation-attempt")}}
`
	dir := writeDevLocalProject(t, map[string]string{
		"gregale.yaml": `functions:
  - name: web
    root: functions/web
    entry: true
    env: {FN: web}
  - name: users
    root: functions/users
    routes: ["/users/**"]
    request_timeout_s: 9
    env: {FN: users}
`,
		"functions/web/handler.py":   handler,
		"functions/users/handler.py": handler,
	})
	srv, token, err := main.DevLocalServerForTest(t, dir)
	if err != nil {
		t.Fatal(err)
	}

	routes := []struct{ host, path, fn string }{
		{"web.localhost:8787", "/users/1", "web"},
		{"users.localhost:8787", "/", "users"},
		{"localhost:8787", "/users", "users"},
		{"localhost:8787", "/users/7", "users"},
		{"localhost:8787", "/about", "web"},
	}
	for _, r := range routes {
		code, body := devLocalGet(t, srv, r.host, r.path, nil)
		if code != 200 || body["fn"] != r.fn || body["path"] != r.path {
			t.Errorf("%s%s = %d %v, want %s", r.host, r.path, code, body, r.fn)
		}
	}
	if code, _ := devLocalGet(t, srv, "nope.localhost:8787", "/", nil); code != http.StatusNotFound {
		t.Errorf("unknown function = %d, want 404", code)
	}

	spoof := http.Header{"X-Faas-Invocation-Source": {"cron"}, "X-Faas-Deadline-Ms": {"1"}}
	_, body := devLocalGet(t, srv, "users.localhost:8787", "/", spoof)
	if body["source"] != nil || body["deadline"] != "9000" || body["id"] == nil || body["id"] == "" {
		t.Errorf("headers = %v, want client values dropped, deadline 9000 and an invocation id", body)
	}

	trigger := func(tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, main.DevLocalTriggerPath, strings.NewReader(`{"function":"users","source":"cron","path":"/"}`))
		req.Host = "localhost:8787"
		if tok != "" {
			req.Header.Set(main.DevLocalTokenHeader, tok)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	if rec := trigger("wrong"); rec.Code != http.StatusForbidden {
		t.Errorf("trigger with a wrong token = %d, want 403", rec.Code)
	}
	rec := trigger(token)
	var result struct {
		Status int
		Body   string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Status != 200 {
		t.Fatalf("trigger = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(result.Body, `"source": "cron"`) || !strings.Contains(result.Body, `"attempt": "1"`) {
		t.Errorf("triggered call body = %s, want source cron and attempt 1", result.Body)
	}
}
