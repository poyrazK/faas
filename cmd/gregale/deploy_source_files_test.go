package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func sourceFileRows(report *main.DeploySourceFilesReport) map[string]main.DeploySourceFile {
	rows := map[string]main.DeploySourceFile{}
	for _, row := range report.Files {
		rows[row.Path] = row
	}
	return rows
}

func assertSourceFileArchive(t *testing.T, report *main.DeploySourceFilesReport, archive string) {
	t.Helper()
	files := archiveFiles(t, archive)
	want := map[string]int64{}
	var total int64
	for name, body := range files {
		want[name] = int64(len(body))
		total += int64(len(body))
	}
	got := map[string]int64{}
	for _, row := range report.Files {
		if row.Included && row.Kind == "file" {
			got[row.ArchivePath] = row.Bytes
		}
	}
	if !reflect.DeepEqual(got, want) || report.IncludedBytes != total || report.FileCount != len(want) {
		t.Fatalf("report differs from actual archive: got %v / %d bytes / %d files; want %v / %d bytes", got, report.IncludedBytes, report.FileCount, want, total)
	}
}

func TestDeploySourceFilesWorktreeArchiveParity(t *testing.T) {
	for _, docker := range []bool{false, true} {
		t.Run(map[bool]string{false: "framework", true: "Dockerfile"}[docker], func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"package.json": "{}", "src/server.js": "source", "dist/app.js": "built", "target/app.jar": "jar",
				"node_modules/ignored.js": "installed", "vendor/modules.txt": "# example.com/x v1\n", "vendor/x/x.go": "vendored",
				".git/config": "metadata", ".gregaleignore": "# comment\n*.log\n!keep.log\n!dist/\nignored/\n!.git/\n",
				"drop.log": "drop", "keep.log": "keep", "ignored/child.js": "excluded subtree", ".env": "PRIVATE=never-print-file-values\n",
			}
			if docker {
				files["Dockerfile"] = "FROM scratch\nCOPY . /app\n"
				files[".dockerignore"] = "src/server.js\ndist/\n"
			}
			writeSourceFiles(t, dir, files)
			report, archive, err := main.ExplainDeploySourceForTest(t, dir, "worktree")
			if err != nil {
				t.Fatal(err)
			}
			assertSourceFileArchive(t, report, archive)
			baseline, _, _, err := main.AutoPackSource(dir, dir, false, 250, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Remove(baseline) }()
			if !reflect.DeepEqual(archiveFiles(t, baseline), archiveFiles(t, archive)) {
				t.Fatal("explanation changed the ordinary deployment payload")
			}
			rows := sourceFileRows(report)
			if !rows["keep.log"].Included || rows["keep.log"].RuleFile != ".gregaleignore" || rows["keep.log"].RuleLine != 3 || rows["drop.log"].Included {
				t.Fatalf("ignore provenance/negation missing: %v", rows)
			}
			if rows[".git"].Included || rows["ignored"].Included || !rows["dist"].Included || !rows["vendor"].Included || !rows[".env"].CredentialLike {
				t.Fatalf("packaging decisions differ: %v", rows)
			}
			if _, exists := rows["ignored/child.js"]; exists {
				t.Fatal("explanation descended into a pruned directory")
			}
			if docker && (rows["src/server.js"].Included || rows["src/server.js"].RuleFile != ".dockerignore" || !rows["target"].Included) {
				t.Fatal("Dockerfile context policy was not used")
			}
		})
	}
}

func initSourceFilesGit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "source-files@example.test"}, {"config", "user.name", "Source files test"}, {"add", "-A"}, {"commit", "-qm", "fixture"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestDeploySourceFilesHEADAndWorktree(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{"package.json": "{}", "server.js": "committed", ".gregaleignore": "# line\n*.log\n", "drop.log": "drop"})
	initSourceFilesGit(t, dir)
	writeSourceFiles(t, dir, map[string]string{"server.js": "dirty and longer", "untracked.js": "untracked", ".gregaleignore": "server.js\n"})
	head, archive, err := main.ExplainDeploySourceForTest(t, dir, "head")
	if err != nil {
		t.Fatal(err)
	}
	assertSourceFileArchive(t, head, archive)
	rows := sourceFileRows(head)
	if head.SourceMode != "head" || head.Commit == "" || rows["server.js"].Bytes != int64(len("committed")) || !rows["server.js"].Included || rows["drop.log"].Included || rows["drop.log"].RuleLine != 2 {
		t.Fatalf("HEAD used working tree data: %v", head)
	}
	if _, exists := rows["untracked.js"]; exists {
		t.Fatal("HEAD included an untracked file")
	}
	baselineInput := filepath.Join(t.TempDir(), "head.tar.gz")
	if err := main.GitArchiveHEAD(dir, baselineInput); err != nil {
		t.Fatal(err)
	}
	baseline, _, _, err := main.PackGitArchive(baselineInput, 250, main.SecretScanOff, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(baseline) }()
	if !reflect.DeepEqual(archiveFiles(t, baseline), archiveFiles(t, archive)) {
		t.Fatal("HEAD report payload differs from ordinary Git packer")
	}
	worktree, workArchive, err := main.ExplainDeploySourceForTest(t, dir, "worktree")
	if err != nil {
		t.Fatal(err)
	}
	assertSourceFileArchive(t, worktree, workArchive)
	work := sourceFileRows(worktree)
	if work["server.js"].Included || !work["untracked.js"].Included || !work["drop.log"].Included || worktree.SourceMode != "worktree" {
		t.Fatalf("worktree did not use current bytes and rules: %v", worktree)
	}
}

func TestDeploySourceFilesGeneratedGoModule(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{"handler.go": "package main\nfunc main() {}\n"})
	for _, source := range []string{"worktree", "head"} {
		t.Run(source, func(t *testing.T) {
			if source == "head" {
				initSourceFilesGit(t, dir)
			}
			report, archive, err := main.ExplainDeploySourceForTest(t, dir, source)
			if err != nil {
				t.Fatal(err)
			}
			assertSourceFileArchive(t, report, archive)
			if row := sourceFileRows(report)["go.mod"]; !row.Included || !row.Generated || row.Bytes == 0 {
				t.Fatalf("generated module missing: %v", row)
			}
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); !os.IsNotExist(err) {
				t.Fatalf("preview wrote a module into source: %v", err)
			}
		})
	}
}

func TestDeploySourceFilesWorkspaceArchiveRoot(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{
		"package.json": `{"workspaces":["apps/*"]}`, "apps/api/package.json": `{"name":"api"}`,
		"apps/api/server.js": "api", "packages/shared/index.js": "shared",
	})
	initSourceFilesGit(t, dir)
	member := filepath.Join(dir, "apps", "api")
	t.Chdir(member)
	for _, source := range []string{"head", "worktree"} {
		report, archive, err := main.ExplainDeploySourceForTest(t, member, source)
		if err != nil {
			t.Fatal(err)
		}
		assertSourceFileArchive(t, report, archive)
		rows := sourceFileRows(report)
		if report.ContextRoot != dir || report.SourceRoot != "apps/api" || !rows["packages/shared/index.js"].Included || rows["apps/api/server.js"].ArchivePath != "apps/api/server.js" {
			t.Fatalf("workspace payload lost its shared context or flat paths: %v", report)
		}
	}
}

func TestDeploySourceFilesAutoFollowsOriginPolicy(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{"package.json": "{}", "server.js": "committed"})
	initSourceFilesGit(t, dir)
	writeSourceFiles(t, dir, map[string]string{"server.js": "dirty"})
	local, _, err := main.ExplainDeploySourceForTest(t, dir, "auto")
	if err != nil || local.SourceMode != "worktree" {
		t.Fatalf("repository without origin should use local files: %v, %v", local, err)
	}
	cmd := exec.CommandContext(t.Context(), "git", "remote", "add", "origin", "https://github.com/example/source-files.git")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add local origin metadata: %v: %s", err, out)
	}
	head, _, err := main.ExplainDeploySourceForTest(t, dir, "auto")
	if err != nil || head.SourceMode != "head" || sourceFileRows(head)["server.js"].Bytes != int64(len("committed")) {
		t.Fatalf("repository with origin should use committed bytes: %v, %v", head, err)
	}
}

func runDeployFilesCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &stdout, &stderr
	defer func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr }()
	code := main.Run(append([]string{"deploy"}, args...))
	return code, stdout.String(), stderr.String()
}

func TestDeploySourceFilesCLIWithoutRemoteCallsOrValues(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{"package.json": "{}", "server.js": "do-not-display-source-body", "private.secrets": "TOKEN=do-not-display-value\n"})
	for _, jsonMode := range []bool{false, true} {
		args := []string{"--dry-run", "--explain-files", "--path", dir, "--secrets-file", filepath.Join(dir, "private.secrets")}
		if jsonMode {
			args = append(args, "--json")
		}
		code, stdout, stderr := runDeployFilesCLI(t, args...)
		if code != 0 {
			t.Fatalf("file inspection failed: %d: %s", code, stderr)
		}
		if strings.Contains(stdout+stderr, "do-not-display") {
			t.Fatal("explanation printed file values")
		}
		if jsonMode {
			var report main.DeploySourceFilesReport
			if err := json.Unmarshal([]byte(stdout), &report); err != nil {
				t.Fatal(err)
			}
			row := sourceFileRows(&report)["private.secrets"]
			if row.Included || row.Reason != "command-scoped exclusion" || report.AccountLimitChecked {
				t.Fatalf("exclusion/inspection contract wrong: %s", stdout)
			}
		} else if !strings.Contains(stdout, "exclude") || !strings.Contains(stdout, "private.secrets") {
			t.Fatalf("missing text explanation: %s", stdout)
		}
	}
	for _, args := range [][]string{{"--explain-files"}, {"--dry-run", "--explain-files", "--image", "x"}, {"--dry-run", "--explain-files", "--yes"}, {"--dry-run", "--explain-files", "--source", "head", "--worktree"}} {
		if code, _, _ := runDeployFilesCLI(t, args...); code == 0 {
			t.Fatalf("ambiguous or unsupported flags accepted: %v", args)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("local payload inspection made %d API calls", calls.Load())
	}
}

func TestDeploySourceFilesRedactedSizes(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{"package.json": "{}", ".env": "STRIPE_SECRET_KEY=sk_live_" + strings.Repeat("a", 28) + "\n"})
	report, archive, err := main.ExplainDeploySourceForTest(t, dir, "worktree")
	if err != nil {
		t.Fatal(err)
	}
	assertSourceFileArchive(t, report, archive)
	if row := sourceFileRows(report)[".env"]; !row.Redacted || !row.CredentialLike {
		t.Fatalf("replacement not identified: %v", row)
	}
}
