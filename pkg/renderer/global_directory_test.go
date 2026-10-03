package renderer

import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/manifest"
)

func TestGlobalDirectoryRenderedGate(t *testing.T) {
	on := true
	for _, enabled := range []*bool{nil, new(bool), &on} {
		body, _, err := renderTOML(tomlRenderCtx{Daemon: "apid", DC: &manifest.DaemonConfig{GlobalAppDirectoryEnabled: enabled}})
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Enabled bool `toml:"global_app_directory_enabled"`
		}
		if _, err := toml.Decode(string(body), &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Enabled != (enabled != nil && *enabled) {
			t.Fatalf("gate: %s", body)
		}
	}
}
