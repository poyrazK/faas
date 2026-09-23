package apihostingcontract

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

func TestCatalogProfiles(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Fixtures) < 15 {
		t.Fatalf("fixture count = %d, want at least 15", len(catalog.Fixtures))
	}
	tags := map[string]bool{}
	quickRuntime := 0
	for _, fixture := range catalog.Fixtures {
		fixture := fixture
		hasRuntime, hasQuick := false, false
		for _, tag := range fixture.Tags {
			tags[tag] = true
			hasRuntime = hasRuntime || tag == "runtime"
			hasQuick = hasQuick || tag == "quick"
		}
		if hasRuntime && hasQuick {
			quickRuntime++
		}
		t.Run(fixture.ID, func(t *testing.T) {
			files := make(fstest.MapFS, len(fixture.Files))
			for path, body := range fixture.Files {
				files[path] = &fstest.MapFile{Data: []byte(body)}
			}
			selected := fs.FS(files)
			if fixture.SourceRoot != "" {
				selected, err = fs.Sub(files, fixture.SourceRoot)
				if err != nil {
					t.Fatalf("select source root %q: %v", fixture.SourceRoot, err)
				}
			}
			got, err := frameworkprofile.Analyze(selected)
			if err != nil {
				t.Fatal(err)
			}
			want := fixture.Expected
			if got.Framework != want.Framework || got.PackageManager != want.PackageManager || got.Port != want.Port || got.HealthPath != want.HealthPath || got.StartCommand != want.StartCommand || got.ConfigFile != want.ConfigFile || got.Inferred != want.Inferred {
				t.Fatalf("profile = %+v, want framework=%q package_manager=%q port=%d health=%q command=%q config=%q inferred=%t", got, want.Framework, want.PackageManager, want.Port, want.HealthPath, want.StartCommand, want.ConfigFile, want.Inferred)
			}
		})
	}
	for _, tag := range []string{"oci", "sse", "workspace", "runtime-candidate"} {
		if !tags[tag] {
			t.Errorf("catalog has no %q fixture", tag)
		}
	}
	if quickRuntime < 3 {
		t.Fatalf("catalog has %d quick runtime fixtures, want at least 3", quickRuntime)
	}
}

func TestWorkspaceFixturesRequireRealRepositoryContext(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtureByID(t, catalog, "pnpm-workspace")
	if err := Validate(Catalog{Version: 1, Fixtures: []Fixture{fixture}}); err != nil {
		t.Fatalf("valid nested workspace fixture rejected: %v", err)
	}

	withoutSourceRoot := fixture
	withoutSourceRoot.SourceRoot = ""
	if err := Validate(Catalog{Version: 1, Fixtures: []Fixture{withoutSourceRoot}}); err == nil || !strings.Contains(err.Error(), "source_root") {
		t.Fatalf("workspace without selected member error = %v, want source_root error", err)
	}

	withoutSibling := fixture
	withoutSibling.Files = make(map[string]string, len(fixture.Files))
	for name, body := range fixture.Files {
		if name != "packages/shared/package.json" {
			withoutSibling.Files[name] = body
		}
	}
	if err := Validate(Catalog{Version: 1, Fixtures: []Fixture{withoutSibling}}); err == nil || !strings.Contains(err.Error(), "sibling project manifest") {
		t.Fatalf("workspace without sibling project error = %v, want sibling-manifest error", err)
	}
}

func TestRuntimeCatalogModesKeepCandidatesOptIn(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	quick, err := SelectRuntimeFixtures(catalog, "")
	if err != nil {
		t.Fatal(err)
	}
	quickExplicit, err := SelectRuntimeFixtures(catalog, "quick")
	if err != nil {
		t.Fatal(err)
	}
	if !sameFixtureIDs(quick, quickExplicit) {
		t.Fatal("default catalog mode differs from quick mode")
	}

	full, err := SelectRuntimeFixtures(catalog, "full")
	if err != nil {
		t.Fatal(err)
	}
	qualify, err := SelectRuntimeFixtures(catalog, "qualify")
	if err != nil {
		t.Fatal(err)
	}
	fullIDs := fixtureIDs(full)
	qualifyIDs := fixtureIDs(qualify)
	for _, candidate := range []string{"django", "fastapi-src-layout"} {
		if slices.Contains(fullIDs, candidate) {
			t.Errorf("full mode unexpectedly selected candidate %q", candidate)
		}
		if !slices.Contains(qualifyIDs, candidate) {
			t.Errorf("qualify mode did not select candidate %q", candidate)
		}
	}
	for _, id := range fullIDs {
		if !slices.Contains(qualifyIDs, id) {
			t.Errorf("qualify mode omitted supported runtime fixture %q", id)
		}
	}
	if _, err := SelectRuntimeFixtures(catalog, "invalid"); err == nil {
		t.Fatal("unknown runtime catalog mode was accepted")
	}
}

func fixtureIDs(fixtures []Fixture) []string {
	ids := make([]string, len(fixtures))
	for i, fixture := range fixtures {
		ids[i] = fixture.ID
	}
	return ids
}

func sameFixtureIDs(a, b []Fixture) bool {
	return slices.Equal(fixtureIDs(a), fixtureIDs(b))
}

func TestDjangoFixtureHasRunnableWSGIProject(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtureByID(t, catalog, "django")
	for name, want := range map[string]string{
		"manage.py":       "execute_from_command_line",
		"app/wsgi.py":     "get_wsgi_application",
		"app/settings.py": "WSGI_APPLICATION",
		"app/urls.py":     "healthz",
	} {
		if !strings.Contains(fixture.Files[name], want) {
			t.Errorf("Django fixture %s does not contain %q", name, want)
		}
	}
	if !hasTag(fixture.Tags, "runtime-candidate") || hasTag(fixture.Tags, "runtime") {
		t.Errorf("Django must remain a runtime candidate until reference-node acceptance passes; tags=%v", fixture.Tags)
	}
}

func TestNestJSFixtureUsesNestRuntime(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtureByID(t, catalog, "nestjs")
	if _, exists := fixture.Files["server.js"]; exists {
		t.Fatal("NestJS fixture must not use a generic Node HTTP server")
	}
	for name, want := range map[string]string{
		"src/main.ts":           "NestFactory.create(AppModule)",
		"src/app.module.ts":     "@Module(",
		"src/app.controller.ts": "@Controller(",
	} {
		if !strings.Contains(fixture.Files[name], want) {
			t.Errorf("NestJS fixture %s does not contain %q", name, want)
		}
	}
	for _, want := range []string{`"build":"nest build"`, `"@nestjs/platform-express"`} {
		if !strings.Contains(fixture.Files["package.json"], want) {
			t.Errorf("NestJS package.json does not contain %q", want)
		}
	}
}

func fixtureByID(t *testing.T, catalog Catalog, id string) Fixture {
	t.Helper()
	for _, fixture := range catalog.Fixtures {
		if fixture.ID == id {
			return fixture
		}
	}
	t.Fatalf("catalog has no fixture %q", id)
	return Fixture{}
}

func TestContainerRuntimeContract(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	ociFixtures := 0
	for _, fixture := range catalog.Fixtures {
		if !hasTag(fixture.Tags, "oci") {
			continue
		}
		ociFixtures++
		if err := ValidateContainerContract(fixture); err != nil {
			t.Errorf("%s: %v", fixture.ID, err)
		}
	}
	if ociFixtures == 0 {
		t.Fatal("catalog has no OCI container fixtures")
	}
}
