package globalingress

// adr: 784
import (
	"testing"

	"github.com/google/uuid"
)

func TestConfigIsCoherentAndDomainsAreDisjoint(t *testing.T) {
	valid := Config{DirectoryID: uuid.NewString(), AppsDomain: "global.example.com", ManifestPath: "/run/faas/global-routes.json", TrustBundlePath: "/etc/faas/global-trust.json"}
	if err := (Config{}).Validate("local.example.com"); err != nil {
		t.Fatal("disabled ingress rejected", err)
	}
	if err := valid.Validate("local.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
		domain string
	}{
		{"missing identity", func(c *Config) { c.DirectoryID = "" }, "local.example.com"},
		{"missing manifest", func(c *Config) { c.ManifestPath = "" }, "local.example.com"},
		{"relative trust", func(c *Config) { c.TrustBundlePath = "trust.json" }, "local.example.com"},
		{"same input paths", func(c *Config) { c.TrustBundlePath = c.ManifestPath }, "local.example.com"},
		{"noncanonical domain", func(c *Config) { c.AppsDomain = "Global.example.com" }, "local.example.com"},
		{"IP domain", func(c *Config) { c.AppsDomain = "192.0.2.1" }, "local.example.com"},
		{"same domain", func(*Config) {}, "global.example.com"},
		{"global nested in local", func(*Config) {}, "example.com"},
		{"local nested in global", func(*Config) {}, "local.global.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.change(&c)
			if err := c.Validate(tc.domain); err == nil {
				t.Fatal("invalid ingress config accepted")
			}
		})
	}
}
