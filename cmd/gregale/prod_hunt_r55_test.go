package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r55 (rc.381): `queue dlq list` shows a failed message's
// invocation ID, and `dlq replay APP <that id>` answered "no dead-letter
// event". The ID now resolves to the event whose source it is, and the
// linked app may be left out.
func TestDLQReplayAcceptsTheInvocationID(t *testing.T) {
	const inv, event = "56004ed9-84b8-4b3e-b631-e77f9c2016b9", "897d2945-e710-4dcf-a251-cfbf69514a6f"
	paths := linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/web/dlq":
			_ = json.NewEncoder(w).Encode(api.DeadLetterEventsResponse{Events: []api.DeadLetterEvent{{ID: event, Source: "invocation", SourceID: inv}}})
			return true
		case "POST /v1/apps/web/dlq/" + event + "/replay":
			_ = json.NewEncoder(w).Encode(api.DeadLetterEvent{ID: event})
			return true
		}
		return false
	})
	code, stdout, stderr := runCLI(t, "dlq", "replay", inv)
	if code != 0 {
		t.Fatalf("dlq replay = %d:\n%s\nrequests: %q", code, stderr, *paths)
	}
	if !strings.Contains(stdout, "replayed "+event) {
		t.Fatalf("stdout = %q", stdout)
	}
}

// Prod hunt r55: `domain create D --app A` needs --app and `domain delete D
// --app A` printed usage. --app is now accepted and checked against the
// binding: another app's domain is not removed.
func TestDomainDeleteChecksApp(t *testing.T) {
	for _, tc := range []struct {
		name, boundTo string
		wantCode      bool
	}{{"same app", "app-web", true}, {"other app", "app-other", false}} {
		t.Run(tc.name, func(t *testing.T) {
			paths := linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
				switch r.Method + " " + r.URL.Path {
				case "GET /v1/domains/shop.example.com":
					_ = json.NewEncoder(w).Encode(api.CustomDomainResponse{Domain: "shop.example.com", AppID: tc.boundTo})
					return true
				case "GET /v1/apps/web":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-web", Slug: "web"})
					return true
				case "DELETE /v1/domains/shop.example.com":
					w.WriteHeader(http.StatusNoContent)
					return true
				}
				return false
			})
			code, _, stderr := runCLI(t, "domain", "delete", "shop.example.com", "--app", "web", "--yes")
			deleted := slices.Contains(*paths, "DELETE /v1/domains/shop.example.com")
			if (code == 0) != tc.wantCode || deleted != tc.wantCode {
				t.Fatalf("exit %d, deleted %v, want success %v:\n%s\nrequests: %q", code, deleted, tc.wantCode, stderr, *paths)
			}
		})
	}
}

// Prod hunt r54/r55: `env unset KEY --yes` failed on an unknown flag; every
// other removal takes --yes.
func TestEnvUnsetAcceptsYes(t *testing.T) {
	linkedAppServer(t, nil)
	_, _, stderr := runCLI(t, "env", "unset", "OLD_KEY", "--yes", "--dry-run")
	if strings.Contains(stderr, "flag provided but not defined") || strings.Contains(stderr, "usage:") {
		t.Fatalf("env unset --yes refused:\n%s", stderr)
	}
}

// Prod hunt r55: `invocation get ID --replay` printed usage; flags may now
// follow the ID.
func TestInvocationGetReplayAfterTheID(t *testing.T) {
	const inv = "56004ed9-84b8-4b3e-b631-e77f9c2016b9"
	paths := linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/invocations/" + inv:
			_ = json.NewEncoder(w).Encode(api.Invocation{ID: inv, State: "dead_letter"})
			return true
		case "POST /v1/invocations/" + inv + "/replay":
			_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{})
			return true
		}
		return false
	})
	if code, _, stderr := runCLI(t, "invocation", "get", inv, "--replay"); code != 0 {
		t.Fatalf("invocation get ID --replay = %d:\n%s\nrequests: %q", code, stderr, *paths)
	}
	if !slices.Contains(*paths, "POST /v1/invocations/"+inv+"/replay") {
		t.Fatalf("no replay request: %q", *paths)
	}
}

// Prod hunt r55 (rc.381): `gregale init` (an app at the root) and then
// `gregale add function work --trigger queue` made a plain `gregale deploy`
// deploy only the function; the root app was skipped without a word. Both
// `add function` and the function-set plan now say so and name `--app`.
func TestFunctionSetNamesTheRootAppItLeavesOut(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const note = "gregale deploy --app"
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"node app", map[string]string{"package.json": `{"type":"module","scripts":{"start":"node handler.js"}}`, "handler.js": "export {}\n"}, true},
		{"python app", map[string]string{"requirements.txt": "flask\n", "main.py": "print(1)\n"}, true},
		{"functions only", map[string]string{"package.json": `{"private":true,"scripts":{"test":"node --test"}}`}, false},
		{"empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			code, out, errOut := runAddFunction(t, "work", "--trigger", "queue", "--lang", "node", "--path", dir)
			if code != 0 {
				t.Fatalf("add function exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
			}
			if got := strings.Contains(out, note); got != tc.want {
				t.Errorf("add function names --app = %v, want %v:\n%s", got, tc.want, out)
			}
			t.Chdir(dir)
			code, out, errOut = runCLI(t, "deploy", "--plan")
			if code != 0 {
				t.Fatalf("deploy --plan exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
			}
			if !strings.Contains(out, "Functions (1)") {
				t.Fatalf("deploy --plan is not the function-set plan:\n%s", out)
			}
			if got := strings.Contains(out, note); got != tc.want {
				t.Errorf("deploy --plan names --app = %v, want %v:\n%s", got, tc.want, out)
			}
		})
	}
}

// Prod hunt r55: `gregale link status` read "status" as a project slug and
// answered only "no such project resource"; it now names the project and
// how to see the current link.
func TestLinkUnknownProjectNamesContext(t *testing.T) {
	linkedAppServer(t, nil)
	code, _, stderr := runCLI(t, "link", "status")
	if code == 0 {
		t.Fatal("link of an unknown project succeeded")
	}
	for _, want := range []string{`no project "status"`, "gregale config context get"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}
