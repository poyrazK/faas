package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestManifestDeclaredBindings(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		slug     string
		want     []string // environment keys
	}{
		{name: "no manifest", slug: "api"},
		{name: "entry naming the app", slug: "api", want: []string{"DATABASE_URL"},
			manifest: "databases:\n  - database: orders\n    app: api\n  - database: billing\n    app: billing\n"},
		{name: "app-less entry in another app's directory is ignored", slug: "api",
			manifest: "project:\n  app: web\ndatabases:\n  - database: orders\n"},
		{name: "app-less entry in the app's own directory", slug: "api", want: []string{"ORDERS_URL"},
			manifest: "project:\n  app: api\ndatabases:\n  - database: orders\n    env: ORDERS_URL\n    scope: production\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Stop the project-context walk at this directory.
			if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.manifest != "" {
				main.WriteManifest(t, dir, tc.manifest)
			}
			got, err := main.ManifestDeclaredBindings(dir, tc.slug)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("declared = %+v, want keys %v", got, tc.want)
			}
			for i, key := range tc.want {
				if got[i].Binding != key || got[i].Type != "postgres" {
					t.Fatalf("declared[%d] = %+v, want %s", i, got[i], key)
				}
			}
		})
	}
}
