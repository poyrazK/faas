package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

type helpGlobalFlag struct {
	Name, Description, Value, Placement string
	Spellings                           []string
	TakesValue                          bool `json:"takes_value"`
}

// adr: 20261115071638 — inherited options retain their parser scope.
func TestJSONHelpGlobalFlagContracts(t *testing.T) {
	setupJSONHelp(t)
	root := readJSONHelp(t, "--json", "help")
	if len(root.Flags) != 0 {
		t.Fatal("global options were mixed into root command-local flags")
	}
	want := map[string]struct{ value, sample string }{
		"http-timeout": {"DURATION", "250ms"}, "debug-http": {},
		"agent-reason": {"TEXT", "inspect command options"}, "json": {},
		"non-interactive": {}, "http-retries": {"N", "1"},
		"org": {"SLUG", "acme"}, "profile": {"NAME", "missing"}, "wait-for-approval": {},
	}
	if len(root.GlobalFlags) != len(want) {
		t.Fatalf("global options: got %d, want %d", len(root.GlobalFlags), len(want))
	}
	seen := map[string]bool{}
	for _, flag := range root.GlobalFlags {
		sample, ok := want[flag.Name]
		placement := "before_command"
		if flag.Name == "json" {
			placement = "before_separator"
			if !slices.Equal(flag.Spellings, []string{"--json", "-j"}) {
				t.Fatalf("JSON spellings: %v", flag.Spellings)
			}
		}
		if !ok || seen[flag.Name] || flag.Description == "" || flag.Value != sample.value || flag.TakesValue != (sample.value != "") || flag.Placement != placement || len(flag.Spellings) == 0 || flag.Spellings[0] != "--"+flag.Name {
			t.Fatalf("incorrect global contract: %+v", flag)
		}
		seen[flag.Name] = true
		for _, spelling := range flag.Spellings {
			t.Run(spelling, func(t *testing.T) {
				args := []string{spelling}
				if flag.TakesValue {
					args = append(args, sample.sample)
				}
				args = append(args, "--json", "help", "deploy")
				leaf := readJSONHelp(t, args...)
				if !reflect.DeepEqual(leaf.GlobalFlags, root.GlobalFlags) {
					t.Fatal("root and topic global contracts differ")
				}
			})
		}
	}
	alias := readJSONHelp(t, "--json", "help", "invoke")
	if !reflect.DeepEqual(alias.GlobalFlags, root.GlobalFlags) {
		t.Fatal("compatibility help lost inherited options")
	}
	deploy := readJSONHelp(t, "--json", "help", "deploy")
	found := false
	for _, local := range deploy.Flags {
		if slices.Contains(local.Spellings, "--profile") {
			found = true
			if local.Value != "SIZE" {
				t.Fatalf("command-local resource profile changed: %+v", local)
			}
		}
	}
	if !found {
		t.Fatal("command-local profile spelling was hidden by the connection option")
	}
}

func TestJSONHelpGlobalFlagsMatchHumanRoot(t *testing.T) {
	setupJSONHelp(t)
	document := readJSONHelp(t, "--json", "help")
	_, rest, ok := strings.Cut(main.TopLevelUsage(false, false), "\nGlobal flags:\n")
	if !ok {
		t.Fatal("human root help omitted global options")
	}
	block, _, ok := strings.Cut(rest, "Docs:")
	if !ok {
		t.Fatal("root help omitted its documentation link")
	}
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	index := 0
	for _, flag := range document.GlobalFlags {
		label := "  " + flag.Spellings[0]
		if flag.TakesValue {
			label += " " + flag.Value
		}
		if index >= len(lines) || !strings.HasPrefix(lines[index], label+" ") {
			t.Fatalf("human global option order/label differs at %s", flag.Name)
		}
		description := strings.TrimSpace(strings.TrimPrefix(lines[index], label))
		index++
		for index < len(lines) && !strings.HasPrefix(lines[index], "  --") {
			description += " " + strings.TrimSpace(lines[index])
			index++
		}
		if description != flag.Description {
			t.Fatalf("human/JSON description drift for %s: %q != %q", flag.Name, description, flag.Description)
		}
	}
	if index != len(lines) {
		t.Fatal("human help has undiscovered global options")
	}
}

func TestJSONHelpGlobalFlagsStayOfflineAndKeepPlacement(t *testing.T) {
	setupJSONHelp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_global-help-private")
	t.Setenv("GREGALE_AGENT_REASON", "private intent sentinel")
	document := readJSONHelp(t, "help", "deploy", "--json")
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"fp_test_global-help-private", "private intent sentinel", server.URL} {
		if strings.Contains(string(data), private) {
			t.Fatal("discovery exposed invocation state")
		}
	}
	for _, args := range [][]string{
		{"--json", "help", "--profile", "missing"},
		{"--json", "help", "--non-interactive"},
		{"--json", "help", "--", "--json"},
	} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		problemText := stderr()
		restore()
		var problem struct {
			Code     string `json:"code"`
			ExitCode int    `json:"exit_code"`
		}
		if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(problemText), &problem) != nil || problem.Code != "invalid_request" || problem.ExitCode != 1 {
			t.Fatalf("placement/boundary changed: args=%q exit=%d stdout=%s stderr=%s", args, code, out, problemText)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("help made %d API requests", requests.Load())
	}
}
