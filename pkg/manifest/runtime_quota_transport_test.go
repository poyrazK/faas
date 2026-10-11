package manifest

// adr: 793
import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func directoryTransportManifest(t *testing.T) *Manifest {
	t.Helper()
	m, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	m.Daemons.Apid = &DaemonConfig{Bind: "unix:///run/faas/apid.sock", GlobalAppDirectoryEnabled: &enabled,
		RuntimeQuotaTransport:  &RuntimeQuotaTransportConfig{DirectoryIssuerID: uuid.NewString(), PrivateKeyPath: "/etc/faas/directory/signing.key", TrustBundlePath: "/etc/faas/directory/trust.json", DeliveryWorker: true},
		GlobalIngressPublisher: &GlobalIngressPublisherConfig{ManifestPath: "/run/faas/global-ingress/routes.json"}}
	return m
}

func TestManifestDirectoryTransportAndPublisher(t *testing.T) {
	if errs := directoryTransportManifest(t).Validate(); errs != nil {
		t.Fatal(errs)
	}
	for name, change := range map[string]func(*Manifest){
		"directory disabled": func(m *Manifest) { *m.Daemons.Apid.GlobalAppDirectoryEnabled = false },
		"bound directory":    func(m *Manifest) { m.ControlPlaneCell = &ControlPlaneCell{ID: uuid.NewString(), Region: "eu-fsn"} },
		"missing authority":  func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport = nil },
		"empty authority":    func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport = &RuntimeQuotaTransportConfig{} },
		"missing trust":      func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.TrustBundlePath = "" },
		"relative key":       func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.PrivateKeyPath = "signing.key" },
		"unclean trust":      func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.TrustBundlePath = "/etc/faas/../trust.json" },
		"invalid issuer":     func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.DirectoryIssuerID = "not-a-uuid" },
		"nil issuer":         func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.DirectoryIssuerID = uuid.Nil.String() },
		"regional API":       func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.RegionalAPIBase = "https://api.example.com" },
		"key trust collision": func(m *Manifest) {
			m.Daemons.Apid.RuntimeQuotaTransport.TrustBundlePath = m.Daemons.Apid.RuntimeQuotaTransport.PrivateKeyPath
		},
		"relative output":   func(m *Manifest) { m.Daemons.Apid.GlobalIngressPublisher.ManifestPath = "routes.json" },
		"root output":       func(m *Manifest) { m.Daemons.Apid.GlobalIngressPublisher.ManifestPath = "/" },
		"control character": func(m *Manifest) { m.Daemons.Apid.GlobalIngressPublisher.ManifestPath = "/run/faas/routes\n.json" },
		"output key collision": func(m *Manifest) {
			m.Daemons.Apid.GlobalIngressPublisher.ManifestPath = m.Daemons.Apid.RuntimeQuotaTransport.PrivateKeyPath
		},
		"output trust collision": func(m *Manifest) {
			m.Daemons.Apid.GlobalIngressPublisher.ManifestPath = m.Daemons.Apid.RuntimeQuotaTransport.TrustBundlePath
		},
		"output consumer trust collision": func(m *Manifest) {
			m.Daemons.GatewaydInternal = &DaemonConfig{GlobalIngressTrustBundlePath: m.Daemons.Apid.GlobalIngressPublisher.ManifestPath}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := directoryTransportManifest(t)
			change(m)
			if errs := m.validateRuntimeQuotaTransport(); errs == nil {
				t.Fatal("incomplete or conflicting publisher authority accepted")
			}
		})
	}
}

func TestManifestRegionalQuotaReceiver(t *testing.T) {
	for name, change := range map[string]func(*Manifest){
		"valid":       func(*Manifest) {},
		"unbound":     func(m *Manifest) { m.ControlPlaneCell = nil },
		"signer":      func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.PrivateKeyPath = "/etc/faas/signing.key" },
		"issuer":      func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.DirectoryIssuerID = uuid.NewString() },
		"delivery":    func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.DeliveryWorker = true },
		"HTTP origin": func(m *Manifest) { m.Daemons.Apid.RuntimeQuotaTransport.RegionalAPIBase = "http://api.example.com" },
		"publisher": func(m *Manifest) {
			m.Daemons.Apid.GlobalIngressPublisher = &GlobalIngressPublisherConfig{ManifestPath: "/run/faas/routes.json"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := directoryTransportManifest(t)
			m.ControlPlaneCell = &ControlPlaneCell{ID: uuid.NewString(), Region: "eu-fsn"}
			*m.Daemons.Apid.GlobalAppDirectoryEnabled = false
			m.Daemons.Apid.GlobalIngressPublisher = nil
			m.Daemons.Apid.RuntimeQuotaTransport = &RuntimeQuotaTransportConfig{RegionalAPIBase: "https://api.example.com", TrustBundlePath: "/etc/faas/trust.json"}
			change(m)
			if errs := m.validateRuntimeQuotaTransport(); (errs == nil) != (name == "valid") {
				t.Fatalf("receiver scope: %v", errs)
			}
		})
	}
}

func TestManifestQuotaTransportPlacementAndDisabledDefaults(t *testing.T) {
	m := directoryTransportManifest(t)
	m.Daemons.GatewaydInternal = m.Daemons.Apid
	if errs := m.Daemons.validate(); errs == nil || !strings.Contains(errs.Error(), "only valid for apid") {
		t.Fatal("wrong daemon accepted quota authority", errs)
	}
	m.Daemons.Apid = &DaemonConfig{RuntimeQuotaTransport: &RuntimeQuotaTransportConfig{}, GlobalIngressPublisher: &GlobalIngressPublisherConfig{}}
	if errs := m.validateRuntimeQuotaTransport(); errs != nil {
		t.Fatal("empty tables activated quota authority", errs)
	}
	for key, value := range map[string]string{"directory_issuer_id": "issuer", "private_key_path": "/key", "trust_bundle_path": "/trust", "regional_api_base": "https://api.example.com", "delivery_worker": "true", "manifest_path": "/routes"} {
		if errs := ValidateTOMLPlacement("apid", map[string]string{key: value}); errs == nil {
			t.Fatal("table leaf accepted at top level", key)
		}
	}
}
