package apid

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPublicOpenAPIIdentity is issue #2704: the served reference (and every
// SDK generated from it) presents Gregale, not the internal predecessor, and
// carries no loopback endpoint or internal license reference.
func TestPublicOpenAPIIdentity(t *testing.T) {
	for name, raw := range map[string][]byte{
		"embedded": OpenAPIYAML(),
		"public":   PublicOpenAPIYAMLFor(allVisible),
	} {
		t.Run(name, func(t *testing.T) {
			var doc struct {
				Info struct {
					Title   string `yaml:"title"`
					Summary string `yaml:"summary"`
					Contact struct {
						Name string `yaml:"name"`
					} `yaml:"contact"`
					License struct {
						Identifier string `yaml:"identifier"`
					} `yaml:"license"`
				} `yaml:"info"`
				Servers []struct {
					Description string `yaml:"description"`
				} `yaml:"servers"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("decode spec: %v", err)
			}
			if doc.Info.Title != "Gregale API" || doc.Info.Contact.Name != "Gregale" {
				t.Errorf("info title %q contact %q, want Gregale", doc.Info.Title, doc.Info.Contact.Name)
			}
			if !strings.HasPrefix(doc.Info.License.Identifier, "LicenseRef-Gregale") {
				t.Errorf("license identifier %q, want a Gregale reference", doc.Info.License.Identifier)
			}
			text := strings.ToLower(string(raw))
			for _, banned := range []string{"one-box faas", "onebox-internal", "127.0.0.1:8081", "/v1/webhooks/stripe"} {
				if strings.Contains(text, banned) {
					t.Errorf("spec contains %q", banned)
				}
			}
		})
	}
}
