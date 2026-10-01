package renderer

import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/manifest"
)

func TestComputeRegionRenderedPerHost(t *testing.T) {
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		body, _, err := renderTOML(tomlRenderCtx{Daemon: "vmmd", DC: fixtureTOML("vmmd"), HostName: "worker", HostRegion: region, HostZone: region + "-a"})
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Node struct{ Region, Zone string } `toml:"compute_node"`
		}
		if _, err := toml.Decode(string(body), &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Node.Region != region || cfg.Node.Zone != region+"-a" {
			t.Fatalf("host region/zone = %+v", cfg.Node)
		}
	}
}

func TestRegionalComputeOperatorGateRenderedAsBoolean(t *testing.T) {
	for _, daemon := range []string{"apid", "githubd"} {
		for _, enabled := range []bool{false, true} {
			body, _, err := renderTOML(tomlRenderCtx{Daemon: daemon, DC: &manifest.DaemonConfig{RegionalComputeEnabled: &enabled}})
			if err != nil {
				t.Fatal(err)
			}
			var cfg struct {
				Enabled bool `toml:"regional_compute_enabled"`
			}
			if _, err := toml.Decode(string(body), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Enabled != enabled {
				t.Fatalf("gate = %v, want %v", cfg.Enabled, enabled)
			}
		}
	}
}
