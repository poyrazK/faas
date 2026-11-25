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
// linked checkout, as did `api consumer list|create` (r51). A command whose
// first positional is the app takes the linked app when that is the only
// positional missing and the others given are ids.
func TestLinkedAppAsArgument(t *testing.T) {
	dir := t.TempDir()
	linkCheckoutTo(t, dir, "web")
	t.Chdir(dir)
	const id = "a27b45f5-2a74-7583-e32a-eabe239d529b"
	for _, tc := range []struct{ in, want []string }{
		{[]string{"realtime", "get", id}, []string{"realtime", "get", "web", id}},
		{[]string{"realtime", "delete", id, "--yes"}, []string{"realtime", "delete", "web", id, "--yes"}},
		{[]string{"consumers", "list"}, []string{"consumers", "list", "web"}},
		{[]string{"consumers", "create", "--name", "Cust One"}, []string{"consumers", "create", "web", "--name", "Cust One"}},
		{[]string{"consumers", "info", id}, []string{"consumers", "info", "web", id}},
		// Already complete, or a word that may be the app: unchanged.
		{[]string{"realtime", "get", "other", id}, []string{"realtime", "get", "other", id}},
		{[]string{"consumers", "list", "other"}, []string{"consumers", "list", "other"}},
		{[]string{"consumers", "info", "notanid"}, []string{"consumers", "info", "notanid"}},
		// An undeclared flag or help: positionals are not known, unchanged.
		{[]string{"consumers", "list", "--bogus", "x"}, []string{"consumers", "list", "--bogus", "x"}},
		{[]string{"consumers", "list", "--help"}, []string{"consumers", "list", "--help"}},
		// An optional app (realtime list) is left to the command.
		{[]string{"realtime", "list"}, []string{"realtime", "list"}},
		{[]string{"realtime", "usage"}, []string{"realtime", "usage"}},
	} {
		if got := main.LinkedAppAsArgument(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("LinkedAppAsArgument(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Prod hunt r51: `gregale api consumer list` in a linked checkout printed
// usage; it now lists the linked app's consumers.
func TestAPIConsumerListUsesTheLinkedApp(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"consumers":[]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	dir := t.TempDir()
	linkCheckoutTo(t, dir, "web")
	t.Chdir(dir)
	if code, _, stderr := runCLI(t, "api", "consumer", "list"); code != 0 {
		t.Fatalf("api consumer list = %d %q, want the linked app used", code, stderr)
	}
	if !slices.ContainsFunc(paths, func(p string) bool { return strings.Contains(p, "/apps/web/") }) {
		t.Fatalf("requests = %q, want one for app web", paths)
	}
}

// Outside a linked checkout nothing is filled.
func TestLinkedAppAsArgumentWithoutLink(t *testing.T) {
	t.Chdir(t.TempDir())
	in := []string{"consumers", "list"}
	if got := main.LinkedAppAsArgument(in); !slices.Equal(got, in) {
		t.Fatalf("LinkedAppAsArgument(%q) = %q, want unchanged", in, got)
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
