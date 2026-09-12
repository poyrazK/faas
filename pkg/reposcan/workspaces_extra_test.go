package reposcan

import (
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
		"apps/api/package.json":  &fstest.MapFile{Data: []byte(`{}`)},
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
		"frontend/package.json": &fstest.MapFile{Data: []byte(`{}`)},
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
