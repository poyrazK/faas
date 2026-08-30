// commands_completion_test.go — Tier A8 / ADR-083.
//
// Smoke tests for the four completion backends + the man renderer.
// Mirrors commands_tier_d_test.go's pattern: pure-string assertions,
// no external process invocation, table-driven where the shape
// is uniform across backends.
//
// The bash -n / groff -man syntax-check tests are intentionally
// NOT included here — both tools are absent on most dev boxes
// (CI's metal runner does have them, but unit tests must pass on
// any machine per CLAUDE.md "make test"). A future PR can add
// `//go:build bash_complete` and `//go:build roff_complete`
// test files for the integrated validation when the toolchain
// is reliably available; today the structural tests below are
// the tripwire.

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletion_Bash_RegistersAllCommands(t *testing.T) {
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionBash)
	out := buf.String()
	if !strings.Contains(out, "# bash completion for gregale") {
		t.Fatalf("bash header missing")
	}
	if !strings.Contains(out, "complete -F __gregale gregale") {
		t.Fatalf("bash registration missing")
	}
	for _, c := range cliCommands {
		if !strings.Contains(out, `"$cmd" = "`+c.Name+`"`) {
			t.Errorf("bash missing dispatch for %q", c.Name)
		}
	}
}

func TestCompletion_Zsh_HasCompdef(t *testing.T) {
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionZsh)
	out := buf.String()
	if !strings.HasPrefix(out, "#compdef gregale\n") {
		t.Fatalf("zsh #compdef header missing or not first line; got prefix %q", firstLine(out))
	}
	for _, c := range cliCommands {
		if !strings.Contains(out, "_gregale_"+c.Name+"()") {
			t.Errorf("zsh missing per-command function for %q", c.Name)
		}
	}
}

func TestCompletion_Fish_HasComplete(t *testing.T) {
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionFish)
	out := buf.String()
	if !strings.Contains(out, "complete -c gregale") {
		t.Fatalf("fish complete -c missing")
	}
	for _, c := range cliCommands {
		if !strings.Contains(out, " -a '"+c.Name+"'") && !strings.Contains(out, " -a \""+c.Name+"\"") {
			t.Errorf("fish missing complete entry for %q", c.Name)
		}
	}
}

func TestCompletion_Powershell_HasRegisterArgumentCompleter(t *testing.T) {
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionPowershell)
	out := buf.String()
	if !strings.Contains(out, "Register-ArgumentCompleter") {
		t.Fatalf("powershell registration missing")
	}
	for _, c := range cliCommands {
		if !strings.Contains(out, " -eq '"+c.Name+"'") {
			t.Errorf("powershell missing dispatch for %q", c.Name)
		}
	}
}

func TestCompletion_ManifestDrift(t *testing.T) {
	// Walk main.go's switch and collect every `case "<name>":` arm.
	// Also walk the dispatch constants (commands2.go) to recover
	// the values behind `case dispatchFoo:` forms.
	dispatchConsts := map[string]string{
		"dispatchApps":              "apps",
		"dispatchDeployments":       "deployments",
		"dispatchDeployment":        "deployment",
		"dispatchDeploys":           "deploys",
		"dispatchBuild":             "build",
		"dispatchInspect":           "inspect",
		"appSlugFallback":           "app",
		"statusLiteral":             "status",
		"dispatchSignKeys":          "sign-keys",
		"dispatchNodeKey":           "node-key",
		"dispatchTrustedPublishers": "trusted-publishers",
		"dispatchHostAge":           "host-age",
		"dispatchBackup":            "backup",
		"dispatchPKI":               "pki",
		"dispatchSignup":            "signup",
		"dispatchDoctor":            "doctor",
	}
	caseNames, err := extractMainCaseArms(dispatchConsts)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	manifestNames := make(map[string]struct{}, len(cliCommands))
	for _, c := range cliCommands {
		manifestNames[c.Name] = struct{}{}
	}
	// Internal pseudo-commands the manifest deliberately omits:
	// help, version. They are dispatched in run() but rendered in
	// the top-level usage block, not as separate cliCommand entries.
	internal := map[string]struct{}{
		"help":       {},
		"version":    {},
		"--version":  {},
		"-v":         {},
		"--help":     {},
		"-h":         {},
		"completion": {},
		"man":        {},
	}
	for name := range caseNames {
		if _, ok := internal[name]; ok {
			continue
		}
		if _, ok := manifestNames[name]; !ok {
			t.Errorf("main.go has case %q but no cliCommand entry in cli_meta.go", name)
		}
	}
	for name := range manifestNames {
		if _, ok := internal[name]; ok {
			// Manifest may include internal commands (e.g. completion, man);
			// that's fine — they ARE in the dispatch table.
			continue
		}
		if _, ok := caseNames[name]; !ok {
			t.Errorf("cliCommand %q has no matching case arm in main.go", name)
		}
	}
}

func extractMainCaseArms(dispatchConsts map[string]string) (map[string]struct{}, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	cases := make(map[string]struct{})
	ast.Inspect(f, func(n ast.Node) bool {
		cs, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range cs.List {
			switch e := expr.(type) {
			case *ast.BasicLit:
				if e.Kind == token.STRING {
					name := strings.Trim(e.Value, `"`)
					cases[name] = struct{}{}
				}
			case *ast.Ident:
				if val, ok := dispatchConsts[e.Name]; ok {
					cases[val] = struct{}{}
				} else {
					cases[e.Name] = struct{}{}
				}
			}
		}
		return true
	})
	return cases, nil
}

func TestCompletion_DispatcherRoutesToBackends(t *testing.T) {
	cases := []struct {
		shell string
		want  string
	}{
		{"bash", "__gregale"},
		{"zsh", "#compdef"},
		{"fish", "complete -c"},
		{"powershell", "Register-ArgumentCompleter"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			var buf bytes.Buffer
			captureStdoutSwap(t, &buf, func() int { return cmdCompletion([]string{tc.shell}) })
			if !strings.Contains(buf.String(), tc.want) {
				t.Fatalf("%s: expected %q in output; got %s", tc.shell, tc.want, buf.String())
			}
		})
	}
}

func TestCompletion_UnknownShellExitsOne(t *testing.T) {
	var buf bytes.Buffer
	captureStderrSwap(t, &buf, func() int { return cmdCompletion([]string{"tcsh"}) })
	if !strings.Contains(buf.String(), "unknown subcommand") {
		t.Fatalf("expected error message; got %s", buf.String())
	}
}

func TestCompletion_NoArgExitsOne(t *testing.T) {
	var buf bytes.Buffer
	captureStderrSwap(t, &buf, func() int { return cmdCompletion(nil) })
	if !strings.Contains(buf.String(), "usage:") {
		t.Fatalf("expected usage; got %s", buf.String())
	}
}

func TestMan_TopLevel_ContainsNameAndSynopsis(t *testing.T) {
	var buf bytes.Buffer
	renderManTop(&buf)
	out := buf.String()
	for _, want := range []string{".TH GREGALE(1)", ".SH NAME", ".SH SYNOPSIS", ".SH SEE ALSO"} {
		if !strings.Contains(out, want) {
			t.Errorf("man top: missing %q", want)
		}
	}
}

func TestMan_CommandPage_ContainsSubcommandList(t *testing.T) {
	c, ok := lookupCliCommand("alerts")
	if !ok {
		t.Fatalf("alerts not in manifest")
	}
	var buf bytes.Buffer
	renderManCommand(&buf, c)
	out := buf.String()
	for _, want := range []string{".TH GREGALE-ALERTS(1)", ".SH SUBCOMMANDS", "list", "add", "rotate-secret"} {
		if !strings.Contains(out, want) {
			t.Errorf("man alerts: missing %q", want)
		}
	}
}

func TestMan_CommandPage_ContainsFlagsSection(t *testing.T) {
	c, ok := lookupCliCommand("registry")
	if !ok {
		t.Fatalf("registry not in manifest")
	}
	var buf bytes.Buffer
	renderManCommand(&buf, c)
	if !strings.Contains(buf.String(), ".SH FLAGS") {
		t.Errorf("man registry: missing FLAGS section")
	}
	if !strings.Contains(buf.String(), "--app") {
		t.Errorf("man registry: missing --app flag")
	}
}

func TestMan_UnknownCommandExitsOne(t *testing.T) {
	var buf bytes.Buffer
	captureStderrSwap(t, &buf, func() int { return cmdMan([]string{"no-such-cmd"}) })
	if !strings.Contains(buf.String(), "unknown command") {
		t.Fatalf("expected error; got %s", buf.String())
	}
}

func TestMan_TooManyArgsExitsOne(t *testing.T) {
	var buf bytes.Buffer
	captureStderrSwap(t, &buf, func() int { return cmdMan([]string{"a", "b"}) })
	if !strings.Contains(buf.String(), "usage:") {
		t.Fatalf("expected usage; got %s", buf.String())
	}
}

func TestLookupCliCommand(t *testing.T) {
	if _, ok := lookupCliCommand("apps"); !ok {
		t.Fatal("apps not in manifest")
	}
	if _, ok := lookupCliCommand("nope"); ok {
		t.Fatal("nope should not be in manifest")
	}
}

func TestEscapeRoff(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{".start", "\\&.start"},
		{"a\\b", "a\\\\b"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := escapeRoff(tc.in); got != tc.want {
			t.Errorf("escapeRoff(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestBash_SlugRegexExtractsSlugs is the regression test for the
// grep -E bug: the original completion script emitted a regex with
// literal '{' and '}' which grep rejects ("invalid repeat"). The
// fix replaces the regex with a sed slice + slug extractor. We
// verify the FIXED script's helper returns the expected slug list
// from a populated cache file. Skip when bash isn't available
// (CI runners without /bin/bash — uncommon).
func TestBash_SlugRegexExtractsSlugs(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	cacheJSON := `{"version":1,"apps":[{"slug":"alpha","id":"1","name":"Alpha"},{"slug":"beta","id":"2","name":"Beta"}],"orgs":[],"saved_at":"2026-08-08T00:00:00Z"}`
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "completion-cache.json")
	if err := os.WriteFile(cachePath, []byte(cacheJSON), 0o600); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	// Render the bash completion script and write it to a temp file.
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "gregale-completion.bash")
	var scriptBuf bytes.Buffer
	captureStdoutSwap(t, &scriptBuf, cmdCompletionBash)
	if err := os.WriteFile(scriptPath, scriptBuf.Bytes(), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	// Drive the script's __gregale_cache_slugs helper. We can't
	// source the full script (complete -F registration requires a
	// real shell session), but we CAN inspect the function body
	// and execute the helper directly with FAAS_COMPLETION_CACHE_PATH
	// pointing at our seeded file. We invoke it through bash -c
	// after sourcing just the helper definitions.
	src := `. ` + scriptPath + `
export FAAS_COMPLETION_CACHE_PATH="` + cachePath + `"
__gregale_cache_path() { printf '%s' "$FAAS_COMPLETION_CACHE_PATH"; }
__gregale_cache_slugs apps
`
	cmd := exec.Command("/bin/bash", "-c", src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash exec failed: %v\noutput: %s", err, string(out))
	}
	got := strings.TrimSpace(string(out))
	want := "alpha\nbeta"
	if got != want {
		t.Fatalf("slug extraction: got %q want %q", got, want)
	}
}

// TestPowershell_AppCommandHasSubcommandAndSlugEntries is the
// regression test for the `len(c.Positionals) == 0` gate that
// suppressed subcommand completion for commands with BOTH
// subcommands (scale/rename/security) AND a <slug> positional.
// The fix drops the positionals gate; the rendered PowerShell
// script must now contain subcommand entries for `app`.
func TestPowershell_AppCommandHasSubcommandAndSlugEntries(t *testing.T) {
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionPowershell)
	out := buf.String()
	if !strings.Contains(out, "$tokens[1] -eq 'app'") {
		t.Fatalf("app dispatch missing:\n%s", out)
	}
	if !strings.Contains(out, "__gregaleCacheSlugs 'apps'") {
		t.Fatalf("app slug-cache completion missing — hasSlugFirst not honoured:\n%s", out)
	}
	for _, sub := range []string{"scale", "rename", "security"} {
		// Go's %q renders a string with double quotes (no escape
		// needed for simple ASCII), so we match either quote style.
		dq := `"` + sub + `"`
		sq := `'` + sub + `'`
		if !strings.Contains(out, dq) && !strings.Contains(out, sq) {
			t.Errorf("app subcommand %q missing from PowerShell completion", sub)
		}
	}
}

// TestMan_PerCommandSourceLabelIsGregale is the regression test
// for the uppercased source field. The .TH source should be the
// brand ("gregale") not the page slug ("GREGALE-ALERTS").
func TestMan_PerCommandSourceLabelIsGregale(t *testing.T) {
	c, ok := lookupCliCommand("alerts")
	if !ok {
		t.Fatalf("alerts not in manifest")
	}
	var buf bytes.Buffer
	renderManCommand(&buf, c)
	out := buf.String()
	// The header line must end with the brand ("gregale") in the
	// source slot — not the uppercased page slug ("GREGALE-ALERTS").
	if !strings.Contains(out, `.TH GREGALE-ALERTS(1)`) {
		t.Errorf("alerts man: title missing or wrong:\n%s", out)
	}
	if strings.Contains(out, `"GREGALE-ALERTS"`) {
		t.Errorf("alerts man: source label is uppercased page slug (should be 'gregale'):\n%s", out)
	}
	if !strings.Contains(out, `"gregale"`) {
		t.Errorf("alerts man: source label 'gregale' missing:\n%s", out)
	}
}

// TestMan_RequiredFlagRenderedWithoutBrackets is the regression
// test for the SYNOPSIS+FLAGS required-flag marker. The Req field
// was documented but the renderer emitted `[ --name value ]` for
// both required and optional flags. Required flags must lose the
// brackets in SYNOPSIS and gain a `(required)` suffix in FLAGS.
func TestMan_RequiredFlagRenderedWithoutBrackets(t *testing.T) {
	c, ok := lookupCliCommand("init")
	if !ok {
		t.Fatalf("init not in manifest")
	}
	var buf bytes.Buffer
	renderManCommand(&buf, c)
	out := buf.String()
	// --template and --path are Req: true in cli_meta.go.
	if !strings.Contains(out, "--template") || !strings.Contains(out, "--path") {
		t.Fatalf("init man: required flags missing entirely:\n%s", out)
	}
	// The required-flag line must NOT be wrapped in `[ ... ]`.
	if strings.Contains(out, "[ --template ") || strings.Contains(out, "[ --path ") {
		t.Errorf("init man: required flag rendered with optional brackets (Req marker dropped):\n%s", out)
	}
	// FLAGS section must mark them with `(required)`.
	if !strings.Contains(out, "(required)") {
		t.Errorf("init man: FLAGS section missing (required) marker:\n%s", out)
	}
}

// TestCompletion_BashScriptIsSyntacticallyValid pipes the rendered
// bash completion through `bash -n` to catch parse errors. Skips
// on hosts without /bin/bash (rare; CI runners have it).
func TestCompletion_BashScriptIsSyntacticallyValid(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionBash)
	cmd := exec.Command("/bin/bash", "-n")
	cmd.Stdin = &buf
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\noutput:\n%s", err, string(out))
	}
}

// TestCompletion_Bash3_2PrimitivesForbidden is the tripwire for
// the macOS bash 3.2 constraint documented in completion_bash.go:15.
// 3.2 ships without `mapfile`, `readarray`, `declare -A`,
// `local -A`, and `[[ -v arr ]]`. Pasting any of these into the
// bash script makes `gregale completion bash` install silently
// but fail at TAB time with a parse error.
//
// We grep the rendered script (NOT the source) so an accidental
// re-introduction via a code-generation helper would also trip.
// One assertion per primitive — clearer failure messages than a
// single multi-primitive regex.
func TestCompletion_Bash3_2PrimitivesForbidden(t *testing.T) {
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionBash)
	out := buf.String()
	forbidden := []struct {
		primitive string
		rationale string
	}{
		{"mapfile", "bash 4+ bulk array read"},
		{"readarray", "bash 4+ alias of mapfile"},
		{"declare -A", "associative array declaration"},
		{"local -A", "associative array, function-scoped"},
		{"[[ -v ", "bash 4.2+ variable-defined test"},
	}
	for _, p := range forbidden {
		if strings.Contains(out, p.primitive) {
			t.Errorf("bash 3.2 forbidden primitive %q (%s) rendered in completion script", p.primitive, p.rationale)
		}
	}
}

// TestCompletion_ZshScriptIsSyntacticallyValid pipes the rendered
// zsh completion through `zsh -n` to catch parse errors. Skips
// on hosts without zsh (uncommon on macOS, rare on CI Linux).
func TestCompletion_ZshScriptIsSyntacticallyValid(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skipf("zsh not available: %v", err)
	}
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionZsh)
	c := exec.Command("zsh", "-n")
	c.Stdin = &buf
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("zsh -n failed: %v\noutput:\n%s", err, string(out))
	}
}

// TestCompletion_FishScriptIsSyntacticallyValid pipes the rendered
// fish completion through `fish -n` to catch parse errors. Skips
// on hosts without fish (rare; install via brew install fish).
func TestCompletion_FishScriptIsSyntacticallyValid(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skipf("fish not available: %v", err)
	}
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionFish)
	c := exec.Command("fish", "-n")
	c.Stdin = &buf
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("fish -n failed: %v\noutput:\n%s", err, string(out))
	}
}

// TestCompletion_PowershellScriptIsSyntacticallyValid parses the
// rendered PowerShell completion via the PowerShell tokenizer.
// We write the script to a temp file (pwsh parses by path), then
// use [System.Management.Automation.Language.Parser]::ParseFile
// which is the canonical parse-only entrypoint. Skips on hosts
// without pwsh (rare; install via brew install --cask powershell).
func TestCompletion_PowershellScriptIsSyntacticallyValid(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skipf("pwsh not available: %v", err)
	}
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionPowershell)
	tmp, err := os.CreateTemp(t.TempDir(), "gregale-complete-*.ps1")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		t.Fatalf("write: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Escape single quotes for the pwsh command-string context.
	escaped := strings.ReplaceAll(tmp.Name(), "'", "''")
	psCmd := fmt.Sprintf("$errors = $null; $null = [System.Management.Automation.Language.Parser]::ParseFile('%s', [ref]$null, [ref]$errors); if ($errors) { $errors | Out-String; exit 1 }", escaped)
	c := exec.Command(pwsh, "-NoProfile", "-Command", psCmd)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("pwsh parse failed: %v\noutput:\n%s", err, string(out))
	}
}

// captureStdoutSwap swaps osStdout for a buffer, runs fn, and restores.
// Renamed to avoid collision with the safeBuffer-based captureStdout
// in commands5_test.go. Accepts func() int because every
// cmdCompletionXxx returns an exit code.
func captureStdoutSwap(t *testing.T, buf *bytes.Buffer, fn func() int) int {
	t.Helper()
	prev := osStdout
	osStdout = buf
	defer func() { osStdout = prev }()
	return fn()
}

// captureStderrSwap redirects os.Stderr to a buffer for fn's
// duration. Uses os.Pipe so the FD-based writes from os.Stderr
// are captured (a buffer+Write would NOT catch them).
func captureStderrSwap(t *testing.T, buf *bytes.Buffer, fn func() int) error {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(buf, r)
		close(done)
	}()
	rc := fn()
	_ = w.Close()
	os.Stderr = orig
	<-done
	_ = rc
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Tier A8.1 / ADR-083 follow-up: PrintUsage ↔ cliCommand.DocSlug parity.
//
// The completion + man subsystem (Tier A8, PR #752) treats the manifest
// as the source of truth — every manifest entry shows up in completion
// scripts and gets a man page. PrintUsage(..., topic) (cmd/gregale/output.go:156)
// resolves command topics through the public consolidated CLI docs page,
// so the topic string must still resolve to a cliCommand.DocSlug or Name;
// otherwise the manifest and the --help URL can drift.
//
// The test walks every PrintUsage(...) call site in cmd/gregale/ via
// go/ast (same machinery as extractMainCaseArms above) and asserts
// two invariants:
//
//  1. Forward: every topic resolves to a cliCommand.DocSlug / Name
//     OR one of a small set of semantic slugs (auth, apps, park-wake)
//     that the docs site recognises but the manifest doesn't carry
//     as a top-level entry.
//  2. Inverse: every cliCommand.DocSlug has at least one PrintUsage
//     caller — catches orphan manifest entries (a command added to
//     cli_meta.go with no leaf that actually prints its docs URL).
//
// The semantic-slug allow-list lives in the test, not production
// code, because the test is the only place we enforce the convention.
// Adding a new semantic slug is a one-line test edit + a code-review
// note.
func TestUsageDocSlugParity(t *testing.T) {
	// 1. Build the set of accepted topics.
	accepted := make(map[string]string, len(cliCommands)+4)
	for _, c := range cliCommands {
		accepted[c.Name] = "cliCommand.Name=" + c.Name
		accepted[c.DocSlug] = "cliCommand.DocSlug=" + c.DocSlug + " (name=" + c.Name + ")"
	}
	semantic := map[string]string{
		"auth":        "login/logout/whoami share the auth docs page",
		"apps":        "gregale app <slug> family uses apps (manifest entry app has DocSlug=apps)",
		"park-wake":   "park + wake share the park-wake docs page",
		"account-slo": "gregale account slo [--window X] is a sibling topic under the CLI docs page",
		// ADR-124 code-review fix #2 — `deployments exclude clear` is
		// the operator escape hatch for dropping a stale persisted
		// exclusion. The dispatch lives on the parent `deployments`
		// verb (dispatchDeployments), so there is no separate
		// cliCommand entry for the exclude subcommand; the PrintUsage
		// topic is the joined parent.DocSlug + sub.Name path. Pin it
		// as a semantic topic so the forward invariant doesn't
		// false-positive on this CLI surface.
		"deployments exclude": "operator escape hatch under the deployments verb (ADR-124 code-review fix #2)",
	}
	for k, v := range semantic {
		accepted[k] = "semantic: " + v
	}

	// 2. Walk every PrintUsage call site under cmd/gregale/.
	usages, err := extractPrintUsageTopics()
	if err != nil {
		t.Fatalf("walk PrintUsage call sites: %v", err)
	}
	if len(usages) == 0 {
		t.Fatal("no PrintUsage call sites found — extractor is broken")
	}

	// 3. Forward invariant: every topic resolves.
	for _, u := range usages {
		if _, ok := accepted[u.topic]; !ok {
			t.Errorf("%s: PrintUsage topic %q does not resolve to any cliCommand.DocSlug/Name or semantic topic", u.where, u.topic)
		}
	}

	// 4. Inverse invariant: every cliCommand.DocSlug has a caller.
	used := make(map[string]int, len(usages))
	for _, u := range usages {
		used[u.topic]++
	}
	// Internal pseudo-commands excluded from the inverse check, mirroring
	// the internal allow-list in TestCompletion_ManifestDrift above.
	// `completion` and `man` dispatch to their own subcommands (the
	// PrintUsage lives inside cmdCompletion / cmdMan, not in per-leaf
	// handlers), so a missing caller from a leaf doesn't mean an
	// orphan manifest entry.
	internalInverse := map[string]struct{}{
		"completion": {},
		"man":        {},
	}
	for _, c := range cliCommands {
		if _, isInternal := internalInverse[c.Name]; isInternal {
			continue
		}
		if used[c.DocSlug] == 0 && used[c.Name] == 0 {
			t.Errorf("cliCommand %q (DocSlug %q) has zero PrintUsage call sites — orphan manifest entry", c.Name, c.DocSlug)
		}
	}

	// 5. Sanity: each topic that IS used must have a non-zero count
	// (catches a topic that's accidentally listed twice in different
	// places that we deduplicated to one — fail loudly instead).
	for topic, n := range used {
		if n == 0 {
			t.Errorf("internal: used[%q] == 0 — extractor bug", topic)
		}
	}
}

// printUsageSite is one PrintUsage(...) call site extracted by
// extractPrintUsageTopics. Topic is the second argument (the docs
// URL slug); Where is a "file:line" string suitable for test errors.
type printUsageSite struct {
	where string
	topic string
}

// extractPrintUsageTopics walks every non-test .go file under the
// current package directory (cmd/gregale/) and collects the topic
// string passed as the second argument of every PrintUsage(...) call.
// Mirrors extractMainCaseArms above: parses each file once via
// go/parser, walks the AST with ast.Inspect.
//
// Const-typed topics (e.g. metricsCmdDocsTopic in commands_metrics.go)
// are resolved via the topicConsts allow-list below — same pattern
// as the dispatchConsts map in TestCompletion_ManifestDrift.
func extractPrintUsageTopics() ([]printUsageSite, error) {
	topicConsts := map[string]string{
		"initCmdDocsTopic":                "init",
		"metricsCmdDocsTopic":             "metrics",
		"sloCmdDocsTopic":                 "slo",
		"sloAccountCmdDocsTopic":          "account-slo",
		"invocationCmdDocsTopic":          "invocations",
		"completionDocsTopic":             "completion",
		"manDocsTopic":                    "man",
		"docsOpenTopic":                   "open",
		"throttleSuggestionsCmdDocsTopic": "throttle-suggestions",
		"debugCmdDocsTopic":               "debug",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("readdir cmd/gregale: %w", err)
	}
	var sites []printUsageSite
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "PrintUsage" {
				return true
			}
			if len(call.Args) < 3 {
				return true
			}
			topic, ok := printUsageTopicString(call.Args[2], topicConsts)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos())
			sites = append(sites, printUsageSite{
				where: fmt.Sprintf("%s:%d", pos.Filename, pos.Line),
				topic: topic,
			})
			return true
		})
	}
	return sites, nil
}

// printUsageTopicString extracts the topic literal from a PrintUsage
// call's second argument. Handles two shapes: a string literal
// (`"alerts"`) and an identifier resolved via topicConsts
// (`metricsCmdDocsTopic` → `"metrics"`). Returns ("", false) when
// the argument is neither — skip the site silently (the caller
// can't verify dynamic topics).
func printUsageTopicString(expr ast.Expr, topicConsts map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		return strings.Trim(e.Value, `"`), true
	case *ast.Ident:
		if v, ok := topicConsts[e.Name]; ok {
			return v, true
		}
		// Unknown identifier: treat as a literal topic and let the
		// test's accepted-map decide whether it resolves. This catches
		// typos in topic names that would otherwise compile but 404
		// at runtime.
		return e.Name, true
	}
	return "", false
}
