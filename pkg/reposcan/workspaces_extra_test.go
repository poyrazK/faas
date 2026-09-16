package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestParseWorkspacesField_ArrayForm — package.json's workspaces
// field with a flat string array is the legacy npm form.
func TestParseWorkspacesField_ArrayForm(t *testing.T) {
	t.Parallel()
	got := parseWorkspacesField([]byte(`["packages/a", "packages/b"]`))
	want := []string{"packages/a", "packages/b"}
	if !equalSet(got, want) {
		t.Errorf("parseWorkspacesField = %v, want %v", got, want)
	}
}

// TestParseWorkspacesField_ObjectForm — yarn/pnpm v3+ write
// workspaces as an object { packages: […] }. The helper must
// unwrap to the inner packages array.
func TestParseWorkspacesField_ObjectForm(t *testing.T) {
	t.Parallel()
	got := parseWorkspacesField([]byte(`{"packages": ["a", "b"], "nohoist": ["c"]}`))
	want := []string{"a", "b"}
	if !equalSet(got, want) {
		t.Errorf("parseWorkspacesField (object) = %v, want %v", got, want)
	}
}

// TestParseWorkspacesField_Empty — empty or null input is a quiet skip.
func TestParseWorkspacesField_Empty(t *testing.T) {
	t.Parallel()
	if got := parseWorkspacesField(nil); got != nil {
		t.Errorf("parseWorkspacesField(nil) = %v, want nil", got)
	}
}

// TestParseGoWorkUses_Basic — the canonical go.work "use ( … )"
// block. Each ./ prefix is stripped.
func TestParseGoWorkUses_Basic(t *testing.T) {
	t.Parallel()
	body := `go 1.23

use (
	./services/api
	./services/worker
)
`
	got := parseGoWorkUses(body)
	want := []string{"services/api", "services/worker"}
	if !equalSet(got, want) {
		t.Errorf("parseGoWorkUses = %v, want %v", got, want)
	}
}

// TestParseGoWorkUses_SingleLine — direct use directives are equivalent to
// entries in a parenthesized use block and may be repeated.
func TestParseGoWorkUses_SingleLine(t *testing.T) {
	t.Parallel()
	body := `go 1.23

use ./services/api
use ./services/worker // keep this module
`
	got := parseGoWorkUses(body)
	want := []string{"services/api", "services/worker"}
	if !equalSet(got, want) {
		t.Errorf("parseGoWorkUses (single-line) = %v, want %v", got, want)
	}
}

// TestParseGoWorkUses_IgnoresDirectivesAfterBlock — closing a use block must
// stop module collection; later toolchain and replace directives are separate
// workspace directives, not module paths.
func TestParseGoWorkUses_IgnoresDirectivesAfterBlock(t *testing.T) {
	t.Parallel()
	body := `go 1.23

use (
	./services/api
)
toolchain go1.24.1
replace example.com/old => ./replacement
`
	got := parseGoWorkUses(body)
	want := []string{"services/api"}
	if !equalSet(got, want) {
		t.Errorf("parseGoWorkUses (post-block directives) = %v, want %v", got, want)
	}
}

// TestParseGoWorkUses_IgnoresComments — line comments inside
// the use block are skipped.
func TestParseGoWorkUses_IgnoresComments(t *testing.T) {
	t.Parallel()
	body := `go 1.23

use (
	./services/api
	// ./services/dead
	./services/worker
)
`
	got := parseGoWorkUses(body)
	want := []string{"services/api", "services/worker"}
	if !equalSet(got, want) {
		t.Errorf("parseGoWorkUses (comments) = %v, want %v", got, want)
	}
}

// TestParseGoWorkUses_Empty — body without a use block returns nil.
func TestParseGoWorkUses_Empty(t *testing.T) {
	t.Parallel()
	got := parseGoWorkUses("go 1.23\n")
	if len(got) != 0 {
		t.Errorf("parseGoWorkUses (no use block) = %v, want []", got)
	}
}

// TestRangeLines — newline-splitting handles \r\n (Windows CRLF)
// and trailing newlines.
func TestRangeLines(t *testing.T) {
	t.Parallel()
	got := rangeLines("a\r\nb\r\n")
	want := []string{"a", "b", ""}
	if len(got) != len(want) {
		t.Errorf("rangeLines(crlf) len = %d, want %d", len(got), len(want))
	}
}

// TestDetectWorkspaces_PackageJSONObjectForm — a package.json
// with the object-form workspaces field is read; members
// lacking markers are skipped.
func TestDetectWorkspaces_PackageJSONObjectForm(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"package.json": &fstest.MapFile{Data: []byte(`{
  "name": "monorepo",
  "workspaces": {
    "packages": ["apps/web", "apps/api", "packages/lib"]
  }
}`)},
		"apps/web/Dockerfile":    &fstest.MapFile{Data: []byte("FROM scratch")},
		"apps/api/package.json":  &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node server.js"}}`)},
		"packages/lib/README.md": &fstest.MapFile{Data: []byte("# lib\n")},
	}
	seeds, _, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatalf("detectWorkspaces: %v", err)
	}
	names := make([]string, len(seeds))
	for i, s := range seeds {
		names[i] = s.name
	}
	if !equalSet(names, []string{"web", "api"}) {
		t.Errorf("object-form workspaces = %v, want {web, api}", names)
	}
}

// TestDetectWorkspaces_GoWork — a go.work file's use block
// is parsed; each module is treated as a workspace member if
// it carries a Dockerfile.
func TestDetectWorkspaces_GoWork(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"go.work": &fstest.MapFile{Data: []byte(`go 1.23

use (
	./services/api
	./services/worker
)
`)},
		"services/api/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")},
	}
	seeds, _, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatalf("detectWorkspaces: %v", err)
	}
	names := make([]string, len(seeds))
	for i, s := range seeds {
		names[i] = s.name
	}
	if !equalSet(names, []string{"api"}) {
		t.Errorf("go.work = %v, want {api}", names)
	}
}

func TestDetectWorkspaces_CargoMembersAndExcludes(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"Cargo.toml": &fstest.MapFile{Data: []byte(`[workspace]
members = ["crates/*", "tools/runner"]
exclude = ["crates/internal"]
resolver = "2"
`)},
		"crates/api/Cargo.toml":      &fstest.MapFile{Data: []byte("[package]\nname = \"api\"\n")},
		"crates/internal/Cargo.toml": &fstest.MapFile{Data: []byte("[package]\nname = \"internal\"\n")},
		"crates/readme/README.md":    &fstest.MapFile{Data: []byte("# docs\n")},
		"tools/runner/Cargo.toml":    &fstest.MapFile{Data: []byte("[package]\nname = \"runner\"\n")},
	}

	seeds, warnings, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatalf("detectWorkspaces: %v", err)
	}
	if got := names(seeds); !equalSet(got, []string{"api", "runner"}) {
		t.Fatalf("Cargo workspace members = %v, want {api, runner}", got)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	for _, seed := range seeds {
		if seed.source != "Cargo.toml: "+seed.rootDir {
			t.Fatalf("seed source = %q for root %q", seed.source, seed.rootDir)
		}
	}
}

func TestDetectWorkspacesUsesDeclaredPackageNamesForRepeatedBasenames(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"pnpm-workspace.yaml":       &fstest.MapFile{Data: []byte("packages:\n  - frontend/api\n  - backend/api\n")},
		"frontend/api/package.json": &fstest.MapFile{Data: []byte(`{"name":"frontend-api","scripts":{"start":"node server.js"}}`)},
		"backend/api/package.json":  &fstest.MapFile{Data: []byte(`{"name":"backend-api","scripts":{"start":"node server.js"}}`)},
	}
	result, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{result.Workloads[0].Name, result.Workloads[1].Name}; !equalSet(got, []string{"frontend-api", "backend-api"}) {
		t.Fatalf("workspace names = %v, want declared package identities", got)
	}
	if result.Workloads[0].RootDir == result.Workloads[1].RootDir {
		t.Fatalf("workspace roots collapsed: %#v", result.Workloads)
	}
}

func TestDetectWorkspacesNormalizesScopedPackageName(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"package.json":              &fstest.MapFile{Data: []byte(`{"workspaces":["packages/api"]}`)},
		"packages/api/package.json": &fstest.MapFile{Data: []byte(`{"name":"@acme/api","scripts":{"start":"node index.js"}}`)},
	}
	result, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Workloads) != 1 || result.Workloads[0].Name != "acme-api" {
		t.Fatalf("scoped package workload = %#v, want acme-api", result.Workloads)
	}
}

func TestDetectWorkspacesCurrentNxProjectJSON(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"nx.json":      &fstest.MapFile{Data: []byte(`{"defaultBase":"main"}`)},
		"package.json": &fstest.MapFile{Data: []byte(`{"private":true}`)},
		"modules/api/project.json": &fstest.MapFile{Data: []byte(`{
  "name":"api",
  "root":"modules/api",
  "projectType":"application",
  "targets":{"serve":{"command":"node index.js"}}
}`)},
		"modules/api/package.json": &fstest.MapFile{Data: []byte(`{"name":"nx-api","private":true}`)},
		"modules/api/index.js":     &fstest.MapFile{Data: []byte("console.log('ready')\n")},
	}
	result, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Workloads) != 1 {
		t.Fatalf("Nx workloads = %#v, want one", result.Workloads)
	}
	workload := result.Workloads[0]
	if workload.Name != "api" || workload.RootDir != "modules/api" || !workload.CommandShell ||
		len(workload.Command) != 1 || workload.Command[0] != "node index.js" || workload.Class != ClassHTTP {
		t.Fatalf("Nx workload = %#v", workload)
	}
	if result.Tier != TierWorkspace || workload.Source == "root-floor" {
		t.Fatalf("Nx detection fell back to root: tier=%v workload=%#v", result.Tier, workload)
	}
}

func TestDetectWorkspacesPackageLevelNxTarget(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"nx.json": &fstest.MapFile{Data: []byte(`{"defaultBase":"main"}`)},
		"services/jobs/package.json": &fstest.MapFile{Data: []byte(`{
  "name":"jobs-worker",
  "nx":{"targets":{"worker":{"options":{"command":"node worker.js"}}}}
}`)},
		"services/jobs/worker.js": &fstest.MapFile{Data: []byte("console.log('work')\n")},
	}
	result, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Workloads) != 1 || result.Workloads[0].Name != "jobs-worker" || result.Workloads[0].Class != ClassWorker {
		t.Fatalf("package-level Nx workload = %#v", result.Workloads)
	}
}

func TestScan_CargoWorkspaceDoesNotFallBackToRoot(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"Cargo.toml": &fstest.MapFile{Data: []byte(`[workspace]
members = ["crates/api"]
resolver = "2"
`)},
		"crates/api/Cargo.toml":  &fstest.MapFile{Data: []byte("[package]\nname = \"workspace-api\"\n")},
		"crates/api/src/main.rs": &fstest.MapFile{Data: []byte("fn main() {}\n")},
	}

	result, err := Scan(fsys)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Workloads) != 1 {
		t.Fatalf("workloads = %#v, want one Cargo member", result.Workloads)
	}
	workload := result.Workloads[0]
	if workload.Name != "workspace-api" || workload.RootDir != "crates/api" || workload.Tier != TierWorkspace {
		t.Fatalf("workload = %#v, want declared Cargo package workspace-api at crates/api", workload)
	}
}

// TestDetectWorkspaces_NxProjectsMap — nx.json with the modern
// "projects" map form: keys become workload names.
func TestDetectWorkspaces_NxProjectsMap(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"nx.json": &fstest.MapFile{Data: []byte(`{
  "version": 2,
  "projects": {
    "frontend": {},
    "backend": {}
  }
}`)},
		"frontend/package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node server.js"}}`)},
	}
	seeds, _, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatalf("detectWorkspaces: %v", err)
	}
	// backend has no marker — skipped; frontend has package.json.
	if len(seeds) != 1 || seeds[0].name != "frontend" {
		t.Errorf("nx projects = %v, want [frontend]", seeds)
	}
}

// TestDetectWorkspaces_PnpmMonorepo_AlreadyCovered is the
// canonical pnpm case; lives here so a refactor of
// workspaces_test.go doesn't lose coverage of the path.
func TestDetectWorkspaces_PnpmMonorepo_AlreadyCovered(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"pnpm-workspace.yaml": &fstest.MapFile{Data: []byte(`packages:
  - "apps/*"
`)},
		"apps/web/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")},
		"apps/api/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")},
		"apps/docs/README.md": &fstest.MapFile{Data: []byte("# docs\n")},
	}
	seeds, _, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatalf("detectWorkspaces: %v", err)
	}
	names := make([]string, len(seeds))
	for i, s := range seeds {
		names[i] = s.name
	}
	if !equalSet(names, []string{"api", "web"}) {
		t.Errorf("pnpm members = %v, want {api, web}", names)
	}
}

func TestDetectWorkspaces_PnpmRecursivePrefixAndExclusion(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"pnpm-workspace.yaml": &fstest.MapFile{Data: []byte(`packages:
  - "./modules/**"
  - "!modules/internal"
`)},
		"modules/group/api/package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node server.js"}}`)},
		"modules/internal/package.json":  &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node private.js"}}`)},
		"modules/shared/package.json":    &fstest.MapFile{Data: []byte(`{"main":"index.js"}`)},
	}
	seeds, warnings, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(seeds); !equalSet(got, []string{"api"}) {
		t.Fatalf("workloads = %v, want only recursive api", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "modules/shared") {
		t.Fatalf("warnings = %v, want shared library explanation", warnings)
	}
}

func TestExpandWorkspacePatterns_GlobstarMatchesZeroOrMoreDirectories(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"api/package.json":                &fstest.MapFile{Data: []byte(`{}`)},
		"groups/backend/api/package.json": &fstest.MapFile{Data: []byte(`{}`)},
	}
	members, err := expandWorkspacePatterns(fsys, []string{"**/api"})
	if err != nil {
		t.Fatal(err)
	}
	if !equalSet(members, []string{"api", "groups/backend/api"}) {
		t.Fatalf("members = %v", members)
	}
}

// TestDetectWorkspaces_RejectsDotDotEscape — a malicious
// pnpm-workspace.yaml entry that contains `..` is rejected by
// fs.ValidPath after the path.Join normalisation. The dangerous
// value is "packages/../escape": leading ".." check passes (the
// path starts with "p"), but path.Join collapses it to "escape"
// before fs.ReadDir ever runs. The fix is an explicit
// fs.ValidPath gate around the add() body.
//
// Without the fix, this fixture would emit a workload named
// "escape" sourced from inside the (hypothetical) tarball's
// "escape/" subtree — a path the scanner is not supposed to
// reach.
func TestDetectWorkspaces_RejectsDotDotEscape(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"pnpm-workspace.yaml": &fstest.MapFile{Data: []byte(`packages:
  - "packages/legal"
  - "packages/../escape"
`)},
		"packages/legal/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")},
		"escape/Dockerfile":         &fstest.MapFile{Data: []byte("FROM scratch")},
	}
	seeds, _, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatalf("detectWorkspaces: %v", err)
	}
	names := make([]string, len(seeds))
	for i, s := range seeds {
		names[i] = s.name
	}
	if !equalSet(names, []string{"legal"}) {
		t.Errorf("dot-dot escape = %v, want only {legal} (escape must be rejected)", names)
	}
}
