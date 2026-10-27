// adr: 20261023115741 — the CLI hides the commands of a capability whose
// platform flag is off for the account.
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/productcap"
)

// ungatedCommands are customer commands no capability owns, with the reason
// they stay visible for every account.
var ungatedCommands = map[string]string{
	"add":          "provisions resources; each verb is owned by its resource's capability",
	"menu":         "guided entry point",
	"start":        "first-deploy guide",
	"capabilities": "reports what is released",
	"dashboard":    "opens the dashboard",
	"doctor":       "local source checks",
	"deploy":       "core deploy",
	"deploys":      "core deploy",
	"deployment":   "core deploy",
	"deployments":  "core deploy",
	"invoke":       "calls an app",
	"login":        "sign-in",
	"logout":       "sign-in",
	"signup":       "sign-in",
	"whoami":       "sign-in",
	"link":         "local checkout state",
	"unlink":       "local checkout state",
	"profile":      "local connection profiles",
	"context":      "local configuration",
	"config":       "local configuration",
	"open":         "opens an app",
	"status":       "platform status",
	"version":      "local",
	"upload-cache": "local resumable uploads",
	"completion":   "local",
	"man":          "local",
	"previews":     "joins previews, so it must stay visible",
}

func TestEveryCustomerCommandHasACapability(t *testing.T) {
	paths := map[string]bool{}
	for _, command := range *main.CliCommands {
		paths[command.Name] = true
		for _, sub := range command.Subcommands {
			paths[command.Name+" "+sub.Name] = true
			for _, leaf := range sub.Subcommands {
				paths[command.Name+" "+sub.Name+" "+leaf.Name] = true
			}
		}
		if command.Audience != 0 {
			continue
		}
		_, ungated := ungatedCommands[command.Name]
		owned := main.CliPathCapability(command.Name) != ""
		switch {
		case !owned && !ungated:
			t.Errorf("customer command %q has no capability: add it to a capability's cli list in pkg/productcap/catalog.json, or to ungatedCommands with a reason", command.Name)
		case owned && ungated:
			t.Errorf("command %q is owned by %q and also listed as ungated", command.Name, main.CliPathCapability(command.Name))
		}
	}
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range catalog.Capabilities {
		for _, path := range capability.CLI {
			if !paths[path] {
				t.Errorf("capability %q owns cli path %q, which is not a command or verb in the manifest", capability.ID, path)
			}
		}
	}
}

func TestCommandCapabilityPath(t *testing.T) {
	for _, tc := range []struct {
		args []string
		path string
		owns string
	}{
		{[]string{"realtime", "list"}, "realtime list", "realtime"},
		{[]string{"bucket", "lifecycle", "my-app"}, "bucket lifecycle", "object-lifecycle"},
		{[]string{"app", "my-app", "tcp", "list"}, "app tcp list", "tcp-listeners"},
		{[]string{"app", "my-app", "rename"}, "app rename", "apps"},
		{[]string{"crons", "--json"}, "crons", "crons"},
		// Hunt 14 H14-89: these routes belong to previews hidden on
		// production-us, so the commands must be owned by them too.
		{[]string{"debug", "requests", "list", "my-app"}, "debug requests list", "production-debugger"},
		{[]string{"debug", "compare", "my-app"}, "debug compare", "production-debugger"},
		{[]string{"debug", "bundle", "my-app", "req"}, "debug bundle", "production-debugger"},
		{[]string{"debug", "coverage", "my-app"}, "debug coverage", "debug"},
		{[]string{"app", "my-app", "security"}, "app security", "signed-deploys"},
		{[]string{"app", "my-app", "routes"}, "app routes", "route-insights"},
		// Hunt 14 H14-100: the profile gate route belongs to the internal
		// traffic-profiles capability.
		{[]string{"canary", "gate", "dep"}, "canary gate", "traffic-profiles"},
		{[]string{"deploy"}, "deploy", ""},
	} {
		path := main.CommandCapabilityPath(tc.args)
		if path != tc.path || main.CliPathCapability(path) != tc.owns {
			t.Errorf("%q → path %q owned by %q, want %q owned by %q", tc.args, path, main.CliPathCapability(path), tc.path, tc.owns)
		}
	}
}

// runCLI runs the CLI and returns its exit code, stdout and stderr.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &out, &errOut
	defer func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr }()
	code := main.Run(args)
	return code, out.String(), errOut.String()
}

func listsCommand(help, name string) bool {
	return strings.Contains(help, "\n  "+name+" ")
}

func TestLoggedOutHelpShowsTierDefaults(t *testing.T) {
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))

	_, help, _ := runCLI(t, "help", "--all")
	if !listsCommand(help, "cron") || listsCommand(help, "realtime") {
		t.Fatalf("logged-out help must list GA cron and hide preview realtime:\n%s", help)
	}
	if code, _, stderr := runCLI(t, "realtime", "--help"); code == 0 || !strings.Contains(stderr, `unknown command "realtime"`) {
		t.Fatalf("realtime --help = %d %q, want unknown command", code, stderr)
	}
	if _, script, _ := runCLI(t, "completion", "bash"); strings.Contains(script, " realtime ") {
		t.Fatal("bash completion offers the hidden realtime command")
	}
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	if _, script, _ := runCLI(t, "completion", "bash"); !strings.Contains(script, " realtime ") {
		t.Fatal("bash completion must offer realtime once every capability is released")
	}
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	if _, reference, _ := runCLI(t, "man", "--markdown"); !strings.Contains(reference, "realtime") {
		t.Fatal("the generated reference must document every released command")
	}
}

// capabilitiesAPI answers GET /v1/capabilities with hidden, and every other
// request with an empty list.
type capabilitiesAPI struct {
	mu     sync.Mutex
	hidden []string
	reads  atomic.Int32
}

func (a *capabilitiesAPI) setHidden(hidden ...string) {
	a.mu.Lock()
	a.hidden = hidden
	a.mu.Unlock()
}

func (a *capabilitiesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != "/v1/capabilities" {
		_, _ = w.Write([]byte("[]"))
		return
	}
	a.reads.Add(1)
	a.mu.Lock()
	hidden := a.hidden
	a.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"registry_version": 1, "plan": "pro", "capabilities": []any{}, "hidden_capabilities": hidden})
}

func TestAccountViewHidesCommandsAndRereadsBeforeRefusing(t *testing.T) {
	api := &capabilitiesAPI{}
	server := httptest.NewServer(api)
	defer server.Close()
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	api.setHidden("crons", "object-lifecycle")

	_, help, _ := runCLI(t, "help", "--all")
	if listsCommand(help, "cron") || !listsCommand(help, "realtime") {
		t.Fatalf("account help must follow the server, not tier defaults:\n%s", help)
	}
	if got := api.reads.Load(); got != 1 {
		t.Fatalf("capability reads = %d, want 1", got)
	}
	if _, _, _ = runCLI(t, "help", "--all"); api.reads.Load() != 1 {
		t.Fatal("a fresh cached view must not be read again")
	}
	if code, _, stderr := runCLI(t, "bucket", "lifecycle", "--help"); code == 0 || !strings.Contains(stderr, `unknown command "bucket lifecycle"`) {
		t.Fatalf("hidden verb help = %d %q, want unknown command", code, stderr)
	}

	code, _, stderr := runCLI(t, "crons", "list", "my-app")
	if code == 0 || !strings.Contains(stderr, `unknown command "crons list"`) {
		t.Fatalf("hidden command = %d %q, want unknown command", code, stderr)
	}
	if got := api.reads.Load(); got != 2 {
		t.Fatalf("capability reads = %d, want a fresh read before refusing", got)
	}

	api.setHidden()
	if _, _, stderr := runCLI(t, "crons", "list", "my-app"); strings.Contains(stderr, "unknown command") {
		t.Fatalf("a capability released since the cached view must run: %q", stderr)
	}
}

func TestLocalCommandsRunWhileTheirCapabilityIsHidden(t *testing.T) {
	api := &capabilitiesAPI{}
	server := httptest.NewServer(api)
	defer server.Close()
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	api.setHidden("data-api")
	if _, help, _ := runCLI(t, "help", "--all"); listsCommand(help, "data-api") {
		t.Fatal("a hidden capability stays out of help")
	}
	// The local development server never calls the platform.
	if _, _, stderr := runCLI(t, "data-api", "dev", "--no-such-flag"); strings.Contains(stderr, "unknown command") {
		t.Fatalf("data-api dev was refused: %q", stderr)
	}
	if code, _, stderr := runCLI(t, "data-api", "types", "notes"); code == 0 || !strings.Contains(stderr, `unknown command "data-api types"`) {
		t.Fatalf("a platform command of a hidden capability = %d %q, want unknown command", code, stderr)
	}
}

// Hunt 14 H14-89: with the production-debugger preview hidden, `debug`
// listed `requests`, `compare` and `bundle`, which answered only "no such
// API route", and `invocations list --app` pointed at `debug requests list`.
func TestHiddenDebuggerSubcommandsLeaveDebugUsage(t *testing.T) {
	api := &capabilitiesAPI{}
	server := httptest.NewServer(api)
	defer server.Close()
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	api.setHidden("production-debugger", "signed-deploys")

	usage := debugUsageOutput(t, "debug")
	if !strings.Contains(usage, "usage: gregale debug <coverage|dependencies|running|regressions>") {
		t.Fatalf("debug usage with the debugger hidden = %q", usage)
	}
	help := debugUsageOutput(t, "debug", "--help")
	if strings.Contains(help, "requests list") || strings.Contains(help, "  compare ") || !strings.Contains(help, "  coverage ") {
		t.Fatalf("debug --help with the debugger hidden:\n%s", help)
	}
	if code, _, stderr := runCLI(t, "debug", "requests", "list", "my-app"); code == 0 || !strings.Contains(stderr, `unknown command "debug requests list"`) {
		t.Fatalf("hidden debug requests = %d %q, want unknown command", code, stderr)
	}
	if code, _, stderr := runCLI(t, "app", "my-app", "security"); code == 0 || !strings.Contains(stderr, `unknown command "app security"`) {
		t.Fatalf("hidden app security = %d %q, want unknown command", code, stderr)
	}
	if hint := debugUsageOutput(t, "invocations", "list", "--app", "my-app"); strings.Contains(hint, "debug requests") || !strings.Contains(hint, "gregale logs <slug> --source http") {
		t.Fatalf("invocations --app hint names a hidden command: %q", hint)
	}
	// Verb lists after a mistyped verb follow the same view.
	if choices := debugUsageOutput(t, "app", "my-app", "idle"); strings.Contains(choices, "security") || !strings.Contains(choices, "health") {
		t.Fatalf("app verb list names a hidden subcommand: %q", choices)
	}
	if typo := debugUsageOutput(t, "debug", "requsts"); strings.Contains(typo, `"requests"`) {
		t.Fatalf("a typo must not suggest a hidden subcommand: %q", typo)
	}

	api.setHidden()
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache2.json"))
	if usage := debugUsageOutput(t, "debug"); !strings.Contains(usage, "<requests|coverage|dependencies|running|regressions|compare|bundle>") {
		t.Fatalf("debug usage with every capability shown = %q", usage)
	}
}

// debugUsageOutput runs the CLI with the process streams swapped, because
// cmdDebug prints its usage straight to os.Stderr.
func debugUsageOutput(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, restore := main.SwapIO(t)
	main.Run(args)
	restore()
	return stdout.String() + stderr()
}
