package main_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// The tests in this file pin the first-run copy and defaults that a new
// customer meets between install and their first deployed app (UX spec §1:
// under five minutes, honest wake copy, one next action per error).

func TestStartPromptConfirmYesDefaultsToYes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		input string
		want  bool
	}{{"\n", true}, {"y\n", true}, {"n\n", false}} {
		var out bytes.Buffer
		var prompt main.StartPrompt
		*prompt.Reader() = bufio.NewReader(strings.NewReader(tc.input))
		*prompt.Writer() = &out
		got, err := prompt.ConfirmYes(ctx, "Open your browser to sign in")
		if err != nil || got != tc.want || !strings.Contains(out.String(), "Open your browser to sign in (Y/n) [y]: ") {
			t.Errorf("input %q: confirmed=%t err=%v prompt=%q", tc.input, got, err, out.String())
		}
	}
	var prompt main.StartPrompt
	*prompt.Writer() = io.Discard
	*prompt.Reader() = bufio.NewReader(strings.NewReader("\n"))
	if got, err := prompt.Confirm(ctx, "Deploy this app"); got || err != nil {
		t.Errorf("Enter confirmed a deploy: %t, %v", got, err)
	}
	*prompt.Reader() = bufio.NewReader(strings.NewReader(""))
	if got, err := prompt.ConfirmYes(ctx, "Open your browser to sign in"); got || !errors.Is(err, io.EOF) {
		t.Errorf("EOF took the default: %t, %v", got, err)
	}
}

// Pressing Enter at the sign-in question starts the browser login instead
// of ending the guided start.
func TestStartSignInEnterStartsBrowserLogin(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		wantLogin   bool
	}{{"enter", "\n", true}, {"no", "n\n", false}} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := main.StartTestEnvironment(t)
			t.Setenv("FAAS_TOKEN", "")
			main.SetFakeKeyring(t)
			var minted atomic.Bool
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/cli-auth/code" {
					minted.Store(true)
				}
				http.NotFound(w, r)
			})
			runner := main.StartTestRunner(t, main.StartTestSource(t), tc.input)
			// The login endpoint 404s, so the flow stops either way.
			if code := runner.Authenticate(); code == 0 {
				t.Fatalf("authenticate succeeded without a login; stdout=%s", stdout)
			}
			if minted.Load() != tc.wantLogin {
				t.Fatalf("browser login started=%t, want %t; stderr=%s", minted.Load(), tc.wantLogin, stderr())
			}
			if !strings.Contains(stdout.String(), "Open your browser to sign in (Y/n) [y]: ") {
				t.Fatalf("sign-in prompt does not show its Yes default: %s", stdout)
			}
		})
	}
}

func TestRenderHintRowHasNoEmoji(t *testing.T) {
	main.ResetJSONOut(t)
	old := *main.TestOnlyTTY
	t.Cleanup(func() { *main.TestOnlyTTY = old })
	for _, tty := range []bool{true, false} {
		*main.TestOnlyTTY = &tty
		var buf bytes.Buffer
		main.RenderHintRow(&buf, "bind to 0.0.0.0")
		if got := buf.String(); got != "  hint: bind to 0.0.0.0\n" {
			t.Errorf("tty=%t: hint row = %q", tty, got)
		}
	}
}

// The deploy success copy is the UX spec §2.5 block verbatim, so it
// promises no wake time and does not call wakes free.
func TestDeployColdWakeSentenceMatchesUXSpec(t *testing.T) {
	spec, err := os.ReadFile("../../docs/faas_ux_spec.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(spec), "### 2.5 ")
	if !ok {
		t.Fatal("UX spec has no §2.5")
	}
	_, block, _ := strings.Cut(section, "```\n")
	block, _, _ = strings.Cut(block, "```")
	_, want, ok := strings.Cut(block, "\n")
	if !ok || !strings.HasPrefix(block, "✓ Deployed.") {
		t.Fatalf("UX spec §2.5 block changed shape:\n%s", block)
	}
	stdout, _, restore := main.SwapIO(t)
	main.PrintDeployColdWakeSentence()
	restore()
	got := stdout.String()
	if got != want {
		t.Fatalf("cold-wake copy drifted from UX spec §2.5:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if strings.ContainsAny(got, "0123456789") || strings.Contains(strings.ToLower(got), "free") {
		t.Fatalf("cold-wake copy promises a number or says free: %q", got)
	}
}

func TestAppsEmptyStateNamesStart(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.AppResponse{})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"apps"})
	restore()
	if code != 0 || stdout.String() != "No apps yet. Create one with gregale start\n" {
		t.Fatalf("apps exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr())
	}
}

func TestNotLoggedInSaysItOnce(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	main.ResetJSONOut(t)
	t.Setenv("FAAS_TOKEN", "")
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"whoami"})
	restore()
	got := stderr()
	if code != 2 || strings.Count(strings.ToLower(got), "not logged in") != 1 || !strings.Contains(got, "\n  Run gregale login, then retry.\n") {
		t.Fatalf("whoami exit=%d stderr=%q", code, got)
	}
	// A caller with its own title keeps the cause in the detail.
	_, stderr, restore = main.SwapIO(t)
	main.PrintErr("MCP release", main.ErrAuth(errors.New("not logged in — Run gregale login, then retry.")))
	restore()
	if got := stderr(); !strings.Contains(got, "not logged in — Run gregale login") {
		t.Fatalf("other titles lost the cause: %q", got)
	}
}

// Swapped adjacent letters are the most common typo; they count as one edit.
func TestTypoSuggestionsCoverTranspositions(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	main.ResetJSONOut(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"deplyo"}, "did you mean 'gregale deploy'?"},
		{[]string{"relase"}, "did you mean 'gregale release'?"},
		{[]string{"deploy", "--naem", "demo"}, "Did you mean --name?"},
		{[]string{"logs", "--folow"}, "Did you mean --follow?"},
	} {
		_, stderr, restore := main.SwapIO(t)
		code := main.Run(tc.args)
		restore()
		if got := stderr(); code == 0 || !strings.Contains(got, tc.want) {
			t.Errorf("run(%v) exit=%d, want %q in stderr: %q", tc.args, code, tc.want, got)
		}
	}
}

func TestMenuEnterStartsGuidedSetup(t *testing.T) {
	old := *main.OsStdin
	t.Cleanup(func() { *main.OsStdin = old })
	*main.OsStdin = strings.NewReader("\n")
	stdout, _, restore := main.SwapIO(t)
	defer restore()
	actions := main.MenuActions()
	index, err := main.ChooseMenuAction(actions)
	if err != nil {
		t.Fatal(err)
	}
	if args := *actions[index].Args(); len(args) != 1 || args[0] != "start" {
		t.Fatalf("Enter chose %q (%v), want the guided start", *actions[index].Label(), args)
	}
	if last := actions[len(actions)-1]; *last.Label() != "Exit" || *last.Args() != nil {
		t.Fatalf("Exit is not the last choice: %q", *last.Label())
	}
	if !strings.Contains(stdout.String(), "  1. Get an app live with guided setup\n") || !strings.Contains(stdout.String(), "Choose [1]: ") {
		t.Fatalf("menu does not show start as the default:\n%s", stdout)
	}
}

func TestLoginFirstRunNamesOnlyStart(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "jane@example.test", Plan: "free"})
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode([]api.AppResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	token := main.TestAPIKey('d')
	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"login", "--token", token})
	restore()
	if code != 0 || !strings.HasSuffix(stdout.String(), "\nYou're in. Next step — deploy your first app:\n  gregale start\n") {
		t.Fatalf("login exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr())
	}
}
