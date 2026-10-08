package manifest

// adr: 784
import (
	"testing"

	"github.com/google/uuid"
)

func TestGlobalIngressManifestScope(t *testing.T) {
	m, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	m.DNS.AppsDomain = "local.example.com"
	c := &DaemonConfig{Bind: "unix:///run/faas/gatewayd-internal.sock", GlobalIngressDirectoryID: uuid.NewString(), GlobalIngressAppsDomain: "global.example.com", GlobalIngressManifestPath: "/run/faas/global-routes.json", GlobalIngressTrustBundlePath: "/etc/faas/global-trust.json"}
	m.Daemons.GatewaydInternal = c
	if errs := m.validateGlobalIngress(); errs != nil {
		t.Fatal(errs)
	}
	m.ControlPlaneCell = &ControlPlaneCell{ID: uuid.NewString(), Region: "eu-fsn"}
	if errs := m.validateGlobalIngress(); errs == nil {
		t.Fatal("regional cell enabled global ingress")
	}
	m.ControlPlaneCell = nil
	c.GlobalIngressTrustBundlePath = ""
	if errs := m.validateGlobalIngress(); errs == nil {
		t.Fatal("partial route consumer accepted")
	}
	c.GlobalIngressTrustBundlePath = "/etc/faas/global-trust.json"
	m.Daemons.GatewaydInternal = nil
	m.Daemons.Apid = c
	if errs := m.Daemons.validate(); errs == nil {
		t.Fatal("wrong daemon accepted ingress settings")
	}
}
