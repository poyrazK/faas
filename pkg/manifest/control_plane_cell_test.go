package manifest

import (
	"testing"

	"github.com/google/uuid"
)

func TestControlPlaneCellManifest(t *testing.T) {
	for _, tc := range []struct {
		name, id, region, hostRegion string
		valid                        bool
	}{
		{"valid", uuid.NewString(), "eu-fsn", "eu-fsn", true},
		{"missing UUID", "", "eu-fsn", "eu-fsn", false},
		{"zero UUID", uuid.Nil.String(), "eu-fsn", "eu-fsn", false},
		{"noncanonical UUID", "01A0F8A4-1808-7F20-898A-EABDD349B0EC", "eu-fsn", "eu-fsn", false},
		{"missing region", uuid.NewString(), "", "", false},
		{"foreign host", uuid.NewString(), "eu-fsn", "eu-hel", false},
		{"unlabelled host", uuid.NewString(), "eu-fsn", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse([]byte(validManifest))
			if err != nil {
				t.Fatal(err)
			}
			m.ControlPlaneCell = &ControlPlaneCell{ID: tc.id, Region: tc.region}
			for i := range m.Fleet.Hosts {
				m.Fleet.Hosts[i].Region = tc.hostRegion
			}
			if errs := m.Validate(); (errs == nil) != tc.valid {
				t.Fatalf("valid=%t errors=%v", tc.valid, errs)
			}
		})
	}
}
