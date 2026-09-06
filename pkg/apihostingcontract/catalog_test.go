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
	if len(catalog.Fixtures) < 9 {
		t.Fatalf("fixture count = %d, want at least 9", len(catalog.Fixtures))
	}
	for _, fixture := range catalog.Fixtures {
		fixture := fixture
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
			if got.Framework != want.Framework || got.Port != want.Port || got.StartCommand != want.StartCommand || got.Inferred != want.Inferred {
				t.Fatalf("profile = %+v, want framework=%q port=%d command=%q inferred=%t", got, want.Framework, want.Port, want.StartCommand, want.Inferred)
			}
		})
	}
}
