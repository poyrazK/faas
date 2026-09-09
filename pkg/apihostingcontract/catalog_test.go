package apihostingcontract

import (
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
	for _, fixture := range catalog.Fixtures {
		fixture := fixture
		for _, tag := range fixture.Tags {
			tags[tag] = true
		}
		t.Run(fixture.ID, func(t *testing.T) {
			files := make(fstest.MapFS, len(fixture.Files))
			for path, body := range fixture.Files {
				files[path] = &fstest.MapFile{Data: []byte(body)}
			}
			got, err := frameworkprofile.Analyze(files)
			if err != nil {
				t.Fatal(err)
			}
			want := fixture.Expected
			if got.Framework != want.Framework || got.PackageManager != want.PackageManager || got.Port != want.Port || got.HealthPath != want.HealthPath || got.StartCommand != want.StartCommand || got.ConfigFile != want.ConfigFile || got.Inferred != want.Inferred {
				t.Fatalf("profile = %+v, want framework=%q package_manager=%q port=%d health=%q command=%q config=%q inferred=%t", got, want.Framework, want.PackageManager, want.Port, want.HealthPath, want.StartCommand, want.ConfigFile, want.Inferred)
			}
		})
	}
	for _, tag := range []string{"oci", "sse", "workspace"} {
		if !tags[tag] {
			t.Errorf("catalog has no %q fixture", tag)
		}
	}
}
