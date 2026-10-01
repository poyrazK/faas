package manifest

import (
	"strings"
	"testing"
)

func TestComputeRegionManifestValidation(t *testing.T) {
	for _, tc := range []struct {
		region, zone string
		valid        bool
	}{
		{"", "", true}, {"eu-fsn", "eu-fsn-a", true},
		{"", "a", false}, {"EU-FSN", "a", false}, {"eu-fsn", "-a", false},
	} {
		m, err := Parse([]byte(validManifest))
		if err != nil {
			t.Fatal(err)
		}
		m.Fleet.Hosts[1].Region, m.Fleet.Hosts[1].Zone = tc.region, tc.zone
		errs := m.Validate()
		if tc.valid && errs != nil {
			t.Fatalf("valid locality rejected: %v", errs)
		}
		if !tc.valid && (errs == nil || !strings.Contains(errs.Error(), "region")) {
			t.Fatalf("invalid locality accepted: region %q zone %q: %v", tc.region, tc.zone, errs)
		}
	}
}
