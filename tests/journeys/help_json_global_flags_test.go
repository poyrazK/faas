package journeys

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: an agent discovers global options offline, then uses that
// metadata to construct a scoped, read-only request from another directory.
func TestJSONHelpGlobalFlagsIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	var requests, lists atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("discovery journey attempted a write: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var response any
		switch r.URL.Path {
		case "/v1/capabilities":
			response = map[string]any{}
		case "/v1/invocations":
			lists.Add(1)
			if r.URL.Query().Get("limit") != "1" {
				t.Errorf("constructed command lost its limit: %s", r.URL.RawQuery)
			}
			response = map[string]any{"invocations": []any{}}
		default:
			t.Errorf("unexpected read: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
	const token = "fp_test_global-help-journey"
	root := tg.exec(t, token, tg.home, time.Minute, "help")
	var document struct {
		Flags       []any
		GlobalFlags []struct {
			Name, Placement string
			Spellings       []string
			TakesValue      bool `json:"takes_value"`
		} `json:"global_flags"`
	}
	if root.exit != 0 || len(root.stderr) != 0 || json.Unmarshal(root.stdout, &document) != nil || len(document.Flags) != 0 || len(document.GlobalFlags) != 9 || strings.Contains(string(root.stdout), token) {
		t.Fatalf("global discovery: exit=%d stdout=%s stderr=%s", root.exit, root.stdout, root.stderr)
	}
	if requests.Load() != 0 {
		t.Fatal("global-option discovery made an API request")
	}
	profile := tg.exec(t, token, tg.home, time.Minute, "config", "profile", "add", "staging", server.URL)
	if profile.exit != 0 {
		t.Fatalf("local profile setup: %s", profile.stderr)
	}
	var args []string
	for _, option := range []struct{ name, value string }{
		{"profile", "staging"}, {"http-timeout", "2s"}, {"http-retries", "1"}, {"non-interactive", ""}, {"json", ""},
	} {
		found := false
		for _, flag := range document.GlobalFlags {
			if flag.Name != option.name {
				continue
			}
			found = true
			placement := "before_command"
			if option.name == "json" {
				placement = "before_separator"
			}
			if flag.Placement != placement || flag.TakesValue != (option.value != "") || len(flag.Spellings) == 0 {
				t.Fatalf("invalid option contract for %s", option.name)
			}
			args = append(args, flag.Spellings[0])
			if flag.TakesValue {
				args = append(args, option.value)
			}
		}
		if !found {
			t.Fatalf("cannot discover %s", option.name)
		}
	}
	args = append(args, "invocation", "list", "--limit", "1")
	result := tg.exec(t, token, t.TempDir(), time.Minute, args...)
	var response struct{ Invocations []any }
	if result.exit != 0 || len(result.stderr) != 0 || json.Unmarshal(result.stdout, &response) != nil || response.Invocations == nil || len(response.Invocations) != 0 || lists.Load() != 1 {
		t.Fatalf("discovered command failed: exit=%d stdout=%s stderr=%s reads=%d", result.exit, result.stdout, result.stderr, lists.Load())
	}
}
