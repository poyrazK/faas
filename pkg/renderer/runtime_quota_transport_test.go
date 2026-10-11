package renderer

// adr: 793
import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/manifest"
)

func TestManagedRuntimeQuotaTransportRoundTrip(t *testing.T) {
	for name, input := range map[string]*manifest.DaemonConfig{
		"disabled":     {},
		"empty tables": {RuntimeQuotaTransport: &manifest.RuntimeQuotaTransportConfig{}, GlobalIngressPublisher: &manifest.GlobalIngressPublisherConfig{}},
		"directory":    {RuntimeQuotaTransport: &manifest.RuntimeQuotaTransportConfig{DirectoryIssuerID: uuid.NewString(), PrivateKeyPath: "/etc/faas/signing.key", TrustBundlePath: "/etc/faas/trust.json", DeliveryWorker: true}, GlobalIngressPublisher: &manifest.GlobalIngressPublisherConfig{ManifestPath: "/run/faas/global-ingress/routes.json"}},
		"receiver":     {RuntimeQuotaTransport: &manifest.RuntimeQuotaTransportConfig{RegionalAPIBase: "https://regional.example.com", TrustBundlePath: "/etc/faas/regional-trust.json"}},
	} {
		t.Run(name, func(t *testing.T) {
			body, flat, err := renderTOML(tomlRenderCtx{Daemon: "apid", DC: input})
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Transport struct {
					DirectoryIssuerID string `toml:"directory_issuer_id"`
					PrivateKeyPath    string `toml:"private_key_path"`
					TrustBundlePath   string `toml:"trust_bundle_path"`
					RegionalAPIBase   string `toml:"regional_api_base"`
					DeliveryWorker    bool   `toml:"delivery_worker"`
				} `toml:"runtime_quota_transport"`
				Publisher struct {
					ManifestPath string `toml:"manifest_path"`
				} `toml:"global_ingress_publisher"`
			}
			if _, err := toml.Decode(string(body), &decoded); err != nil {
				t.Fatal(err)
			}
			got := manifest.RuntimeQuotaTransportConfig{DirectoryIssuerID: decoded.Transport.DirectoryIssuerID, PrivateKeyPath: decoded.Transport.PrivateKeyPath, TrustBundlePath: decoded.Transport.TrustBundlePath, RegionalAPIBase: decoded.Transport.RegionalAPIBase, DeliveryWorker: decoded.Transport.DeliveryWorker}
			want := manifest.RuntimeQuotaTransportConfig{}
			if input.RuntimeQuotaTransport != nil {
				want = *input.RuntimeQuotaTransport
			}
			if got != want {
				t.Fatalf("transport lost on managed render: got %+v, want %+v", got, want)
			}
			publisher := ""
			if input.GlobalIngressPublisher != nil {
				publisher = input.GlobalIngressPublisher.ManifestPath
			}
			if decoded.Publisher.ManifestPath != publisher {
				t.Fatal("publisher lost on managed render", string(body))
			}
			if errs := manifest.ValidateTOMLPlacement("apid", flat); errs != nil {
				t.Fatal(errs)
			}
		})
	}
}
