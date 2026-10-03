package manifest

import (
	"testing"

	"github.com/google/uuid"
)

func TestGlobalDirectoryCannotShareRegionalManifest(t *testing.T) {
	m, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	m.Daemons.Apid = &DaemonConfig{GlobalAppDirectoryEnabled: &enabled}
	if errs := m.validateControlPlaneCell(); errs != nil {
		t.Fatal(errs)
	}
	m.ControlPlaneCell = &ControlPlaneCell{ID: uuid.NewString(), Region: "eu-fsn"}
	for i := range m.Fleet.Hosts {
		m.Fleet.Hosts[i].Region = "eu-fsn"
	}
	if errs := m.validateControlPlaneCell(); errs == nil {
		t.Fatal("directory and regional cell share a database scope")
	}
}
