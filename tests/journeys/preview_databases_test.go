package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// PR preview databases (ADR-20261102123523), a preview capability: a
// production database's `previews` setting gives each pull request's preview
// its own database, bound with production's environment keys and access,
// migrated by the PR's release command and purged with the preview.

// previewDatabaseJourney is the state TestPreviewDatabases's steps share.
type previewDatabaseJourney struct {
	*postgresJourney
	repo, branch string
	number       int    // the fixture pull request
	preview      string // the preview app's slug
	sourceID     string // the production database's ID
	database     string // the preview database's name
}

// TestPreviewDatabases sets a production database's previews to `empty`,
// opens a pull request on the fixture repository whose release command
// creates the probe's table, and checks that the preview reads and writes
// its own database, migrated by that release command, while production
// keeps its rows; deleting the preview purges its database. It needs the
// GitHub fixture repository (GREGALE_E2E_GITHUB_REPO), as TestGitHubDeploys
// does.
func TestPreviewDatabases(t *testing.T) {
	tg := needs(t, tierFull, true)
	repo := os.Getenv("GREGALE_E2E_GITHUB_REPO")
	if repo == "" {
		t.Skip("no fixture repository: set GREGALE_E2E_GITHUB_REPO (scripts/e2e-journeys.sh --github-repo)")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("the gh CLI is not installed")
	}
	hidden := hiddenCapabilities(t, tg)
	skipHidden(t, hidden, "preview-databases")
	skipHidden(t, hidden, "release-commands")
	t.Parallel()
	j := &previewDatabaseJourney{postgresJourney: &postgresJourney{tg: tg, bindings: map[string]string{}}, repo: repo}
	j.app, j.db = tg.app(t, "pgpr"), tg.slug("pgpr")
	// Cleanups run last-registered first: the preview (and with it its
	// database) goes before the production database, which goes before the
	// app.
	t.Cleanup(func() { j.purge(t, j.db) })
	t.Cleanup(func() { j.cleanup(t) })
	s := newStory(t)
	s.step("postgres_available", j.available)
	s.step("create_database", func(t *testing.T) {
		tg.ok(t, time.Minute, "postgres", "create", j.db, "--region", j.region)
		j.sourceID = str(j.waitReady(t, j.db), "id")
	})
	s.step("deploy_app", func(t *testing.T) {
		receipt := tg.deploy(t, j.app, "--path", postgresProbeSource(t, ""))
		j.base = strings.TrimRight(str(receipt, "app_url"), "/")
		if j.base == "" {
			j.base = tg.appURL(j.app)
		}
	})
	s.step("attach_database", func(t *testing.T) { j.attach(t, j.db, "DATABASE_URL") })
	s.step("migrate_production", j.migrateProduction)
	s.step("previews_empty", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "postgres", "previews", "set", j.db, "empty").stdout)
		if str(doc, "previews") != "empty" {
			t.Fatalf("previews set empty answered %s", compact(doc))
		}
	})
	s.step("open_pull_request", j.openPullRequest)
	s.step("preview_database_ready", j.previewDatabaseReady)
	s.step("preview_deploy", func(t *testing.T) {
		tg.ok(t, 25*time.Minute, "preview", "create", "--app", j.app, "--repo", j.repo, "--ref", j.branch,
			"--pr-number", strconv.Itoa(j.number), "--wait")
		tg.ok(t, 25*time.Minute, "preview", "wait", j.preview, "--timeout", "20m")
	})
	s.step("preview_reads_its_own_database", func(t *testing.T) {
		preview := *j.postgresJourney
		preview.base = tg.appURL(j.preview)
		// The preview's release command made the table in its own database,
		// which starts empty; production's rows stay where they are.
		preview.expectRows(t, "DATABASE_URL")
		preview.write(t, "DATABASE_URL", "preview")
		preview.expectRows(t, "DATABASE_URL", "preview")
		j.expectRows(t, "DATABASE_URL", "production")
	})
	s.step("preview_delete_purges_database", j.deletePreview)
}

// migrateProduction creates production's table through a migration binding
// and writes one row. The migration binding stays: a preview gets each of
// production's bindings, so its release command migrates its own database
// through its own MIGRATION_DATABASE_URL.
func (j *previewDatabaseJourney) migrateProduction(t *testing.T) {
	j.bind(t, j.db, "MIGRATION_DATABASE_URL", "--access", "migration")
	j.tg.deploy(t, j.app, "--path", postgresProbeSource(t, "create"), "--rollout", "immediate")
	j.write(t, "DATABASE_URL", "production")
}

// openPullRequest pushes a branch whose whole tree is the probe with its
// release command, and opens a pull request for it. The tree is fixed, so
// after the first run the preview's build is a build-cache hit.
func (j *previewDatabaseJourney) openPullRequest(t *testing.T) {
	files := map[string]string{"Procfile": "web: node server.js\nrelease: node server.js migrate create\n"}
	src := filepath.Join("testdata", "postgres-probe")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read postgres probe source: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read postgres probe source: %v", err)
		}
		files[e.Name()] = string(b)
	}
	j.branch = "e2ej-pgpr-" + j.tg.runID
	ghBranchWithFiles(t, j.repo, "main", j.branch, files, "e2e journeys preview database "+j.tg.runID)
	prURL := strings.TrimSpace(gh(t, "pr", "create", "--repo", j.repo, "--head", j.branch, "--base", "main",
		"--title", "e2e journeys preview database "+j.tg.runID, "--body", "Opened and closed by the Gregale journey suite."))
	n, err := strconv.Atoi(prURL[strings.LastIndex(prURL, "/")+1:])
	if err != nil {
		t.Fatalf("gh pr create printed %q", prURL)
	}
	j.number, j.preview = n, fmt.Sprintf("pr-%d-%s", n, j.app)
}

// previewDatabaseReady creates the preview environment, which sets up its
// database, and waits until the database and the preview's copies of
// production's two bindings are ready. Waiting here keeps the release
// command from running before its MIGRATION_DATABASE_URL exists: the API
// create path does not wait for a provisioning database.
func (j *previewDatabaseJourney) previewDatabaseReady(t *testing.T) {
	key := j.tg.scopedKey(t, "pgpr-preview")
	r := j.tg.apiCall(http.MethodPost, "/v1/apps/"+j.app+"/previews", map[string]int{"pr_number": j.number}, key)
	if r.status != http.StatusCreated && r.status != http.StatusOK {
		t.Fatalf("create preview %s: %s", j.preview, r)
	}
	eventually(t, "the preview's database", 5*time.Minute, 10*time.Second, func() (bool, string) {
		j.database = j.previewDatabaseName(t)
		return j.database != "", "no database lists preview.pr_number=" + strconv.Itoa(j.number)
	})
	doc := j.waitReady(t, j.database)
	if str(doc, "service_class") != "development" || str(doc, "id") == j.sourceID {
		t.Errorf("preview database reads as %s", compact(doc))
	}
	eventually(t, "the preview's bindings ready", 5*time.Minute, 5*time.Second, func() (bool, string) {
		list := object(t, j.tg.ok(t, time.Minute, "postgres", "bindings", "list", j.database).stdout)
		access := map[string]string{}
		for _, item := range items(list, "items") {
			if str(item, "state") == "ready" {
				access[str(item, "environment_key")] = str(item, "access")
			}
		}
		return access["DATABASE_URL"] != "" && access["MIGRATION_DATABASE_URL"] == "migration", compact(list)
	})
}

// previewDatabaseName finds the database the preview environment got from
// the production database, or "" before it is listed.
func (j *previewDatabaseJourney) previewDatabaseName(t *testing.T) string {
	t.Helper()
	list := object(t, j.tg.ok(t, time.Minute, "postgres", "list").stdout)
	for _, item := range items(list, "items") {
		if str(item, "preview.source_database_id") == j.sourceID && str(item, "preview.pr_number") == strconv.Itoa(j.number) &&
			str(item, "state") != "deleted" {
			return str(item, "name")
		}
	}
	return ""
}

// deletePreview deletes the preview: its database is purged with it, with no
// undo window.
func (j *previewDatabaseJourney) deletePreview(t *testing.T) {
	j.tg.ok(t, 2*time.Minute, "preview", "delete", j.preview)
	j.preview = ""
	eventually(t, j.database+" purged", pgDatabaseWait, 5*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "postgres", "get", j.database)
		if res.exit != 0 {
			return strings.Contains(res.problem(), "not_found"), res.problem()
		}
		doc := object(t, res.stdout)
		return str(doc, "state") == "deleted", compact(doc)
	})
	j.database = ""
}

// cleanup removes what a failed run left: the preview, its database, the
// pull request and the branch. It only logs failures.
func (j *previewDatabaseJourney) cleanup(t *testing.T) {
	if j.preview != "" {
		j.tg.run(t, 2*time.Minute, "preview", "delete", j.preview)
	}
	if j.database != "" {
		j.purge(t, j.database)
	}
	if j.number != 0 {
		ghQuiet("pr", "close", strconv.Itoa(j.number), "--repo", j.repo)
	}
	if j.branch != "" {
		ghQuiet("api", "-X", "DELETE", "repos/"+j.repo+"/git/refs/heads/"+j.branch)
	}
}

// ghBranchWithFiles creates branch from base's head with a commit whose
// whole tree is files.
func ghBranchWithFiles(t *testing.T, repo, base, branch string, files map[string]string, message string) string {
	t.Helper()
	parent := strings.TrimSpace(gh(t, "api", "repos/"+repo+"/git/ref/heads/"+base, "--jq", ".object.sha"))
	type entry struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	tree := struct {
		Tree []entry `json:"tree"`
	}{}
	for path, content := range files {
		tree.Tree = append(tree.Tree, entry{Path: path, Mode: "100644", Type: "blob", Content: content})
	}
	treeSHA := ghInput(t, tree, "api", "repos/"+repo+"/git/trees", "--jq", ".sha")
	commit := ghInput(t, map[string]any{"message": message, "tree": treeSHA, "parents": []string{parent}},
		"api", "repos/"+repo+"/git/commits", "--jq", ".sha")
	gh(t, "api", "repos/"+repo+"/git/refs", "-f", "ref=refs/heads/"+branch, "-f", "sha="+commit)
	return commit
}

// ghInput runs the GitHub CLI with body as its JSON input and returns the
// trimmed output.
func ghInput(t *testing.T, body any, args ...string) string {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gh", append(args, "--input", "-")...)
	cmd.Stdin = strings.NewReader(string(b))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gh %s: %v: %s", strings.Join(args, " "), err, tail(string(out), 500))
	}
	return strings.TrimSpace(string(out))
}
