package renderer

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

func loadProductionTemplate(t *testing.T, name string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Load(filepath.Join("..", "..", "deploy", "manifest", "production", name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return m
}

// The OVH template (docs/runbooks/GcpToOvhMigration.md, "The OVH manifest")
// is a copy of the GCP one that changes only the hosts, the network, the log
// archive and the app entry. A release ships the same code to either fleet,
// so the copied sections must not drift apart: bumping a kernel, base image
// or switch in one template and not the other would make the move itself
// change the platform.
func TestOVHProductionManifestKeepsTheGCPReleaseAndSwitches(t *testing.T) {
	gcp := loadProductionTemplate(t, "gcp-live.template.yaml")
	ovh := loadProductionTemplate(t, "ovh-live.template.yaml")

	for name, pair := range map[string][2]any{
		"release":         {gcp.Release, ovh.Release},
		"pki":             {gcp.PKI, ovh.PKI},
		"hot_path":        {gcp.HotPath, ovh.HotPath},
		"dns":             {gcp.DNS, ovh.DNS},
		"storage":         {gcp.Storage, ovh.Storage},
		"cgroups":         {gcp.Cgroups, ovh.Cgroups},
		"artifact_signer": {gcp.ArtifactSigner, ovh.ArtifactSigner},
		// ControlPlaneFailover.md step 4 moves the control plane by changing
		// only its name, so its address and the DSN stay the same.
		"postgresql.dsn": {gcp.PostgreSQL.DSN, ovh.PostgreSQL.DSN},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("%s differs between gcp-live and ovh-live templates; change both", name)
		}
	}
	if got, want := controlPlaneAddress(t, ovh), controlPlaneAddress(t, gcp); got != want {
		t.Errorf("OVH control plane address = %q, want the GCP one %q (the switchover keeps it)", got, want)
	}
}

// Bare metal has no provider firewall or single private card
// (ADR-20261109100000), and NVMe device names change across reboots on the
// OVH servers, so every host names its private card and pins its data disk.
func TestOVHProductionManifestPinsBareMetalNetworkAndDisks(t *testing.T) {
	m := loadProductionTemplate(t, "ovh-live.template.yaml")
	if m.Overlay.Provider != "static" || m.Overlay.Interface == "" {
		t.Fatalf("overlay = %+v, want the static provider with the vRack interface named", m.Overlay)
	}
	if m.DNS.Mode == "cloudflare" && m.DNS.OriginPull == "" {
		t.Fatal("dns.mode cloudflare without dns.origin_pull; the control-plane deploy would fail")
	}
	for _, h := range m.Fleet.Hosts {
		if !strings.HasPrefix(h.StorageDevice, "/dev/disk/by-id/") {
			t.Errorf("host %s storage_device = %q, want a /dev/disk/by-id path", h.Name, h.StorageDevice)
		}
		if h.PublicIPv4Forwarded {
			t.Errorf("host %s sets public_ipv4_forwarded, a GCP forwarding-rule flag", h.Name)
		}
	}
}

func controlPlaneAddress(t *testing.T, m *manifest.Manifest) string {
	t.Helper()
	for _, h := range m.Fleet.Hosts {
		if h.Role == "control-plane" {
			return h.Address
		}
	}
	t.Fatal("manifest has no control-plane host")
	return ""
}
