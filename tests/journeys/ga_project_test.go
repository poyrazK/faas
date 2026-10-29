package journeys

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// GA inventory rows 19 (projects), 44 (service wake-ahead), 65 (internal
// services) and 66 (service bindings): a Compose repository deploys as one
// project, and its services call each other by name as the multi-service
// guide (docs/multi-service-apps.md) says.

// shop names the project's services. Service names carry the run's slug so
// each is its app's slug and the sweeper finds a leftover one.
type shop struct{ web, api, ops, batch string }

// shopCompose is the project's Compose file:
//   - web publishes a port and declares that it calls api;
//   - api publishes none, so it is internal; it declares its listener with
//     expose: and allows only web to call it;
//   - ops runs web's code under the declared policy and declares only web;
//   - batch runs web's code under the project policy and declares api, which
//     does not allow it.
//
// The build contexts never change, so later runs are build-cache hits.
func shopCompose(s shop) string {
	return fmt.Sprintf(`services:
  %[1]s:
    build: ./web
    ports: ["8080:8080"]
    depends_on: [%[2]s]
  %[2]s:
    build: ./api
    expose: ["8080"]
    x-gregale-allow-callers: [%[1]s]
  %[3]s:
    build: ./web
    ports: ["8080:8080"]
    depends_on: [%[1]s]
    x-gregale-service-policy: declared
  %[4]s:
    build: ./web
    ports: ["8080:8080"]
    depends_on: [%[2]s]
`, s.web, s.api, s.ops, s.batch)
}

// shopSource copies testdata/project-shop into a fresh directory with its
// Compose file.
func shopSource(t *testing.T, s shop) string {
	t.Helper()
	dir := t.TempDir()
	for _, service := range []string{"web", "api"} {
		src := filepath.Join("testdata", "project-shop", service)
		entries, err := os.ReadDir(src)
		if err != nil {
			t.Fatalf("read shop source: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(dir, service), 0o755); err != nil {
			t.Fatalf("create shop source: %v", err)
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				t.Fatalf("read shop source: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, service, e.Name()), b, 0o644); err != nil {
				t.Fatalf("write shop source: %v", err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(shopCompose(s)), 0o644); err != nil {
		t.Fatalf("write compose.yaml: %v", err)
	}
	return dir
}

// TestMultiServiceProject deploys a web service, an internal api service and
// two more callers as one project, then checks:
//   - the project lists them and what each calls;
//   - web is public and api is not;
//   - web reaches api by its injected URL, its .internal name and the private
//     HTTPS alias;
//   - service authorization: the declared policy and api's caller allowlist,
//     both through `gregale app binding service explain` and on live calls;
//   - wake-ahead is on for web, and a call wakes a parked api.
//
// Rows 19, 44, 65 and 66.
func TestMultiServiceProject(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	svc := shop{web: tg.app(t, "web"), api: tg.app(t, "api"), ops: tg.app(t, "ops"), batch: tg.app(t, "batch")}
	web, apiService := svc.web, svc.api
	project := tg.slug("shop")
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		// Deleting the project detaches its apps; tg.app's cleanups delete them.
		res := tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete project %s (%s)", project, res.problem())
		}
	})
	base := tg.appURL(web)
	s := newStory(t)

	s.step("deploy_project", func(t *testing.T) {
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--project",
			"--project-slug", project, "--path", shopSource(t, svc),
			"--wait", "--timeout", deployLimit.String(), "--yes")
		if res.exit != 0 {
			t.Fatalf("project deploy failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
		t.Logf("metric project_deploy_s=%d", int(res.elapsed.Seconds()))
		// Project apps take the plan's auth default; the journey calls the
		// public services anonymously.
		for _, public := range []string{svc.web, svc.ops, svc.batch} {
			tg.ok(t, time.Minute, "app", public, "--no-require-authn")
		}
	})
	s.check("project_lists_services", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "project", "get", project).stdout)
		workloads, _ := field(doc, "workloads").([]any)
		calls := map[string][]string{}
		for _, raw := range workloads {
			var deps []string
			for _, dep := range asSlice(field(raw, "depends_on")) {
				deps = append(deps, fmt.Sprint(dep))
			}
			calls[str(raw, "workload_name")] = deps
		}
		_, listsAPI := calls[apiService]
		if !listsAPI || !slices.Equal(calls[web], []string{apiService}) || !slices.Equal(calls[svc.ops], []string{web}) ||
			!slices.Equal(calls[svc.batch], []string{apiService}) {
			t.Fatalf("project %s services = %v; want web and batch calling api, ops calling web", project, calls)
		}
	})
	s.step("web_is_public", func(t *testing.T) {
		eventually(t, "web answers on its public URL", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/")
			return r.status == http.StatusOK && str(jsonBody(t, r), "service") == "web", r.String()
		})
	})
	s.check("api_has_no_public_url", func(t *testing.T) {
		r := get(tg.appURL(apiService) + "/whoami")
		if r.status == http.StatusOK {
			t.Fatalf("the internal api answered on a public URL: %s", r)
		}
	})
	s.step("call_by_injected_url", func(t *testing.T) {
		expectServiceCall(t, base, "env")
	})
	s.check("call_by_internal_name", func(t *testing.T) {
		expectServiceCall(t, base, "dns")
	})
	s.check("call_over_private_https", func(t *testing.T) {
		// The private HTTPS listener is an installation option
		// (docs/networking.md); guest-init sets up its trust only where it
		// is enabled.
		if dep := jsonBody(t, get(base+"/dependency")); field(dep, "ca_bundle") != true {
			t.Skip("private service HTTPS is not enabled on this installation (no GREGALE_SERVICE_CA_BUNDLE in the guest)")
		}
		expectServiceCall(t, base, "https")
	})
	s.check("explain_authorization", func(t *testing.T) {
		for _, tc := range []struct {
			caller, target string
			exit           int
		}{{web, apiService, 0}, {svc.ops, web, 0}, {svc.ops, apiService, 1}, {svc.batch, apiService, 1}} {
			res := tg.run(t, time.Minute, "app", "binding", "service", "explain", tc.caller, tc.target)
			if res.exit != tc.exit {
				t.Errorf("explain %s -> %s exit %d, want %d: %s", tc.caller, tc.target, res.exit, tc.exit, tail(string(res.stdout), 500))
			}
		}
	})
	s.check("declared_policy_allows_the_declared_call", func(t *testing.T) {
		expectCall(t, tg.appURL(svc.ops)+"/call", http.StatusOK, "web")
	})
	s.check("declared_policy_refuses_an_undeclared_call", func(t *testing.T) {
		expectCall(t, tg.appURL(svc.ops)+"/call?service=api", http.StatusForbidden, "caller has not declared this service binding")
	})
	s.check("allowlist_refuses_other_callers", func(t *testing.T) {
		expectCall(t, tg.appURL(svc.batch)+"/call", http.StatusForbidden, "target does not allow this service caller")
	})
	s.check("wake_ahead_on_by_default", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "app", "wake-ahead", web).stdout)
		if enabled, _ := field(doc, "enabled").(bool); !enabled {
			t.Fatalf("wake-ahead for %s = %s; want on by default for a project service", web, compact(doc))
		}
	})
	s.check("parked_service_wakes_on_call", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "app", "park", apiService)
		waitParked(t, tg, apiService)
		r := get(base + "/call?via=env")
		doc := jsonBody(t, r)
		if r.status != http.StatusOK || num(doc, "status") != http.StatusOK || str(doc, "body.service") != "api" {
			t.Fatalf("call to the parked api: %s", r)
		}
		t.Logf("metric service_call_wake_ms=%d", int(num(doc, "ms")))
	})
}

// expectCall asks a service of the project to make a call (url is its
// /call endpoint) until the callee answers with status, and with want as
// its service name (200) or in its refusal (403).
func expectCall(t *testing.T, url string, status int, want string) {
	t.Helper()
	eventually(t, fmt.Sprintf("%s answers %d", url, status), 2*time.Minute, 3*time.Second, func() (bool, string) {
		r := get(url)
		if r.status != http.StatusOK {
			return false, r.String()
		}
		doc := jsonBody(t, r)
		if int(num(doc, "status")) != status {
			return false, compact(doc)
		}
		if status == http.StatusOK {
			return str(doc, "body.service") == want, compact(doc)
		}
		return strings.Contains(str(doc, "body.raw"), want), compact(doc)
	})
}

// expectServiceCall asks web to call api by the injected URL (via=env), its
// .internal name (via=dns) or the private HTTPS alias (via=https) until api
// answers.
func expectServiceCall(t *testing.T, base, via string) {
	t.Helper()
	eventually(t, "web calls api ("+via+")", 2*time.Minute, 3*time.Second, func() (bool, string) {
		r := get(base + "/call?via=" + via)
		if r.status != http.StatusOK {
			return false, r.String()
		}
		doc := jsonBody(t, r)
		return num(doc, "status") == http.StatusOK && str(doc, "body.service") == "api", compact(doc)
	})
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}
