// adr: 1152
// zones_test.go pins the ADR-1152 §2 cell shape: one control-plane host, or
// an odd number of at least three, each in its own zone.

package manifest

import (
	"strings"
	"testing"
)

// threeControlPlanes extends validManifest to three control-plane hosts and
// one compute host, with the given zones ("" omits the field).
func threeControlPlanes(zones ...string) string {
	cp := func(name, addr, zone string) string {
		s := "    - name: " + name + "\n      role: control-plane\n      address: " + addr + "\n"
		if zone != "" {
			s += "      zone: " + zone + "\n"
		}
		return s
	}
	hosts := cp("fsn-1", "10.42.0.1:7100", zones[0]) +
		cp("fsn-3", "10.42.0.3:7100", zones[1]) +
		cp("fsn-4", "10.42.0.4:7100", zones[2])
	return strings.Replace(validManifest,
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n", hosts, 1)
}

// adr: 1152
func TestValidate_ControlPlaneZones(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // empty: valid
	}{
		{name: "one control plane needs no zone", body: validManifest},
		{name: "three control planes in three zones", body: threeControlPlanes("us-east1-b", "us-east1-c", "us-east1-d")},
		{name: "missing zone", body: threeControlPlanes("us-east1-b", "", "us-east1-d"), want: "needs a zone"},
		{name: "shared zone", body: threeControlPlanes("us-east1-b", "us-east1-b", "us-east1-d"), want: "share zone"},
		{name: "bad zone label", body: threeControlPlanes("us-east1-b", "US_East", "us-east1-d"), want: "lower-case letters"},
		{
			name: "two control planes",
			body: strings.Replace(threeControlPlanes("us-east1-b", "us-east1-c", "us-east1-d"),
				"    - name: fsn-4\n      role: control-plane\n      address: 10.42.0.4:7100\n      zone: us-east1-d\n", "", 1),
			want: "odd number of at least 3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.body, "fsn-1") {
				t.Fatal("fixture lost its control plane")
			}
			m, err := Parse([]byte(tt.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			errs := m.Validate()
			if tt.want == "" {
				if errs != nil {
					t.Fatalf("Validate: %v", errs)
				}
				return
			}
			if errs == nil || !strings.Contains(errs.Error(), tt.want) {
				t.Fatalf("Validate = %v, want substring %q", errs, tt.want)
			}
		})
	}
}
