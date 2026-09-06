package main

// Test file for issue #737 / ADR-083 — the customer-visible
// "Detected: …" CLI print line is the load-bearing acceptance gate
// for the zero-config function vs app deploy auto-detection. The
// test drives resolveDeployShape directly (no apid, no auth) and
// captures osStdout via the same swap used in commands_metrics_test.go
// / commands_usage_summary_test.go, then asserts the print line.
//
// The test is whitebox (package main) on purpose: the print +
// detect + infer triad lives in pack.go next to detectShape /
// inferFunctionRuntime, so the unit-test seam is local. We don't
// shell out to `go run ./cmd/gregale` because that would require an
// authenticated apid at the other end — out of scope for the print
// line, which is the only new customer-visible behaviour.

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestResolveDeployShape_Function is the headline case: a cwd
// containing only handler.js must produce the
// "Detected: function, runtime=node22, handler=handler.handler"
// line on stdout. A regression that drops the print line, picks
// the wrong shape, picks the wrong runtime, or moves the print
// line after the multipart POST fails this gate.
func TestResolveDeployShape_Function(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "handler.js", "exports.handler = async () => ({statusCode:200, body:'ok'});")

	var buf bytes.Buffer
	oldOut := osStdout
	osStdout = &buf
	defer func() { osStdout = oldOut }()

	sh, rt, hnd, err := resolveDeployShape(dir, false, false, false)
	if err != nil {
		t.Fatalf("resolveDeployShape: %v", err)
	}
	if sh != shapeFunction {
		t.Errorf("shape = %d, want %d (shapeFunction)", sh, shapeFunction)
	}
	if rt != "node22" {
		t.Errorf("runtime = %q, want %q", rt, "node22")
	}
	if hnd != "handler.handler" {
		t.Errorf("handler = %q, want %q", hnd, "handler.handler")
	}
	got := buf.String()
	wantLine := "Detected: function, runtime=node22, handler=handler.handler, class=function"
	if !strings.Contains(got, wantLine) {
		t.Errorf("stdout missing %q; got %q", wantLine, got)
	}
}

func TestResolveDeployShape_FunctionBannerUsesExplicitRuntime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "handler.go", "package main")

	var buf bytes.Buffer
	oldOut := osStdout
	osStdout = &buf
	defer func() { osStdout = oldOut }()

	sh, rt, hnd, err := resolveDeployShape(dir, false, false, false, "go124-alpine", "custom.handler")
	if err != nil {
		t.Fatalf("resolveDeployShape: %v", err)
	}
	if sh != shapeFunction || rt != runtimeGo124 || hnd != defaultTemplateHandler {
		t.Fatalf("inferred shape = (%v, %q, %q), want function defaults", sh, rt, hnd)
	}
	want := "Detected: function, runtime=go124-alpine, handler=custom.handler, class=function"
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("stdout missing %q; got %q", want, buf.String())
	}
}

// TestResolveDeployShape_JSONSuppressesPrint pins the §3.2 --json
// contract: when a customer runs `gregale deploy --json` on a
// handler.js-only cwd, stdout must remain a parseable JSON stream.
// resolveDeployShape must NOT write "Detected: function, ..." to
// stdout in that mode — that line would land before the deploy
// response's JSON and break `gregale deploy --json | jq`. The
// shape is still resolved (the wire is unchanged), only the
// customer-visible banner is suppressed.
func TestResolveDeployShape_JSONSuppressesPrint(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		sh    shape
	}{
		{"function", []string{"handler.js"}, shapeFunction},
		{"app", []string{"package.json"}, shapeApp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				writeFile(t, dir, f, "")
			}
			var buf bytes.Buffer
			oldOut := osStdout
			osStdout = &buf
			defer func() { osStdout = oldOut }()

			// jsonOutput=true is the load-bearing flag for this test.
			sh, _, _, err := resolveDeployShape(dir, false, false, true)
			if err != nil {
				t.Fatalf("resolveDeployShape: %v", err)
			}
			if sh != tc.sh {
				t.Errorf("shape = %d, want %d", sh, tc.sh)
			}
			if strings.Contains(buf.String(), "Detected:") {
				t.Errorf("stdout must not contain Detected: line under jsonOutput=true; got %q", buf.String())
			}
		})
	}
}

// TestResolveDeployShape_App pins the app-shape print line: a cwd
// containing package.json must produce the
// "Detected: app, framework=node" line. The framework name comes
// from detectFramework, kept here only to pin the format string
// shape. Issue #740 / DEPLOY-PROV-5 / ADR-087 extends the line with
// a version= token when a version file is present; this test pins
// the no-version fallback (the empty case stays format-stable for
// customers without a .nvmrc / package.json::engines).
func TestResolveDeployShape_App(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")

	var buf bytes.Buffer
	oldOut := osStdout
	osStdout = &buf
	defer func() { osStdout = oldOut }()

	sh, _, _, err := resolveDeployShape(dir, false, false, false)
	if err != nil {
		t.Fatalf("resolveDeployShape: %v", err)
	}
	if sh != shapeApp {
		t.Errorf("shape = %d, want %d (shapeApp)", sh, shapeApp)
	}
	got := buf.String()
	wantLine := "Detected: app, framework=node, class=app"
	if !strings.Contains(got, wantLine) {
		t.Errorf("stdout missing %q; got %q", wantLine, got)
	}
	if strings.Contains(got, "version=") {
		t.Errorf("stdout unexpectedly contains version= token; got %q", got)
	}
}

// TestResolveDeployShape_AppWithVersion pins the
// "Detected: app, framework=node, version=22.11.0" banner extension
// (issue #740 / DEPLOY-PROV-5 / ADR-087). The version comes from the
// CLI-side mirror of pkg/builderd/detectversion.go (which reads
// .nvmrc); the server independently re-derives it for
// build_provenance.framework_version.
func TestResolveDeployShape_AppWithVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, ".nvmrc", "22.11.0")

	var buf bytes.Buffer
	oldOut := osStdout
	osStdout = &buf
	defer func() { osStdout = oldOut }()

	sh, _, _, err := resolveDeployShape(dir, false, false, false)
	if err != nil {
		t.Fatalf("resolveDeployShape: %v", err)
	}
	if sh != shapeApp {
		t.Errorf("shape = %d, want shapeApp", sh)
	}
	got := buf.String()
	wantLine := "Detected: app, framework=node, version=22.11.0, class=app"
	if !strings.Contains(got, wantLine) {
		t.Errorf("stdout missing %q; got %q", wantLine, got)
	}
}

// TestResolveDeployShape_FunctionErrorWithMarkerSuggestion pins the
// function error path's runtime suggestion (issue #740 / ADR-087).
// When a user passes --function on a directory that has a Node
// project (package.json + .nvmrc) but no handler.{js,ts,py,go}, the
// error message must surface the marker-derived runtime as an
// actionable hint.
func TestResolveDeployShape_FunctionErrorWithMarkerSuggestion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, ".nvmrc", "22.11.0")

	sh, _, _, err := resolveDeployShape(dir, true, false, false)
	if err == nil {
		t.Fatalf("expected error for --function with no handler file")
	}
	if sh != shapeUnknown {
		t.Errorf("shape = %d, want shapeUnknown", sh)
	}
	want := "Detected node project"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error must include the marker suggestion %q; got %q", want, err.Error())
	}
	if !strings.Contains(err.Error(), "--runtime node22") {
		t.Errorf("error must suggest --runtime node22; got %q", err.Error())
	}
}

// TestResolveDeployShape_FunctionErrorFallsBackWhenNoVersion pins
// the negative case: when the function error path fires but no
// version file is present, the suggestion is omitted and the error
// remains the bare "no handler file" message (no degraded hint
// surfacing a wrong runtime).
func TestResolveDeployShape_FunctionErrorFallsBackWhenNoVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")

	_, _, _, err := resolveDeployShape(dir, true, false, false)
	if err == nil {
		t.Fatalf("expected error for --function with no handler file")
	}
	if strings.Contains(err.Error(), "Detected ") {
		t.Errorf("error must not include a marker suggestion when no version file is present; got %q", err.Error())
	}
}

// TestResolveDeployShape_Unknown pins the no-source error path: an
// empty / README-only cwd must surface an actionable error that
// mentions BOTH app and function paths, NOT silently pick app.
func TestResolveDeployShape_Unknown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hi")

	var buf bytes.Buffer
	oldOut := osStdout
	osStdout = &buf
	defer func() { osStdout = oldOut }()

	sh, _, _, err := resolveDeployShape(dir, false, false, false)
	if err == nil {
		t.Fatalf("resolveDeployShape on empty dir should error; got shape=%d", sh)
	}
	if sh != shapeUnknown {
		t.Errorf("shape = %d, want %d (shapeUnknown)", sh, shapeUnknown)
	}
	if !strings.Contains(err.Error(), "package.json") {
		t.Errorf("error should mention the app-marker path; got %v", err)
	}
	if !strings.Contains(err.Error(), "handler.{js,ts,py,go}") {
		t.Errorf("error should mention the function path; got %v", err)
	}
	// The unknown path emits NO "Detected:" line — the print is
	// only on a successful shape resolve. Pins that the gate is
	// well-defined.
	if strings.Contains(buf.String(), "Detected:") {
		t.Errorf("stdout should not contain Detected line on shapeUnknown; got %q", buf.String())
	}
}

// TestResolveDeployShape_ExplicitFunctionForcesFunction pins the
// --function short-circuit: even a cwd that has package.json (an
// app marker) must resolve to shapeFunction when explicitFunction
// is true. The customer paid for the explicit flag — auto-detection
// must NOT overrule them.
func TestResolveDeployShape_ExplicitFunctionForcesFunction(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, "handler.js", "")

	var buf bytes.Buffer
	oldOut := osStdout
	osStdout = &buf
	defer func() { osStdout = oldOut }()

	sh, rt, hnd, err := resolveDeployShape(dir, true, false, false)
	if err != nil {
		t.Fatalf("resolveDeployShape(--function): %v", err)
	}
	if sh != shapeFunction {
		t.Errorf("shape = %d, want %d (shapeFunction via --function)", sh, shapeFunction)
	}
	if rt != "node22" {
		t.Errorf("runtime = %q, want %q", rt, "node22")
	}
	if hnd != "handler.handler" {
		t.Errorf("handler = %q, want %q", hnd, "handler.handler")
	}
	if !strings.Contains(buf.String(), "Detected: function, runtime=node22, handler=handler.handler") {
		t.Errorf("stdout missing function print line; got %q", buf.String())
	}
}

// TestResolveDeployShape_ExplicitAppForcesApp pins the --app
// short-circuit: even a cwd that has only handler.js (no app
// markers) must resolve to shapeApp when explicitApp is true.
// Symmetric to the function test above.
func TestResolveDeployShape_ExplicitAppForcesApp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "handler.js", "")

	var buf bytes.Buffer
	oldOut := osStdout
	osStdout = &buf
	defer func() { osStdout = oldOut }()

	sh, _, _, err := resolveDeployShape(dir, false, true, false)
	if err != nil {
		t.Fatalf("resolveDeployShape(--app): %v", err)
	}
	if sh != shapeApp {
		t.Errorf("shape = %d, want %d (shapeApp via --app)", sh, shapeApp)
	}
	if !strings.Contains(buf.String(), "Detected: app") {
		t.Errorf("stdout missing app print line; got %q", buf.String())
	}
}

// TestResolveDeployShape_FunctionRuntimes pins each extension in
// the runtime map (.js / .ts → node22, .py → python312,
// .go → go124). The handler wire value stays the literal
// "handler.handler" across all four — matching the function-*
// template convention (cmd/gregale/templates/function-node/handler.js).
func TestResolveDeployShape_FunctionRuntimes(t *testing.T) {
	cases := []struct {
		name    string
		handler string
		wantRt  string
	}{
		{"node_js", "handler.js", "node22"},
		{"node_ts", "handler.ts", "node22"},
		{"python", "handler.py", "python312"},
		{"go", "handler.go", "go124"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, tc.handler, "")

			var buf bytes.Buffer
			oldOut := osStdout
			osStdout = &buf
			defer func() { osStdout = oldOut }()

			sh, rt, hnd, err := resolveDeployShape(dir, false, false, false)
			if err != nil {
				t.Fatalf("resolveDeployShape: %v", err)
			}
			if sh != shapeFunction {
				t.Errorf("shape = %d, want %d (shapeFunction)", sh, shapeFunction)
			}
			if rt != tc.wantRt {
				t.Errorf("runtime = %q, want %q", rt, tc.wantRt)
			}
			if hnd != "handler.handler" {
				t.Errorf("handler = %q, want %q", hnd, "handler.handler")
			}
			wantSub := "Detected: function, runtime=" + tc.wantRt + ", handler=handler.handler, class=function"
			if !strings.Contains(buf.String(), wantSub) {
				t.Errorf("stdout missing %q; got %q", wantSub, buf.String())
			}
		})
	}
}

// TestBuildCreateRequest pins the CreateAppRequest wire-shape contract
// for issue #737 / ADR-083. The headline regression it catches: an
// auto-detected function cwd (handler.js-only) where buildCreateRequest
// sets Type="function" but leaves Runtime="" — apid's buildApp
// validator (cmd/apid/handlers.go:98) would then 400 the request with
// "Invalid runtime, functions require runtime node22, ...". The
// customer would see "Detected: function, runtime=node22, ..." followed
// by "Could not create app: Invalid runtime ..." — silently broken.
func TestBuildCreateRequest(t *testing.T) {
	tru := true
	falsy := false
	cases := []struct {
		name      string
		sh        shape
		runtime   string
		authnPtr  *bool
		wantType  string
		wantRt    string
		wantAuthn *bool
	}{
		// Function path: must propagate BOTH Type and Runtime.
		// The auto-detect path is the load-bearing case — the
		// header regression the test pins.
		{
			name:     "function_with_runtime",
			sh:       shapeFunction,
			runtime:  "node22",
			wantType: "function",
			wantRt:   "node22",
		},
		{
			name:     "function_python_runtime",
			sh:       shapeFunction,
			runtime:  "python312",
			wantType: "function",
			wantRt:   "python312",
		},
		// Function with empty runtime — Type is still set, but
		// Runtime stays empty so apid's validator surfaces a
		// clear "Invalid runtime" 400 (better than a silent
		// server-side type guess).
		{
			name:     "function_empty_runtime",
			sh:       shapeFunction,
			runtime:  "",
			wantType: "function",
			wantRt:   "",
		},
		// App path: Type stays empty (apid treats as "app"); no
		// function fields leak onto the wire.
		{
			name:     "app_no_function_fields",
			sh:       shapeApp,
			runtime:  "",
			wantType: "",
			wantRt:   "",
		},
		// App path with runtime incidentally set — must NOT
		// leak onto the wire. The CLI clears *runtime on the
		// --app path (commands2.go:564-568), so this case
		// catches a regression where the clear stops working.
		{
			name:     "app_with_runtime_still_app",
			sh:       shapeApp,
			runtime:  "node22",
			wantType: "",
			wantRt:   "",
		},
		// RequireAuthn propagation (issue #560) must survive the
		// helper extraction — explicit true / false / unset.
		{
			name:      "function_with_authn_true",
			sh:        shapeFunction,
			runtime:   "node22",
			authnPtr:  &tru,
			wantType:  "function",
			wantRt:    "node22",
			wantAuthn: &tru,
		},
		{
			name:      "app_with_authn_false",
			sh:        shapeApp,
			runtime:   "",
			authnPtr:  &falsy,
			wantType:  "",
			wantRt:    "",
			wantAuthn: &falsy,
		},
		{
			name:      "function_with_authn_unset",
			sh:        shapeFunction,
			runtime:   "node22",
			authnPtr:  nil,
			wantType:  "function",
			wantRt:    "node22",
			wantAuthn: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildCreateRequest("slug", tc.sh, tc.runtime, tc.authnPtr, nil)
			if got.Slug != "slug" {
				t.Errorf("Slug = %q, want %q", got.Slug, "slug")
			}
			if got.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tc.wantType)
			}
			if got.Runtime != tc.wantRt {
				t.Errorf("Runtime = %q, want %q", got.Runtime, tc.wantRt)
			}
			if tc.wantAuthn == nil {
				if got.RequireAuthn != nil {
					t.Errorf("RequireAuthn = %v, want nil (unset)", *got.RequireAuthn)
				}
			} else {
				if got.RequireAuthn == nil {
					t.Errorf("RequireAuthn = nil, want %v", *tc.wantAuthn)
				} else if *got.RequireAuthn != *tc.wantAuthn {
					t.Errorf("RequireAuthn = %v, want %v", *got.RequireAuthn, *tc.wantAuthn)
				}
			}
		})
	}
}

func TestBuildCreateRequestResourceProfile(t *testing.T) {
	got := buildCreateRequest("slug", shapeApp, "", nil, nil, "small")
	if got.ResourceProfile != "small" {
		t.Fatalf("ResourceProfile = %q, want small", got.ResourceProfile)
	}
}

// TestResolveDeployShape_NestedMarkerHint pins the customer-visible
// behaviour of issue #744 / ADR-086: a cwd whose only deployable source
// is a marker buried at depth 1 (the common monorepo layout
// apps/web/package.json) must surface a `Hint: ... gregale scan --path .`
// line so the customer knows what to do next.
//
// Text mode: the hint is appended to the PrintFail line on stderr.
// --json mode: the hint goes to stderr ONLY (stdout must remain a
// parseable JSON envelope).
func TestResolveDeployShape_NestedMarkerHint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "apps/web/package.json", "{}")
	writeFile(t, dir, "README.md", "monorepo root")

	_, _, _, err := resolveDeployShape(dir, false, false, false)
	if err == nil {
		t.Fatalf("resolveDeployShape on nested-marker cwd should error; got nil")
	}

	// The error must wrap a *NestedMarkerHintError so programmatic
	// consumers (dashboards, CI scripts) can errors.As it.
	var hintErr *NestedMarkerHintError
	if !errors.As(err, &hintErr) {
		t.Fatalf("error chain missing *NestedMarkerHintError; got %T: %v", err, err)
	}
	if !strings.Contains(hintErr.Hint, "gregale scan --path .") {
		t.Errorf("hint missing 'gregale scan --path .'; got %q", hintErr.Hint)
	}

	// Drive printErr (text mode) and assert the hint lands on stderr.
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &stdout, &stderr
	defer func() { osStdout, osStderr = oldOut, oldErr }()

	prevJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = prevJSON }()

	code := printErr("Could not resolve deploy shape", err)
	if code == 0 {
		t.Errorf("printErr returned 0; expected non-zero exit code on shape error")
	}
	if !strings.Contains(stderr.String(), "gregale scan --path .") {
		t.Errorf("stderr missing hint; got %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "gregale scan --path .") {
		t.Errorf("stdout must not contain hint in text mode; got %q", stdout.String())
	}

	// Pin the no-duplicate behaviour (issue #744 review finding): the
	// hint branch must drop the title because the wrapped err.Error()
	// already encodes the cwd ("no deployable source found in <dir>: ...").
	// A regression that rendered both title and err.Error() would print
	// the cwd twice on stderr — visually noisy and confusing.
	occurrences := strings.Count(stderr.String(), "no deployable source found in")
	if occurrences != 1 {
		t.Errorf("stderr should contain 'no deployable source found in' exactly once (no title+err duplication); got %d occurrences in %q", occurrences, stderr.String())
	}
}

// TestResolveDeployShape_NestedMarkerHint_JSON pins the §3.3 --json contract
// from ADR-086: when a customer runs `gregale deploy --json` on a cwd with
// nested markers, printErr must (a) NOT include the hint in the JSON
// envelope (the envelope is the wire error), and (b) emit the hint as a
// separate human-readable line so a customer running
// `gregale deploy --json 2>&1 | less` still sees the next-step guidance.
//
// Implementation note: the JSON envelope path uses writeJSONProblem, which
// writes to os.Stderr directly (not via the osStderr swap). This is
// pre-existing behaviour — see json_flag.go:writeJSONProblem — and a
// property this test deliberately does NOT regress. We pin only the
// shape-resolution error chain (typed NestedMarkerHintError) + the
// stderr hint line via the swap, which is the load-bearing contract.
func TestResolveDeployShape_NestedMarkerHint_JSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "apps/web/package.json", "{}")

	sh, _, _, err := resolveDeployShape(dir, false, false, true)
	if err == nil {
		t.Fatalf("resolveDeployShape(--json) on nested-marker cwd should error; got nil")
	}
	if sh != shapeUnknown {
		t.Errorf("shape = %d, want %d (shapeUnknown)", sh, shapeUnknown)
	}

	// The error chain must carry the hint for downstream consumers
	// (dashboards, CI scripts) that errors.As it.
	var hintErr *NestedMarkerHintError
	if !errors.As(err, &hintErr) {
		t.Fatalf("error chain missing *NestedMarkerHintError; got %T: %v", err, err)
	}
	if !strings.Contains(hintErr.Hint, "gregale scan --path .") {
		t.Errorf("hint missing 'gregale scan --path .'; got %q", hintErr.Hint)
	}

	// Drive printErr under --json and confirm the hint lands on the
	// swappable stderr (the PrintWarn side of printErr's typed-error
	// branch). The JSON envelope itself is exercised by
	// json_flag_test.go; we only assert the hint split here.
	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	defer func() { osStderr = oldErr }()

	prevJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prevJSON }()

	code := printErr("Could not resolve deploy shape", err)
	if code == 0 {
		t.Errorf("printErr returned 0; expected non-zero exit code on shape error")
	}
	if !strings.Contains(stderr.String(), "gregale scan --path .") {
		t.Errorf("stderr missing hint under --json; got %q", stderr.String())
	}
}

// TestResolveDeployShape_NoNestedHintOnEmptyDir pins the negative case:
// a cwd with no markers at any depth must produce the bare error and
// MUST NOT carry the hint (no false-positive on a totally empty repo).
func TestResolveDeployShape_NoNestedHintOnEmptyDir(t *testing.T) {
	dir := t.TempDir()

	_, _, _, err := resolveDeployShape(dir, false, false, false)
	if err == nil {
		t.Fatalf("resolveDeployShape on empty dir should error; got nil")
	}

	var hintErr *NestedMarkerHintError
	if errors.As(err, &hintErr) {
		t.Errorf("empty dir must NOT carry NestedMarkerHintError; got hint %q", hintErr.Hint)
	}
}

// TestResolveDeployShape_NoNestedHintOnExcludedDirs pins the false-positive
// guard: a cwd whose only nested markers live under excluded dirs
// (node_modules / .git / vendor / __pycache__) must NOT carry the hint.
func TestResolveDeployShape_NoNestedHintOnExcludedDirs(t *testing.T) {
	dir := t.TempDir()
	// node_modules/<pkg>/package.json — a real-world monorepo WILL have
	// these, and we don't want every Node project to fire the hint.
	writeFile(t, dir, "node_modules/some-pkg/package.json", "{}")
	writeFile(t, dir, ".git/HEAD", "ref: refs/heads/main")

	_, _, _, err := resolveDeployShape(dir, false, false, false)
	if err == nil {
		t.Fatalf("resolveDeployShape should error (no top-level markers); got nil")
	}

	var hintErr *NestedMarkerHintError
	if errors.As(err, &hintErr) {
		t.Errorf("excluded-dir markers must NOT fire NestedMarkerHintError; got hint %q", hintErr.Hint)
	}
}
