package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// branchesCLI shows `gregale environment branch`, which the CLI hides while
// environment branches are dark (cmd/gregale/envbranch.go).
var branchesCLI = []string{"FAAS_ENVIRONMENT_BRANCHES_ENABLED=1"}

// TestEnvironmentBranches deploys a one-service project and branches its
// production environment (ADR-20261031193635, docs/environment-branches.md):
//   - the branch is created at once and names its workload's URL;
//   - the workload, never started in the branch, answers its first request
//     from the parent's build;
//   - the branch lists under its parent with its data ready;
//   - deleting the branch stops its URL serving, and production keeps
//     serving.
//
// It skips while the target has environment branches off.
func TestEnvironmentBranches(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	project := tg.slug("branches")
	const branch = "journey"
	// The project does not exist yet, so the API answers not_found once
	// branches are on. Anything else skips before a project is deployed for
	// nothing.
	switch res := tg.runWithEnv(t, branchesCLI, time.Minute, "environment", "branch", project, "--list"); {
	case res.problemCode() == "environment_branch_unavailable":
		t.Skip("environment branches are off on this target")
	case res.exit != 0 && res.problemCode() == "":
		t.Skipf("this CLI cannot branch environments: %s", tg.redact(tail(res.problem(), 300)))
	}
	web := tg.app(t, "brweb")
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		// A parent with branches cannot be deleted, so the branch goes first.
		tg.runWithEnv(t, branchesCLI, 2*time.Minute, "environment", "delete", project, branch, "--yes")
		res := tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete project %s (%s)", project, res.problem())
		}
	})
	production := tg.appURL(web)
	var branchURL string
	s := newStory(t)

	s.step("deploy_project", func(t *testing.T) {
		dir := shopServices(t)
		compose := "services:\n  " + web + ":\n    build: ./web\n    ports: [\"8080:8080\"]\n"
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
			t.Fatalf("write compose.yaml: %v", err)
		}
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--project",
			"--project-slug", project, "--path", dir, "--wait", "--timeout", deployLimit.String(), "--yes")
		if res.exit != 0 {
			t.Fatalf("project deploy failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
		// Project apps take the plan's auth default; the journey calls the
		// workload anonymously, in production and in the branch.
		tg.ok(t, time.Minute, "app", web, "--no-require-authn")
		eventually(t, "production serves", 3*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(production + "/")
			return r.status == 200 && strings.Contains(string(r.body), `"service":"web"`), r.String()
		})
	})

	s.step("branch_production", func(t *testing.T) {
		res := tg.runWithEnv(t, branchesCLI, 2*time.Minute, "environment", "branch", project, branch, "--from", "production", "--ttl", "1h")
		if res.exit != 0 {
			t.Fatalf("branch failed (exit %d): %s", res.exit, tg.redact(res.problem()))
		}
		t.Logf("metric environment_branch_create_s=%.1f", res.elapsed.Seconds())
		doc := object(t, res.stdout)
		if str(doc, "slug") != branch || str(doc, "branch.parent") != "production" {
			t.Fatalf("branch = %s; want %s branched from production", compact(doc), branch)
		}
		workloads := asSlice(field(doc, "branch.workloads"))
		if len(workloads) != 1 {
			t.Fatalf("branch workloads = %s; want the one production workload", compact(doc))
		}
		branchURL = strings.TrimSuffix(str(workloads[0], "url"), "/")
		if branchURL == "" || branchURL == production {
			t.Fatalf("branch workload url = %q; want its own address", branchURL)
		}
	})

	s.check("branch_lists_under_parent", func(t *testing.T) {
		eventually(t, "the branch's data is ready", 2*time.Minute, 3*time.Second, func() (bool, string) {
			res := tg.runWithEnv(t, branchesCLI, time.Minute, "environment", "branch", project, "--list", "--from", "production")
			if res.exit != 0 {
				return false, res.problem()
			}
			doc := object(t, res.stdout)
			for _, b := range asSlice(field(doc, "branches")) {
				if str(b, "slug") == branch {
					return str(b, "data") == "ready", compact(b)
				}
			}
			return false, compact(doc)
		})
	})

	s.step("first_request_starts_the_branch", func(t *testing.T) {
		start := time.Now()
		eventually(t, "the branch serves its first request", 3*time.Minute, 2*time.Second, func() (bool, string) {
			r := get(branchURL + "/")
			return r.status == 200 && strings.Contains(string(r.body), `"service":"web"`), r.String()
		})
		t.Logf("metric environment_branch_first_request_s=%.1f", time.Since(start).Seconds())
	})

	var expiresAt time.Time
	s.check("status_names_the_branch", func(t *testing.T) {
		doc := object(t, tg.runWithEnvOK(t, branchesCLI, time.Minute, "environment", "branch", project, branch, "--status").stdout)
		workloads := asSlice(field(doc, "branch.workloads"))
		if str(doc, "branch.parent") != "production" || len(workloads) != 1 ||
			strings.TrimSuffix(str(workloads[0], "url"), "/") != branchURL {
			t.Fatalf("status = %s; want a branch of production serving at %s", compact(doc), branchURL)
		}
		var err error
		if expiresAt, err = time.Parse(time.RFC3339, str(doc, "branch.expires_at")); err != nil {
			t.Fatalf("status expires_at %q: %v", str(doc, "branch.expires_at"), err)
		}
	})

	s.check("extend_moves_the_expiry", func(t *testing.T) {
		doc := object(t, tg.runWithEnvOK(t, branchesCLI, time.Minute, "environment", "branch", project, branch, "--extend", "3h").stdout)
		extended, err := time.Parse(time.RFC3339, str(doc, "branch.expires_at"))
		if err != nil || !extended.After(expiresAt) || extended.Before(time.Now().Add(2*time.Hour)) {
			t.Fatalf("extend = %s (%v); want an expiry about 3h from now, after %s", compact(doc), err, expiresAt)
		}
	})

	s.step("reset_keeps_the_url", func(t *testing.T) {
		// ADR-20261031193635 amendment 2026-11-12: a reset makes the branch
		// again from production, and its workloads keep their URLs.
		res := tg.runWithEnv(t, branchesCLI, 3*time.Minute, "environment", "branch", project, branch, "--reset")
		if res.exit != 0 {
			t.Fatalf("reset failed (exit %d): %s", res.exit, tg.redact(res.problem()))
		}
		doc := object(t, res.stdout)
		workloads := asSlice(field(doc, "branch.workloads"))
		if len(workloads) != 1 || strings.TrimSuffix(str(workloads[0], "url"), "/") != branchURL {
			t.Fatalf("reset = %s; want the workload still at %s", compact(doc), branchURL)
		}
		eventually(t, "the reset branch serves at its URL", 3*time.Minute, 2*time.Second, func() (bool, string) {
			r := get(branchURL + "/")
			return r.status == 200 && strings.Contains(string(r.body), `"service":"web"`), r.String()
		})
	})

	s.step("delete_branch", func(t *testing.T) {
		res := tg.runWithEnv(t, branchesCLI, 2*time.Minute, "environment", "delete", project, branch, "--yes")
		if res.exit != 0 {
			t.Fatalf("branch delete failed (exit %d): %s", res.exit, tg.redact(res.problem()))
		}
		eventually(t, "the deleted branch stops serving", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(branchURL + "/")
			return r.status != 200, r.String()
		})
	})

	s.check("production_unchanged", func(t *testing.T) {
		r := get(production + "/")
		if r.status != 200 || !strings.Contains(string(r.body), `"service":"web"`) {
			t.Fatalf("production after the branch = %s", r.String())
		}
	})
}
