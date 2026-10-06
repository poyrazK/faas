package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func TestMergeAppFlag(t *testing.T) {
	for _, tc := range []struct {
		name       string
		positional []string
		app        string
		max        int
		want       []string
		wantErr    error
	}{
		{name: "no flag keeps positionals", positional: []string{"api"}, max: 1, want: []string{"api"}},
		{name: "flag fills omitted slug", app: "api", max: 1, want: []string{"api"}},
		{name: "same app twice is fine", positional: []string{"api"}, app: "api", max: 1, want: []string{"api"}},
		{name: "different apps conflict", positional: []string{"api"}, app: "web", max: 1, wantErr: errAppFlagConflict},
		{name: "flag fills slug before trailing id", positional: []string{"wake-1"}, app: "api", max: 2, want: []string{"api", "wake-1"}},
		{name: "full positionals must agree", positional: []string{"web", "wake-1"}, app: "api", max: 2, wantErr: errAppFlagConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mergeAppFlag(tc.positional, tc.app, tc.max)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && !slices.Equal(got, tc.want)) {
				t.Fatalf("mergeAppFlag(%q, %q, %d) = (%q, %v), want (%q, %v)", tc.positional, tc.app, tc.max, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

// TestSlugFirstLeavesAcceptAppFlag reproduces production-us hunt #4:
// `gregale link` says to pass --app for app-scoped commands, but these
// leaves rejected it with "flag provided but not defined: -app". A
// conflicting slug and --app must now reach the leaf's own validation,
// which happens before any request.
func TestSlugFirstLeavesAcceptAppFlag(t *testing.T) {
	for _, args := range [][]string{
		{"ps", "slug-a"},
		{"logs", "slug-a"},
		{"metrics", "slug-a"},
		{"analytics", "slug-a"},
		{"slo", "slug-a"},
		{"inspect", "slug-a"},
		{"throttle-suggestions", "slug-a"},
		{"wake-timeline", "slug-a", "wake-1"},
		{"dlq", "list", "slug-a"},
		{"events", "subscriptions", "slug-a"},
		{"debug", "requests", "list", "slug-a"},
		{"bindings", "slug-a"},
	} {
		t.Run(strings.Join(args[:len(args)-1], " "), func(t *testing.T) {
			stderr, restore := captureStderr(t)
			code := run(append(append([]string(nil), args...), "--app", "slug-b"))
			restore()
			out := stderr.String()
			if code == 0 {
				t.Fatalf("conflicting slug and --app exited 0; stderr:\n%s", out)
			}
			if strings.Contains(out, "not defined") || strings.Contains(out, "unknown flag --app") {
				t.Fatalf("leaf still rejects --app:\n%s", out)
			}
		})
	}
}

// TestHumanFlagErrorsUsePublicSpelling reproduces production-us hunt #4:
// 30 leaves printed the flag package's raw output for a mistyped flag,
// with single-dash names, every default, and internal FlagSet names
// such as "Usage of usage-list:".
func TestHumanFlagErrorsUsePublicSpelling(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{args: []string{"metrics", "--bogus"}, want: []string{"unknown flag --bogus", "run 'gregale metrics --help' for usage"}},
		{args: []string{"usage", "--bogus"}, want: []string{"unknown flag --bogus", "run 'gregale usage --help' for usage"}},
		{args: []string{"invocations", "list", "--bogus"}, want: []string{"run 'gregale invocations list --help' for usage"}},
		{args: []string{"metrics", "x", "--range"}, want: []string{"flag --range needs a value"}},
		// A leaf's own usage line survives; only the defaults dump is dropped.
		{args: []string{"dlq", "list", "x", "--limit", "abc"}, want: []string{`invalid value "abc" for flag --limit`, "usage: gregale dlq list <app>"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stderr, restore := captureStderr(t)
			code := run(tc.args)
			restore()
			out := stderr.String()
			if code == 0 {
				t.Fatalf("exit 0 for invalid flags; stderr:\n%s", out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("stderr missing %q:\n%s", want, out)
				}
			}
			for _, raw := range []string{"Usage of ", "flag provided but not defined", "usage-list", "\n  -"} {
				if strings.Contains(out, raw) {
					t.Errorf("stderr still contains raw flag output %q:\n%s", raw, out)
				}
			}
		})
	}
}

func TestNormalizeFlagDiagnostic(t *testing.T) {
	for in, want := range map[string]string{
		"flag provided but not defined: -app":                 "unknown flag --app",
		"flag needs an argument: -range":                      "flag --range needs a value",
		`invalid value "abc" for flag -limit: parse error`:    `invalid value "abc" for flag --limit: parse error`,
		`invalid boolean value "maybe" for -all: parse error`: `invalid boolean value "maybe" for --all: parse error`,
		"bad flag syntax: ---x":                               "bad flag syntax: ---x",
	} {
		if got := normalizeFlagDiagnostic(in); got != want {
			t.Errorf("normalizeFlagDiagnostic(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCustomerPlatformDoctorSkipsOwnerTools reproduces production-us hunt
// #4: `gregale init --template customer-platform` followed by `gregale
// doctor` flagged FAAS_API and FAAS_TOKEN, which only the owner-machine
// tools/ scripts read, and advised `gregale secrets set --app <slug>
// FAAS_TOKEN=<value>`: an account-owner credential in the app runtime.
func TestCustomerPlatformDoctorSkipsOwnerTools(t *testing.T) {
	dir := t.TempDir()
	if err := templates.Materialize("customer-platform", dir); err != nil {
		t.Fatal(err)
	}
	refs := scanEnvRefs(dir, envVarRefRegex)
	for _, owner := range []string{"FAAS_TOKEN", "FAAS_API"} {
		if slices.Contains(refs, owner) {
			t.Errorf("doctor env scan reports owner-tool variable %s: %v", owner, refs)
		}
	}
	if !slices.Contains(refs, "DATABASE_URL") {
		t.Errorf("doctor env scan lost the app's own DATABASE_URL: %v", refs)
	}
}

// TestVersionJSON reproduces production-us hunt #4: `gregale --json
// version` printed the human "gregale dev" line, breaking the documented
// "every command accepts --json" contract scripts rely on.
func TestVersionJSON(t *testing.T) {
	stdout, restore := captureStdout(t)
	code := run([]string{"--json", "version"})
	restore()
	out := stdout.String()
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["version"] == "" {
		t.Fatalf("--json version = %q (%v), want a JSON object with version", out, err)
	}
}

// TestDeployInvalidManifestFailsBeforeDetection reproduces production-us
// hunt #4: with a gregale.yaml that failed to parse, deploy fell back to
// file heuristics and printed "Detected: app, framework=node" for a
// function template before reporting the manifest error.
func TestDeployInvalidManifestFailsBeforeDetection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, "handler.js", "exports.handler = async () => ({})\n")
	writeFile(t, dir, "gregale.yaml", "function:\n  runtime: node22\n  handler: handler.handler\ntrigers: []\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"title":"not found","status":404}`, http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	stdout, restoreOut := captureStdout(t)
	stderr, restoreErr := captureStderr(t)
	code := run([]string{"deploy", "--dry-run", "--name", "manifest-typo", "--path", dir})
	restoreErr()
	restoreOut()
	if code == 0 {
		t.Fatal("deploy with an invalid manifest exited 0")
	}
	if !strings.Contains(stderr.String(), `unknown key "trigers"`) {
		t.Errorf("stderr missing the manifest error:\n%s\nstdout:\n%s", stderr.String(), stdout.String())
	}
	if strings.Contains(stdout.String(), "Detected:") {
		t.Errorf("deploy printed a heuristic shape before the manifest error:\n%s", stdout.String())
	}
}

// production-us hunt #4 (H4-36): the single-request debug leaves accept the
// app as --app as well as the leading positional.
func TestDebugRequestRefArgsAcceptAppFlag(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
		ok   bool
	}{
		{args: []string{"api", "req-1"}, want: []string{"api", "req-1"}, ok: true},
		{args: []string{"req-1", "--app", "api"}, want: []string{"api", "req-1"}, ok: true},
		{args: []string{"--app=api", "req-1"}, want: []string{"api", "req-1"}, ok: true},
		{args: []string{"api", "req-1", "--app", "api"}, want: []string{"api", "req-1"}, ok: true},
		{args: []string{"web", "req-1", "--app", "api"}, ok: false},
		{args: []string{"req-1"}, ok: false},
		{args: []string{"req-1", "--app"}, ok: false},
	} {
		got, ok := debugRequestRefArgs(tc.args)
		if ok != tc.ok || (tc.ok && !slices.Equal(got, tc.want)) {
			t.Errorf("debugRequestRefArgs(%q) = (%q, %v), want (%q, %v)", tc.args, got, ok, tc.want, tc.ok)
		}
	}
}

// production-us hunt #4 (H4-15): `webhooks list` help showed no argument and
// the command then demanded --app. It now takes the slug positionally or as
// --app, and two different apps are refused.
func TestWebhooksListAcceptsSlugEitherWay(t *testing.T) {
	resetJSONOut(t)
	for _, args := range [][]string{{"demo"}, {"--app", "demo"}, {"demo", "--app", "demo"}} {
		f := authedFakeAPI(t, `[]`, http.StatusOK)
		if code := cmdWebhooksList(args); code != 0 {
			t.Fatalf("webhooks list %q exit = %d, want 0", args, code)
		}
		if f.sawPath != "/v1/apps/demo/webhooks" {
			t.Fatalf("webhooks list %q hit %s, want /v1/apps/demo/webhooks", args, f.sawPath)
		}
	}
	authedFakeAPI(t, `[]`, http.StatusOK)
	if code := cmdWebhooksList([]string{"web", "--app", "demo"}); code != 1 {
		t.Fatalf("conflicting apps exit = %d, want 1", code)
	}
}

// production-us hunt #4 (H4-17): `routes health suggest <slug>` without
// --deployment listed five requirements at once. Each mistake names itself.
func TestRouteHealthTargetErrorNamesTheMissingPiece(t *testing.T) {
	const dep = "0123abcd-0123-4abc-8def-0123456789ab"
	for _, tc := range []struct {
		positional []string
		deployment string
		want       string
	}{
		{positional: nil, deployment: dep, want: "pass the app slug"},
		{positional: []string{"a", "b"}, deployment: dep, want: "exactly one app slug"},
		{positional: []string{"api"}, want: "--deployment is required"},
		{positional: []string{"api"}, deployment: "v7", want: "canonical deployment UUID"},
	} {
		err := routeHealthTargetError(tc.positional, tc.deployment)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("routeHealthTargetError(%q, %q) = %v, want %q", tc.positional, tc.deployment, err, tc.want)
		}
	}
	if err := routeHealthTargetError([]string{"api"}, dep); err != nil {
		t.Errorf("valid target rejected: %v", err)
	}
}

// production-us hunt #4 (H4-38): `github-webhook-secret set ...` handed "set"
// to the flag parser and always failed; the documented verb must reach the
// server, and the legacy verb-less form keeps working.
func TestGithubWebhookSecretSetVerbDispatches(t *testing.T) {
	resetJSONOut(t)
	secret := strings.Repeat("a", 32)
	for _, args := range [][]string{
		{"set", "--installation-id", "42", "--secret", secret},
		{"--installation-id", "42", "--secret", secret},
	} {
		f := authedFakeAPI(t, `{}`, http.StatusOK)
		if code := cmdGithubWebhookSecret(args); code != 0 || f.sawMethod == "" {
			t.Fatalf("github-webhook-secret %q exit=%d request=%s %s, want a request and exit 0", args, code, f.sawMethod, f.sawPath)
		}
	}
	if code := cmdGithubWebhookSecret([]string{"rotate"}); code != 1 {
		t.Fatalf("unknown verb exit = %d, want 1", code)
	}
}

// production-us hunt #4 (H4-42): `deployment summary <uuid>` demanded --app
// although the deployment names its app.
func TestDeploymentSummaryResolvesAppFromDeployment(t *testing.T) {
	resetJSONOut(t)
	t.Chdir(t.TempDir()) // no linked project
	const depID = "0123456789abcdef0123456789abcdef"
	var summaryPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/deployments/"+depID:
			_, _ = w.Write([]byte(`{"id":"` + depID + `","app_id":"app-uuid-1","status":"live"}`))
		case r.URL.Path == "/v1/apps":
			_, _ = w.Write([]byte(`[{"id":"app-uuid-1","slug":"billing-api"}]`))
		case strings.HasPrefix(r.URL.Path, "/v1/apps/billing-api/deployments/"):
			summaryPath = r.URL.Path
			_, _ = w.Write([]byte(`{"deployment":{"id":"` + depID + `","status":"live"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	_, restore := captureStdout(t)
	code := cmdDeploymentSummary([]string{depID})
	restore()
	if code != 0 || summaryPath == "" {
		t.Fatalf("deployment summary <uuid> exit=%d summary request=%q, want the app resolved from the deployment", code, summaryPath)
	}
}

// production-us hunt #4 (H4-44): `crons fire-now <cron-id>` answered only "no
// such fire-now request". The cron id gets pointed at `crons run`.
func TestCronsFireNowWithCronIDPointsAtRun(t *testing.T) {
	resetJSONOut(t)
	const id = "332bde0bfde44444b492c067949ad22a"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cron-fire-now-requests/" + id:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found","detail":"no such fire-now request"}`))
		case "/v1/crons/" + id:
			_, _ = w.Write([]byte(`{"id":"` + id + `","schedule":"* * * * *"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	stderr, restore := captureStderr(t)
	code := cmdCronsFireNowGet([]string{id})
	restore()
	if code == 0 || !strings.Contains(stderr.String(), "gregale crons run "+id) {
		t.Fatalf("exit=%d stderr=%q, want a pointer to `crons run`", code, stderr.String())
	}
}

// production-us hunt #4 (H4-46): `cors rm <slug> <rule-id>` (the shape of
// `cors ls <slug>`) used the slug as the rule id and answered "no such edge
// rule". The slug is accepted; a non-id is refused before any request.
func TestCorsRmAcceptsSlugAndValidatesID(t *testing.T) {
	resetJSONOut(t)
	const id = "622a2d44-d560-49f3-9281-dd191b5282e7"
	for _, args := range [][]string{{id}, {"h3-hello-node", id}} {
		f := authedFakeAPI(t, ``, http.StatusNoContent)
		if code := cmdCorsRm(args); code != 0 || !strings.HasSuffix(f.sawPath, "/"+id) {
			t.Fatalf("cors rm %q exit=%d path=%s, want a delete of %s", args, code, f.sawPath, id)
		}
	}
	f := authedFakeAPI(t, ``, http.StatusNoContent)
	if code := cmdCorsRm([]string{"h3-hello-node"}); code != 1 || f.sawMethod != "" {
		t.Fatalf("cors rm <slug> exit=%d request=%s, want a local rejection", code, f.sawMethod)
	}
}
