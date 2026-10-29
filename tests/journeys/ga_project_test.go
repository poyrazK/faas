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

// shopCompose is the project's Compose file. web publishes a port and
// declares that it calls api; api publishes none, so it is internal, and
// declares its listener with expose:. Service names carry the run's slug so
// each is its app's slug and the sweeper finds a leftover one; the build
// contexts never change, so later runs are build-cache hits.
func shopCompose(web, apiService string) string {
	return fmt.Sprintf(`services:
  %[1]s:
    build: ./web
    ports: ["8080:8080"]
    depends_on: [%[2]s]
  %[2]s:
    build: ./api
    expose: ["8080"]
`, web, apiService)
}

// shopSource copies testdata/project-shop into a fresh directory with its
// Compose file.
func shopSource(t *testing.T, web, apiService string) string {
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
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(shopCompose(web, apiService)), 0o644); err != nil {
		t.Fatalf("write compose.yaml: %v", err)
	}
	return dir
}

// TestMultiServiceProject deploys a web service and an internal api service
// as one project, then checks the project lists both and what web calls; web
// is public and api is not; web reaches api by its injected URL and by its
// .internal name; wake-ahead is on for web; and a call wakes a parked api
// (rows 19, 44, 65, 66).
func TestMultiServiceProject(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	web, apiService := tg.app(t, "web"), tg.app(t, "api")
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
			"--project-slug", project, "--path", shopSource(t, web, apiService),
			"--wait", "--timeout", deployLimit.String(), "--yes")
		if res.exit != 0 {
			t.Fatalf("project deploy failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
		t.Logf("metric project_deploy_s=%d", int(res.elapsed.Seconds()))
		// Project apps take the plan's auth default; the journey calls web anonymously.
		tg.ok(t, time.Minute, "app", web, "--no-require-authn")
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
		if _, ok := calls[apiService]; !ok || !slices.Equal(calls[web], []string{apiService}) {
			t.Fatalf("project %s services = %v; want %s calling %s", project, calls, web, apiService)
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

// expectServiceCall asks web to call api by the injected URL (via=env) or by
// its .internal name (via=dns) until api answers.
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
