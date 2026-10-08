// adr: 570
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

func TestTrafficAppsDomainWriterConfig(t *testing.T) {
	for _, fixture := range []struct{ name, document, configured string }{
		{"default", "", hostidentity.DefaultAppsDomain},
		{"custom", `apps_domain = "apps.example.test"`, "apps.example.test"},
		{"disabled", `apps_domain = ""`, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "writer.toml")
			if err := os.WriteFile(path, []byte(fixture.document), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, overlay := range []string{"", "override.example.test"} {
				want := fixture.configured
				if overlay != "" {
					want = overlay
				}
				got := cfg.GetAppsDomain(func(key string) string {
					if key != "FAAS_APPS_DOMAIN" {
						t.Fatalf("unexpected config input %s", key)
					}
					return overlay
				})
				if got != want {
					t.Fatalf("domain=%q want=%q", got, want)
				}
			}
		})
	}
}
