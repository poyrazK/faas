package renderer

// adr: 656
import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/manifest"
)

func TestGlobalIngressPrivateConfigRendered(t *testing.T) {
	dc := &manifest.DaemonConfig{GlobalIngressDirectoryID: uuid.NewString(), GlobalIngressAppsDomain: "global.example.com", GlobalIngressManifestPath: "/run/faas/global-routes.json", GlobalIngressTrustBundlePath: "/etc/faas/global-trust.json"}
	body, flat, err := renderTOML(tomlRenderCtx{Daemon: "gatewayd_internal", DC: dc})
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if _, err := toml.Decode(string(body), &values); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"global_ingress_directory_id": dc.GlobalIngressDirectoryID, "global_ingress_apps_domain": dc.GlobalIngressAppsDomain, "global_ingress_manifest_path": dc.GlobalIngressManifestPath, "global_ingress_trust_bundle_path": dc.GlobalIngressTrustBundlePath} {
		if values[key] != value {
			t.Fatalf("%s missing: %s", key, body)
		}
	}
	if errs := manifest.ValidateTOMLPlacement("gatewayd_internal", flat); len(errs) > 0 {
		t.Fatal(errs)
	}
}
