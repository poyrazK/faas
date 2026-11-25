package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r50: realtime verbs take APP_SLUG first and printed usage in a
// linked checkout; a UUID right after the verb means the app was left out.
func TestRealtimeArgsWithLinkedApp(t *testing.T) {
	dir := t.TempDir()
	linkCheckoutTo(t, dir, "web")
	t.Chdir(dir)
	const id = "a27b45f5-2a74-7583-e32a-eabe239d529b"
	for _, tc := range []struct{ in, want []string }{
		{[]string{"get", id}, []string{"get", "web", id}},
		{[]string{"delete", id, "--yes"}, []string{"delete", "web", id, "--yes"}},
		{[]string{"auth", "status", id}, []string{"auth", "status", "web", id}},
		{[]string{"get", "other", id}, []string{"get", "other", id}},
		{[]string{"usage"}, []string{"usage"}},
		{[]string{"list"}, []string{"list"}},
	} {
		if got := main.RealtimeArgsWithLinkedApp(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("RealtimeArgsWithLinkedApp(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// linkCheckoutTo saves dir's standalone link to app, the file deploy writes.
func linkCheckoutTo(t *testing.T, dir, app string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".gregale"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"version":2,"app":"` + app + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".gregale", "project.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Prod hunt r50: `redis bind <cache>` in a linked checkout printed usage;
// --app now defaults to the linked app, for unbind too.
func TestRedisBindUsesTheLinkedApp(t *testing.T) {
	var apps []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1/redis/sessions/bindings") {
			body, _ := io.ReadAll(r.Body)
			var req struct {
				App string `json:"app"`
			}
			_ = json.Unmarshal(body, &req)
			app := req.App
			if app == "" {
				app = r.URL.Query().Get("app")
			}
			if app == "" {
				app = strings.TrimPrefix(r.URL.Path, "/v1/redis/sessions/bindings/")
			}
			apps = append(apps, r.Method+" "+app)
			_, _ = w.Write([]byte(`{"cache":"sessions","app":"web","env_key":"REDIS_URL"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	dir := t.TempDir()
	linkCheckoutTo(t, dir, "web")
	t.Chdir(dir)
	for _, verb := range []string{"bind", "unbind"} {
		if code, _, stderr := runCLI(t, "redis", verb, "sessions"); code != 0 {
			t.Fatalf("redis %s = %d %q, want the linked app used", verb, code, stderr)
		}
	}
	if len(apps) != 2 || !strings.HasSuffix(apps[0], " web") || !strings.HasSuffix(apps[1], " web") {
		t.Fatalf("binding requests = %q, want both for web", apps)
	}
}
