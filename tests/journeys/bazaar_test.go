package journeys

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBazaarRegressions replays, as a customer, the bugs three rounds of
// deploying a multi-service project ("bazaar") by hand found. Each check
// names the change that fixed it, so a failure on an older release can be
// told from a regression:
//   - #5934: a Compose service without ports is created internal and its
//     gregale.yaml workflows are registered; a burst over a throttle rule
//     answers 429, not 503; a cache hit sends each X-RateLimit-* header once;
//     metrics --services explains a hidden service map;
//   - #6001: a standalone app created without a size starts at the plan's
//     RAM capped at 1024 MB; an alias whose revision a rollback superseded
//     answers 409; a job that exits in milliseconds keeps its output;
//   - #6060: a job's command can follow --, commas included.
func TestBazaarRegressions(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	t.Run("project", func(t *testing.T) {
		t.Parallel()
		bazaarProject(t, tg)
	})
	t.Run("edge", func(t *testing.T) {
		t.Parallel()
		bazaarEdge(t, tg)
	})
	t.Run("alias_rollback", func(t *testing.T) {
		t.Parallel()
		bazaarAliasRollback(t, tg)
	})
	t.Run("job_output", func(t *testing.T) {
		t.Parallel()
		bazaarJobOutput(t, tg)
	})
	t.Run("cli", func(t *testing.T) {
		t.Parallel()
		bazaarCLI(t, tg)
	})
}

// bazaarWorkflows is the api service's gregale.yaml: one manual workflow.
const bazaarWorkflows = `workflows:
  - name: process_order
    steps:
      - name: charge
        run: charge_order
`

// bazaarProject deploys a public web service and an api service that
// publishes no port and declares a workflow in its own gregale.yaml.
func bazaarProject(t *testing.T, tg *target) {
	web, apiService := tg.app(t, "bz-web"), tg.app(t, "bz-api")
	project := tg.slug("bazaar")
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		res := tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete project %s (%s)", project, res.problem())
		}
	})
	s := newStory(t)
	if !s.step("deploy_project", func(t *testing.T) {
		dir := shopServices(t)
		compose := fmt.Sprintf(`services:
  %[1]s:
    build: ./web
    ports: ["8080:8080"]
    depends_on: [%[2]s]
  %[2]s:
    build: ./api
    expose: ["8080"]
`, web, apiService)
		for path, body := range map[string]string{"compose.yaml": compose, "api/gregale.yaml": bazaarWorkflows} {
			if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
		}
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--project",
			"--project-slug", project, "--path", dir, "--wait", "--timeout", deployLimit.String(), "--yes")
		if res.exit != 0 {
			t.Fatalf("project deploy failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
	}) {
		return
	}
	s.check("unpublished_service_is_internal", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "app", apiService).stdout)
		if got := str(doc, "visibility"); got != "internal" {
			t.Fatalf("%s publishes no port but its app is %q, want internal (#5934): %s", apiService, got, compact(doc))
		}
	})
	s.check("service_manifest_workflows_registered", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "deployment", "latest", "--app", apiService).stdout)
		if !strings.Contains(compact(field(doc, "workflows")), "process_order") {
			t.Fatalf("%s's deployment has no process_order workflow from its gregale.yaml (#5934): workflows=%s",
				apiService, compact(field(doc, "workflows")))
		}
	})
}

// bazaarEdge puts a throttle rule and a cache rule on a probe app.
func bazaarEdge(t *testing.T, tg *target) {
	slug, base := tg.deployProbe(t, "bz-edge")
	s := newStory(t)
	rule := func(t *testing.T, args ...string) {
		t.Helper()
		tg.ok(t, time.Minute, append([]string{"edge", "rule", "create", "--app", slug, "--match-host", "@app"}, args...)...)
	}

	s.check("standalone_default_ram", func(t *testing.T) {
		plan := str(object(t, tg.ok(t, time.Minute, "whoami").stdout), "plan")
		want, known := map[string]int{"free": 256, "hobby": 512, "pro": 1024, "scale": 1024}[plan]
		if !known {
			t.Skipf("no default RAM expectation for plan %q", plan)
		}
		doc := object(t, tg.ok(t, time.Minute, "app", slug).stdout)
		if got := int(num(doc, "ram_mb")); got != want {
			t.Fatalf("an app deployed without a size on the %s plan has ram_mb=%d, want %d (#6001)", plan, got, want)
		}
	})
	if s.step("throttle_rule", func(t *testing.T) {
		rule(t, "--kind", "throttle", "--match-path", "/throttled/**",
			"--throttle-requests-per-second", "2", "--throttle-burst", "3")
	}) {
		s.check("burst_answers_429_not_503", func(t *testing.T) {
			eventually(t, "a burst over the throttle answers 429", 2*time.Minute, 5*time.Second, func() (bool, string) {
				results := burst(base+"/throttled/x", 8)
				refused := 0
				for _, r := range results {
					if r.status == http.StatusServiceUnavailable && failureCode(r) == "rate_limit_unavailable" {
						t.Fatalf("a burst over a 2 rps / burst 3 throttle got 503 rate_limit_unavailable, want 429 (#5934): %s", r)
					}
					if r.status == http.StatusTooManyRequests {
						refused++
					}
				}
				return refused > 0, burstSummary(results)
			})
		})
	}
	if s.step("cache_rule", func(t *testing.T) {
		rule(t, "--kind", "cache", "--match-path", "/cacheable", "--cache-max-age-seconds", "60")
	}) {
		s.check("cache_hit_sends_rate_limit_headers_once", func(t *testing.T) {
			var hit httpResult
			eventually(t, "GET /cacheable is a cache hit", 2*time.Minute, 2*time.Second, func() (bool, string) {
				hit = get(base + "/cacheable")
				return hit.status == http.StatusOK && hit.header.Get("X-Faas-Cache") == "hit",
					hit.String() + " x-faas-cache=" + hit.header.Get("X-Faas-Cache")
			})
			for _, name := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
				if values := hit.header.Values(name); len(values) > 1 {
					t.Errorf("a cache hit sent %s %d times %q, want once (#5934)", name, len(values), values)
				}
			}
		})
	}
}

// burst sends n requests to url at once.
func burst(url string, n int) []httpResult {
	results := make([]httpResult, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = get(url)
		}()
	}
	wg.Wait()
	return results
}

// burstSummary counts a burst's answers by status and failure code.
func burstSummary(results []httpResult) string {
	counts := map[string]int{}
	for _, r := range results {
		key := fmt.Sprint(r.status)
		if code := failureCode(r); code != "" && r.status >= 400 {
			key += " " + code
		}
		counts[key]++
	}
	return fmt.Sprint(counts)
}

// bazaarAliasRollback names v2 with an alias, rolls back to v1, and calls
// the alias, whose revision no longer serves.
func bazaarAliasRollback(t *testing.T, tg *target) {
	slug := tg.app(t, "bz-alias")
	base := tg.appURL(slug)
	s := newStory(t)
	var v1, v2 map[string]any
	var alias string
	if !s.step("deploy_v1_and_v2", func(t *testing.T) {
		v1 = tg.deployVersion(t, slug, "v1")
		if u := str(v1, "app_url"); u != "" {
			base = strings.TrimRight(u, "/")
		}
		v2 = tg.deployVersion(t, slug, "v2", "--rollout", "immediate")
		expectVersion(t, base, "v2", 2*time.Minute)
	}) {
		return
	}
	if !s.step("alias_v2", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "release", "alias", "set", "--app", slug, "--name", "bz",
			"--deployment", str(v2, "id")).stdout)
		if alias = strings.TrimRight(str(doc, "url"), "/"); alias == "" {
			t.Fatalf("alias set printed no URL: %s", compact(doc))
		}
		expectVersion(t, alias, "v2", 2*time.Minute)
	}) {
		return
	}
	if !s.step("rollback_to_v1", func(t *testing.T) {
		tg.ok(t, 27*time.Minute, "rollback", slug, "--to", str(v1, "id"),
			"--expected-current", str(v2, "id"),
			"--reason", "e2e-journeys run "+tg.runID, "--wait", "--timeout", "25m")
		expectVersion(t, base, "v1", 2*time.Minute)
	}) {
		return
	}
	s.check("superseded_alias_answers_409", func(t *testing.T) {
		eventually(t, "the superseded revision's alias answers 409", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(alias + "/")
			return r.status == http.StatusConflict && strings.Contains(string(r.body), "no longer serves traffic"),
				r.String() + " (503 traffic_policy_unavailable before #6001)"
		})
	})
}

// bazaarJobOutput runs a job whose command, given after --, prints a line
// with a comma and exits at once.
func bazaarJobOutput(t *testing.T, tg *target) {
	name := tg.slug("bz-job")
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		if res := tg.run(t, time.Minute, "job", "delete", name); res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete job %s (%s)", name, res.problem())
		}
	})
	s := newStory(t)
	if !s.step("create_with_command_after_double_dash", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "job", "create", name, "--image", "alpine:3.20", "--", "/bin/sh", "-c", "echo bazaar, done")
		doc := object(t, tg.ok(t, time.Minute, "job", "info", name).stdout)
		if got := compact(field(doc, "command")); !strings.Contains(got, "echo bazaar, done") {
			t.Fatalf("the job's command is %s; want /bin/sh -c 'echo bazaar, done' kept whole (#6060)", got)
		}
	}) {
		return
	}
	if !s.step("run", func(t *testing.T) {
		tg.ok(t, 12*time.Minute, "job", "run", name, "--tasks", "1", "--wait", "--wait-timeout", "10m")
	}) {
		return
	}
	s.check("output_of_an_instant_task_is_kept", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "job", "logs", name, "latest", "0").stdout)
		if logs := str(doc, "log_content"); !strings.Contains(logs, "bazaar, done") {
			t.Fatalf("the task's stored output lacks its one line (#6001): %q", tail(logs, 400))
		}
	})
}

// bazaarCLI checks CLI answers for capabilities an account may not have.
func bazaarCLI(t *testing.T, tg *target) {
	s := newStory(t)
	s.check("metrics_services_is_explained", func(t *testing.T) {
		res := tg.run(t, time.Minute, "metrics", "--services")
		if res.exit == 0 {
			return
		}
		out := string(res.stdout) + string(res.stderr)
		if strings.Contains(out, "no such API route") {
			t.Fatalf("metrics --services answered only \"no such API route\" (#5934): %s", tail(out, 400))
		}
		if !strings.Contains(out, "not enabled") && !strings.Contains(out, "unknown flag --services") {
			t.Fatalf("metrics --services failed without naming the hidden service map: %s", tail(out, 400))
		}
	})
}
