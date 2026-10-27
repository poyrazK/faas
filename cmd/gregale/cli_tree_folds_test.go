package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// foldAPI answers the SLO, SLO panel, edge suggestion and throttle routes
// and records the paths it served.
type foldAPI struct {
	mu    sync.Mutex
	paths []string
}

func (a *foldAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.paths = append(a.paths, r.URL.Path+"?"+r.URL.RawQuery)
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/apps/api/slos":
		_, _ = w.Write([]byte(`[{"id":"s1","name":"availability","sli":"availability","objective_pct":99.9,"window_days":30}]`))
	case "/v1/apps/api/slo":
		_, _ = w.Write([]byte(`{"app_id":"app-1","window":"` + r.URL.Query().Get("window") + `"}`))
	case "/v1/apps/api/edge-rules/suggestions":
		_, _ = w.Write([]byte(`{"window":"24h","suggestions":[]}`))
	case "/v1/apps/api/throttle-suggestions":
		_, _ = w.Write([]byte(`{"app_id":"app-1","source":"prometheus","suggestions":[]}`))
	default:
		_, _ = w.Write([]byte(`[]`))
	}
}

func (a *foldAPI) served(prefix string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, path := range a.paths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// ADR-20261026145832 placements: the SLO panel is `alert slo list --window`
// and per-route rate limits are `edge rule suggest --rate-limits`.
func TestCliTreeFoldsRunTheFoldedCommands(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Chdir(t.TempDir())
	api := &foldAPI{}
	server := httptest.NewServer(api)
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")

	code, stdout, stderr := runCLI(t, "--json", "alert", "slo", "list", "--app", "api", "--window", "24h")
	var listed struct {
		SLOs  []json.RawMessage `json:"slos"`
		Panel struct {
			Window string `json:"window"`
		} `json:"panel"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &listed) != nil || len(listed.SLOs) != 1 || listed.Panel.Window != "24h" {
		t.Fatalf("alert slo list --window = %d %q %q, want the SLOs and the 24h panel", code, stdout, stderr)
	}
	if code, stdout, _ := runCLI(t, "--json", "alert", "slo", "list", "--app", "api"); code != 0 || !strings.HasPrefix(strings.TrimSpace(stdout), "[") {
		t.Errorf("alert slo list without --window = %d %q, want today's JSON list", code, stdout)
	}
	if code, _, _ := runCLI(t, "alert", "slo", "list", "--app", "api", "--window", "30d"); code == 0 {
		t.Error("alert slo list --window 30d must be refused; the panel serves 1h, 24h and 7d")
	}

	if code, _, stderr := runCLI(t, "--json", "edge", "rule", "suggest", "--app", "api", "--rate-limits", "--range", "24h"); code != 0 || !api.served("/v1/apps/api/throttle-suggestions?") {
		t.Fatalf("edge rule suggest --rate-limits = %d %q, want the throttle suggestions", code, stderr)
	}
	if code, stdout, _ := runCLI(t, "edge", "rule", "suggest", "--app", "api"); code != 0 || !strings.Contains(stdout, "--rate-limits") {
		t.Errorf("edge rule suggest = %d %q, want protections and a pointer to --rate-limits", code, stdout)
	}
}

// The operation noun runs customer-operations, and its delivery verbs have
// tree names.
func TestCliTreeOperationNoun(t *testing.T) {
	enableEveryCLIFeature(t)
	for _, tc := range []struct{ in, want string }{
		{"operation list --self", "customer-operations list --self"},
		{"operation delivery get op1 --self", "customer-operations delivery op1 --self"},
		{"operation delivery attempts op1 --self", "customer-operations delivery-attempts op1 --self"},
		{"operation delivery retry op1 --self", "customer-operations retry-delivery op1 --self"},
	} {
		got, err := main.RewriteCliTree(strings.Fields(tc.in))
		if err != nil || strings.Join(got, " ") != tc.want {
			t.Errorf("%q rewrote to %q (%v), want %q", tc.in, strings.Join(got, " "), err, tc.want)
		}
	}
}

// A folded earlier name names its tree path in a terminal.
func TestCliTreeHintsFoldedNames(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	prev := *main.CliTreeHintTerminal
	*main.CliTreeHintTerminal = func() bool { return true }
	t.Cleanup(func() { *main.CliTreeHintTerminal = prev })
	for _, tc := range []struct {
		args []string
		hint string
	}{
		{[]string{"slo", "api"}, "'gregale slo' is now 'gregale alert slo list --window'"},
		{[]string{"throttle-suggestions", "api"}, "'gregale throttle-suggestions' is now 'gregale edge rule suggest --rate-limits'"},
		{[]string{"delayed-task", "list", "--app", "api"}, "'gregale delayed-task list' is now 'gregale cron list --one-off'"},
	} {
		if _, _, stderr := runCLI(t, tc.args...); !strings.Contains(stderr, tc.hint) {
			t.Errorf("gregale %s in a terminal = %q, want %q", strings.Join(tc.args, " "), stderr, tc.hint)
		}
	}
}
