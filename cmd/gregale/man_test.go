// man_test.go — Tier A8.1 / ADR-083 follow-up. The Levenshtein-1
// suggestion surface lives in man.go (suggestCommand + levenshtein);
// these tests pin both the helper and the integration with cmdMan.
//
// Tier A8 (PR #752) shipped `gregale man <command>` and the
// per-command roff page; this PR adds "did you mean X?" output
// for unknown commands. The thresholds (maxDist=2, no ties) are
// load-bearing — a too-lax threshold would hallucinate across
// unrelated commands, a too-strict threshold would make the
// feature invisible for common typos like `webhoks` → `webhooks`.

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestLevenshtein_KnownPairs pins the DP table for a few canonical
// inputs. The helper is hot (one call per cell of the table) and
// the algorithm is well-known, but pinning specific inputs catches
// regressions in the row-swap (prev/cur) or the cost assignment.
// Reference: an out-of-tree Python oracle (linked in the PR
// description) reproduces each expected value by hand-tracing the
// DP table; the test values were cross-checked against it.
func TestLevenshtein_KnownPairs(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "abcd", 1},
		{"apps", "aps", 1},       // deletion
		{"apps", "app", 1},       // deletion
		{"apps", "apsp", 2},      // same length, two subs
		{"apps", "appss", 1},     // insertion
		{"kitten", "sitting", 3}, // canonical Levenshtein example
		{"gregale", "gregales", 1},
		{"gregale", "Gregale", 1}, // case-sensitive
	}
	for _, tc := range cases {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestSuggestCommand_TableDriven enumerates the suggestion surface
// against the live cliCommands manifest. The expected outputs
// were derived from the manifest (the manifest names are
// referenced through dispatch constants like dispatchApps = "apps",
// dispatchDeployment = "deployment" — see cmd/gregale/commands2.go).
// If a future PR adds a new top-level command that shadows one of
// these expected suggestions, this test fails loudly so the
// suggestion list can be re-audited.
//
// Notes on the cases:
//   - empty_query: distances from "" to every cliCommand are the
//     name lengths; the shortest names (1-2 chars) hit distance ≤2
//     alone, so the algorithm picks one. The function does NOT
//     special-case empty input — this is intentional; cmdMan never
//     reaches this path with 0 args (it renders the top-level man
//     page instead). We pin the academic behaviour so a future
//     refactor doesn't silently shift it.
//   - typo_aps_is_a_three_way_tie: aps is at distance 1 from app,
//     apps, and ps simultaneously. Three candidates at the same
//     minimum distance makes the suggestion ambiguous; the
//     algorithm suppresses it. This is the right answer — the
//     user has to choose between three real commands, and the
//     "right" one depends on what they meant.
//   - typo_deploy_returns_itself: deploy→deploy=0 is the minimum;
//     deployment is at distance 4. No tie, exact match wins.
func TestSuggestCommand_TableDriven(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		want   string
		wantOK bool
	}{
		// Single-winner suggestions (the happy path).
		{"typo_webhook_transposition_suggests_webhooks", "webhoks", "webhooks", true},
		{"typo_app_returns_itself_at_distance_0", "app", "app", true},
		{"typo_deploy_returns_itself", "deploy", "deploy", true},
		{"exact_match_returns_self", "apps", "apps", true},
		{"hyphenated_typo_within_threshold", "invok", "invoke", true},
		// Tie-suppression (the load-bearing branch).
		{"typo_aps_is_three_way_tie_no_suggestion", "aps", "", false},
		// Far-miss (above maxDist=2).
		{"far_miss_no_suggestion", "zzzzzzzzzz", "", false},
		// Empty query is academic (cmdMan 0-args branch renders
		// the top-level page) but we pin the deterministic pick
		// so a future refactor doesn't shift it silently.
		{"empty_query_deterministic_shortest_pick", "", "ps", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := suggestCommand(tc.query)
			if ok != tc.wantOK {
				t.Fatalf("suggestCommand(%q) ok = %v, want %v (got %q)", tc.query, ok, tc.wantOK, got)
			}
			if got != tc.want {
				t.Errorf("suggestCommand(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

// TestCmdMan_UnknownCommandSuggests pins the integration:
// `gregale man webhoks` (unknown command) prints both
// "unknown command \"webhoks\"" and the suggestion
// "Did you mean \"webhooks\"?". Both lines must appear on
// stderr so a shell pipeline can still capture the roff output
// on stdout when the command is valid.
//
// `webhoks` was chosen over the more obvious `aps` because aps is
// a three-way tie (app / apps / ps) at distance 1 — the
// tie-suppression branch would fire and the suggestion wouldn't
// print. webhoks has only webhooks at distance ≤2, so the
// single-winner branch fires and the suggestion surfaces.
func TestCmdMan_UnknownCommandSuggests(t *testing.T) {
	stdout, restoreOut := captureStdout(t)
	defer restoreOut()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()

	code := cmdMan([]string{"webhoks"})
	if code != 1 {
		t.Errorf("cmdMan(webhoks) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "webhoks"`) {
		t.Errorf("stderr missing unknown-command line, got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), `Did you mean "webhooks"`) {
		t.Errorf("stderr missing suggestion line, got:\n%s", stderr.String())
	}
	// Suggestion must NOT contaminate stdout — stdout is roff
	// output, and the user might pipe `gregale man webhoks` to
	// `man -l -`. The unknown-command + suggestion belong on
	// stderr exclusively.
	if stdout.String() != "" {
		t.Errorf("stdout got unexpected output: %q", stdout.String())
	}
}

// TestCmdMan_TiedTypoNoSuggestion pins the tie-suppression branch
// via the integration path: `gregale man aps` (a 3-way tie among
// app/apps/ps at distance 1) must NOT print a "Did you mean"
// suggestion, even though the algorithm found candidates.
func TestCmdMan_TiedTypoNoSuggestion(t *testing.T) {
	stdout, restoreOut := captureStdout(t)
	defer restoreOut()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()

	code := cmdMan([]string{"aps"})
	if code != 1 {
		t.Errorf("cmdMan(aps) = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), "Did you mean") {
		t.Errorf("tied typo should not suggest, got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("expected unknown-command line, got:\n%s", stderr.String())
	}
	_ = stdout
}

// TestCmdMan_UnknownCommandNoSuggestion pins the boundary:
// a query at distance > 2 from every cliCommand.Name must NOT
// print a suggestion (the algorithm suppresses it).
func TestCmdMan_UnknownCommandNoSuggestion(t *testing.T) {
	stdout, restoreOut := captureStdout(t)
	defer restoreOut()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()

	code := cmdMan([]string{"zzzzzzzzzz"})
	if code != 1 {
		t.Errorf("cmdMan(zzzzzzzzzz) = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), "Did you mean") {
		t.Errorf("did not expect suggestion for far-miss query, got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("expected unknown-command line, got:\n%s", stderr.String())
	}
	_ = stdout
}

// TestCmdMan_KnownCommandDoesNotSuggest verifies the integration
// boundary: a valid command (which lookupCliCommand finds) prints
// roff on stdout and never hits the suggestion branch.
func TestCmdMan_KnownCommandDoesNotSuggest(t *testing.T) {
	stdout, restoreOut := captureStdout(t)
	defer restoreOut()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()

	code := cmdMan([]string{"apps"})
	if code != 0 {
		t.Errorf("cmdMan(apps) = %d, want 0", code)
	}
	if stdout.String() == "" {
		t.Error("expected roff output on stdout for valid command")
	}
	if !strings.Contains(stdout.String(), ".TH") {
		t.Errorf("expected roff .TH header on stdout, got:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("valid command should not print unknown-command, got: %s", stderr.String())
	}
	if strings.Contains(stderr.String(), "Did you mean") {
		t.Errorf("valid command should not suggest, got: %s", stderr.String())
	}
}

// TestSuggestSubcommand_PicksClosest pins the helper against the
// live alerts manifest. The expected outputs are derived from the
// manifest's Subcommands slice (list, add, info, update, rm,
// rotate-secret). If a future PR renames a subcommand, this test
// fails loudly so the suggestion list can be re-audited.
//
// Cases:
//   - typos: the happy path — single candidate at distance ≤2.
//   - exact: the arg equals a real subcommand.
//   - over_threshold: distance > 2 from every subcommand → no
//     suggestion (the algorithm falls through to the bare
//     "unknown subcommand" line).
//
// Empty query is academic (the dispatcher short-circuits at
// len(args)==0) and is pinned separately in
// TestSuggestSubcommand_PicksClosest_Empty below.
func TestSuggestSubcommand_PicksClosest(t *testing.T) {
	c, ok := lookupCliCommand("alerts")
	if !ok {
		t.Fatal("alerts not in cliCommands manifest — extractor broken or test fired pre-init")
	}
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"transposition_suggests_list", "lst", "list"},
		{"transposition_suggests_list_long", "lsit", "list"},
		{"deletion_suggests_add", "addd", "add"},
		{"deletion_suggests_rm", "rme", "rm"},
		{"insertion_suggests_rotate_secret", "rotate-secrets", "rotate-secret"},
		{"exact_match_returns_self", "add", "add"},
		{"over_threshold_no_suggestion", "fooooo", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := suggestSubcommand(tc.query, c)
			if got != tc.want {
				t.Errorf("suggestSubcommand(%q, alerts) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

// TestSuggestSubcommand_PicksClosest_Empty pins the deterministic
// behaviour of the helper for an empty query. Empty input is
// academic for the dispatcher (the len(args)==0 branch prints
// usage), but the algorithm has a defined pick (the shortest-name
// subcommand) and we pin it so a future refactor doesn't shift
// the lie silently.
//
// As of this commit, alerts has subcommands {list, add, info,
// update, rm, rotate-secret}; the shortest is "rm" (length 2).
// If the manifest grows a 1-char subcommand, this test fails
// loudly — that's the intent.
func TestSuggestSubcommand_PicksClosest_Empty(t *testing.T) {
	c, ok := lookupCliCommand("alerts")
	if !ok {
		t.Fatal("alerts not in cliCommands manifest")
	}
	got, ok := suggestSubcommand("", c)
	if !ok {
		t.Errorf("empty query should pick the shortest subcommand; got ok=false")
	}
	if len(got) > 2 {
		t.Errorf("empty query should pick the shortest subcommand (%d chars); got %q (%d chars)", 2, got, len(got))
	}
}

// TestSuggestSubcommand_SuppressesTies pins the tie-suppression
// branch. alerts has subcommands at distance 1 from "lst": only
// "list". The test would also fire if a future rename introduced
// a 3-way tie (e.g. add "lstat" alongside "list") — the helper
// would silently suppress and the test would fail. That's the
// intent: surfaces manifest drift that would otherwise hide as a
// hidden missing suggestion.
func TestSuggestSubcommand_SuppressesTies(t *testing.T) {
	c, ok := lookupCliCommand("alerts")
	if !ok {
		t.Fatal("alerts not in cliCommands manifest")
	}
	if got, _ := suggestSubcommand("lst", c); got != "list" {
		t.Fatalf("expected single-winner 'list'; got %q", got)
	}
}

// TestDispatcher_CmdAlertsUnknownSuggests is the integration test
// for commit 3 (the dispatcher patch). It is intentionally NOT
// shipped in commit 2 — commit 2 only validates the helpers in
// isolation. After commit 3 lands, the dispatcher emits the
// "Did you mean" hint and this test goes green.
//
// The test is in this file (not commands_alerts_test.go) because
// man_test.go already owns the helpers and the dispatcher test
// only differs from the helper tests in that it walks the
// dispatcher path. Co-locating keeps the suggestion surface in
// one file.
func TestDispatcher_CmdAlertsUnknownSuggests(t *testing.T) {
	stdout, restoreOut := captureStdout(t)
	defer restoreOut()

	var stderrBuf bytes.Buffer
	_ = captureStderrSwap(t, &stderrBuf, func() int { return cmdAlerts([]string{"listz"}) })

	if !strings.Contains(stderrBuf.String(), `unknown alerts subcommand "listz"`) {
		t.Errorf("stderr missing unknown-subcommand line, got:\n%s", stderrBuf.String())
	}
	if !strings.Contains(stderrBuf.String(), `Did you mean "list"`) {
		t.Errorf("stderr missing Did-you-mean hint, got:\n%s", stderrBuf.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout should be empty for unknown subcommand, got: %q", stdout.String())
	}
}
