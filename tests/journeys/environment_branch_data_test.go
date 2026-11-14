package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEnvironmentBranchData branches a project whose workload keeps rows in
// managed PostgreSQL (ADR-20261031193635 slice 2, docs/environment-branches.md
// "What a branch copies"):
//   - the branch reads production's rows as of the branch point;
//   - a row written in the branch stays out of production, and a row
//     written in production after the branch point stays out of the branch;
//   - a reset copies production again at the same URL, dropping the
//     branch's own row;
//   - deleting the branch leaves production's rows.
//
// It skips while the target has environment branches off.
func TestEnvironmentBranchData(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	project := tg.slug("brdata")
	const branch = "journey-data"
	switch res := tg.runWithEnv(t, branchesCLI, time.Minute, "environment", "branch", project, "--list"); {
	case res.problemCode() == "environment_branch_unavailable":
		t.Skip("environment branches are off on this target")
	case res.exit != 0 && res.problemCode() == "":
		t.Skipf("this CLI cannot branch environments: %s", tg.redact(tail(res.problem(), 300)))
	}
	skipHidden(t, hiddenCapabilities(t, tg), "release-commands")
	j := &postgresJourney{tg: tg, bindings: map[string]string{}}
	j.app, j.db = tg.app(t, "brnotes"), tg.slug("brdata-pg")
	// Cleanups run last-registered first: the branch goes before the
	// project, the project's binding before the database.
	t.Cleanup(func() { j.purge(t, j.db) })
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		j.detach(t, j.db)
	})
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		tg.runWithEnv(t, branchesCLI, 2*time.Minute, "environment", "delete", project, branch, "--yes")
		res := tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete project %s (%s)", project, res.problem())
		}
	})
	production := tg.appURL(j.app)
	j.base = production
	var branchURL string
	inBranch := func() *postgresJourney {
		copy := *j
		copy.base = branchURL
		return &copy
	}
	deployProject := func(t *testing.T, release string) {
		t.Helper()
		dir := t.TempDir()
		if err := os.Rename(postgresProbeSource(t, release), filepath.Join(dir, "notes")); err != nil {
			t.Fatalf("stage the probe: %v", err)
		}
		compose := "services:\n  " + j.app + ":\n    build: ./notes\n    ports: [\"8080:8080\"]\n"
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
	}
	s := newStory(t)

	s.step("postgres_available", j.available)
	s.step("deploy_project", func(t *testing.T) {
		deployProject(t, "")
		tg.ok(t, time.Minute, "app", j.app, "--no-require-authn")
	})
	s.step("create_database", func(t *testing.T) {
		tg.ok(t, time.Minute, "postgres", "create", j.db, "--region", j.region)
		j.waitReady(t, j.db)
	})
	s.step("attach_and_migrate", func(t *testing.T) {
		j.attach(t, j.db, "DATABASE_URL")
		// The table comes from a release command over a migration binding,
		// as docs/postgres.md tells customers.
		migration := j.bind(t, j.db, "MIGRATION_DATABASE_URL", "--access", "migration")
		deployProject(t, "create")
		j.unbind(t, j.db, migration)
	})
	s.step("write_in_production", func(t *testing.T) {
		j.write(t, "DATABASE_URL", "before-branch")
		j.expectRows(t, "DATABASE_URL", "before-branch")
	})

	s.step("branch_production", func(t *testing.T) {
		res := tg.runWithEnv(t, branchesCLI, 2*time.Minute, "environment", "branch", project, branch, "--from", "production", "--ttl", "2h")
		if res.exit != 0 {
			t.Fatalf("branch failed (exit %d): %s", res.exit, tg.redact(res.problem()))
		}
		workloads := asSlice(field(object(t, res.stdout), "branch.workloads"))
		if len(workloads) != 1 {
			t.Fatalf("branch = %s; want the one production workload", compact(object(t, res.stdout)))
		}
		branchURL = strings.TrimSuffix(str(workloads[0], "url"), "/")
		if branchURL == "" || branchURL == production {
			t.Fatalf("branch workload url = %q; want its own address", branchURL)
		}
		eventually(t, "the branch's database copy is ready", 5*time.Minute, 5*time.Second, func() (bool, string) {
			doc := object(t, tg.runWithEnvOK(t, branchesCLI, time.Minute, "environment", "branch", project, branch, "--status").stdout)
			return str(doc, "branch.data") == "ready" && len(asSlice(field(doc, "branch.databases"))) == 1, compact(doc)
		})
	})

	s.check("branch_reads_production_rows", func(t *testing.T) {
		inBranch().expectRows(t, "DATABASE_URL", "before-branch")
	})

	s.step("writes_stay_on_their_side", func(t *testing.T) {
		inBranch().write(t, "DATABASE_URL", "in-branch")
		j.write(t, "DATABASE_URL", "after-branch")
		inBranch().expectRows(t, "DATABASE_URL", "before-branch", "in-branch")
		j.expectRows(t, "DATABASE_URL", "before-branch", "after-branch")
	})

	s.step("reset_copies_production_again", func(t *testing.T) {
		res := tg.runWithEnv(t, branchesCLI, 3*time.Minute, "environment", "branch", project, branch, "--reset")
		if res.exit != 0 {
			t.Fatalf("reset failed (exit %d): %s", res.exit, tg.redact(res.problem()))
		}
		eventually(t, "the reset branch's database copy is ready", 5*time.Minute, 5*time.Second, func() (bool, string) {
			doc := object(t, tg.runWithEnvOK(t, branchesCLI, time.Minute, "environment", "branch", project, branch, "--status").stdout)
			return str(doc, "branch.data") == "ready", compact(doc)
		})
		inBranch().expectRows(t, "DATABASE_URL", "before-branch", "after-branch")
	})

	s.step("delete_branch", func(t *testing.T) {
		tg.runWithEnvOK(t, branchesCLI, 2*time.Minute, "environment", "delete", project, branch, "--yes")
		eventually(t, "the deleted branch stops serving", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(branchURL + "/notes?env=DATABASE_URL")
			return r.status != 200, r.String()
		})
	})

	s.check("production_rows_unchanged", func(t *testing.T) {
		j.expectRows(t, "DATABASE_URL", "before-branch", "after-branch")
	})
}
