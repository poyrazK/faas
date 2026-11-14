package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

type helpDocument struct {
	SchemaVersion               int    `json:"schema_version"`
	Command                     string `json:"command"`
	RequestedCommand            string `json:"requested_command"`
	DocsURL                     string `json:"docs_url"`
	Aliases                     []string
	Positionals                 []string
	Examples                    []string
	Notes                       []string
	SubcommandsAfterPositionals bool `json:"subcommands_after_positionals"`
	FlagsAfterPositionals       bool `json:"flags_after_positionals"`
	Flags                       []struct {
		Name       string
		Spellings  []string
		TakesValue bool `json:"takes_value"`
		Required   bool
		Repeatable bool
		Choices    []string
	}
	Subcommands []struct{ Command, Description string }
}

func setupJSONHelp(t *testing.T) {
	t.Helper()
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	main.ResetJSONOut(t)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_JSON", "0")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "")
}

func readJSONHelp(t *testing.T, args ...string) helpDocument {
	t.Helper()
	out, stderr, restore := main.SwapIO(t)
	code := main.Run(args)
	errText := stderr()
	restore()
	if code != 0 || errText != "" {
		t.Fatalf("run(%q) = %d; stderr=%s", args, code, errText)
	}
	var document helpDocument
	decoder := json.NewDecoder(strings.NewReader(out.String()))
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("help is not JSON: %v: %s", err, out)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("help must be exactly one JSON document: %v", err)
	}
	if document.SchemaVersion != 1 || document.DocsURL == "" || document.Aliases == nil || document.Positionals == nil || document.Flags == nil || document.Examples == nil || document.Notes == nil || document.Subcommands == nil {
		t.Fatalf("missing schema metadata or array collections: %+v", document)
	}
	return document
}

func TestJSONHelpRootUsesCanonicalManifest(t *testing.T) {
	setupJSONHelp(t)
	document := readJSONHelp(t, "--json", "help")
	if document.Command != "gregale" || document.RequestedCommand != "gregale" {
		t.Fatalf("root command: %+v", document)
	}
	want := main.CliTreeManifest(main.PublicReferenceCommands())
	if len(document.Subcommands) != len(want) {
		t.Fatalf("root lists %d commands; manifest has %d", len(document.Subcommands), len(want))
	}
	seen := map[string]bool{}
	for i, command := range want {
		got := document.Subcommands[i]
		if got.Command != "gregale "+command.Name || got.Description != command.Short || seen[got.Command] {
			t.Fatalf("root command %d drifted: %+v", i, got)
		}
		seen[got.Command] = true
	}
	if !seen["gregale invocation"] || seen["gregale invoke"] || seen["gregale apps"] || seen["gregale realtime"] {
		t.Fatalf("root includes an earlier or hidden name: %v", seen)
	}
	operator := readJSONHelp(t, "--json", "help", "--all", "--operator")
	if !slices.ContainsFunc(operator.Subcommands, func(c struct{ Command, Description string }) bool { return c.Command == "gregale invoke" }) {
		t.Fatal("explicit compatibility discovery omitted the earlier invocation root")
	}
}

func TestJSONHelpAliasesAndLeafFlags(t *testing.T) {
	setupJSONHelp(t)
	for _, tc := range []struct{ topic, canonical string }{
		{"invocation run", "invocation run"},
		{"invoke", "invocation run"},
		{"invocations wait", "invocation wait"},
		{"crons add", "cron create"},
		{"cron create", "cron create"},
		{"release attempt get", "release attempt get"},
		{"app scale", "app scale"},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			document := readJSONHelp(t, append([]string{"--json", "help"}, strings.Fields(tc.topic)...)...)
			if document.Command != "gregale "+tc.canonical || document.RequestedCommand != "gregale "+tc.topic {
				t.Fatalf("wrong canonical target: %+v", document)
			}
		})
	}
	document := readJSONHelp(t, "--json", "help", "invocation", "run")
	if !slices.Contains(document.Aliases, "gregale invoke") {
		t.Fatalf("missing compatibility name: %v", document.Aliases)
	}
	invoke, _ := main.LookupCliCommand("invoke")
	if len(document.Flags) != len(invoke.Flags) || !slices.Equal(document.Positionals, invoke.Positionals) || len(document.Examples) == 0 {
		t.Fatalf("invocation manifest drift: %+v", document)
	}
	for i, flag := range invoke.Flags {
		got := document.Flags[i]
		wantValue := !flag.Bool && (flag.Value != "" || flag.Req || len(flag.ClosedSet) > 0)
		if got.Name != flag.Name || got.Required != flag.Req || got.Repeatable != flag.Repeatable || !slices.Equal(got.Choices, flag.ClosedSet) || got.TakesValue != wantValue {
			t.Errorf("flag %s drifted: %+v", flag.Name, got)
		}
		if !slices.Contains(got.Spellings, "--"+flag.Name) {
			t.Errorf("flag %s lacks accepted spelling", flag.Name)
		}
	}
}

func TestJSONHelpOfflineAndInvalidTopics(t *testing.T) {
	setupJSONHelp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_help-private")
	readJSONHelp(t, "--profile", "missing", "--debug-http", "--non-interactive", "--json", "help", "invocation", "run", "--all")
	for _, topic := range [][]string{
		{"not-a-command"}, {"invocation", "run", "unexpected"}, {"realtime"}, {"release", "approval", "list"},
		{"invocation", "--bad-option"}, {"--", "--json"}, {"invocation", "--operator"},
		{"fp_test_help-private"},
	} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(append([]string{"--json", "help"}, topic...))
		errText := stderr()
		restore()
		var problem struct {
			Code     string `json:"code"`
			ExitCode int    `json:"exit_code"`
		}
		if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(errText), &problem) != nil || problem.Code != "invalid_request" || problem.ExitCode != 1 || strings.Contains(errText, "fp_test_help-private") {
			t.Fatalf("invalid topic %q: exit=%d stdout=%q stderr=%q", topic, code, out, errText)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("local help contacted the API %d times", requests.Load())
	}
	// The mode does not persist into the next Run, and flag help retains its
	// existing human rendering for callers that use FAAS_JSON globally.
	for _, args := range [][]string{{"help", "invocation", "run"}, {"--json", "invocation", "run", "--help"}} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		errText := stderr()
		restore()
		if code != 0 || json.Valid(out.Bytes()) || !strings.Contains(out.String(), "Usage:") || errText != "" {
			t.Fatalf("human help changed: exit=%d stdout=%s stderr=%s", code, out, errText)
		}
	}
}

func TestJSONHelpCachedCapabilityFiltering(t *testing.T) {
	setupJSONHelp(t)
	api := &capabilitiesAPI{}
	api.setHidden("crons", "object-lifecycle")
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_help-cache")
	// Fill the existing account cache through the public capability read.
	if _, err := main.NewClient(server.URL, "fp_test_help-cache").GetCapabilities(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := api.reads.Load()
	document := readJSONHelp(t, "--json", "help")
	for _, command := range document.Subcommands {
		if command.Command == "gregale cron" {
			t.Fatal("cached hidden cron leaked into JSON discovery")
		}
	}
	bucket := readJSONHelp(t, "--json", "help", "bucket")
	for _, command := range bucket.Subcommands {
		if command.Command == "gregale bucket lifecycle" {
			t.Fatal("cached hidden nested capability leaked")
		}
	}
	if api.reads.Load() != before {
		t.Fatal("JSON help refreshed the cached capability view")
	}
	t.Setenv("FAAS_JSON", "1")
	readJSONHelp(t, "help", "invocation", "wait")
}
