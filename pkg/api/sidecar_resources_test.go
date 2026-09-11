package api

import "testing"

func TestSidecarDiskIOProfileWeight(t *testing.T) {
	tests := []struct {
		profile string
		weight  int
		ok      bool
	}{
		{profile: "", ok: false},
		{profile: string(SidecarDiskIOProfileLow), weight: 50, ok: true},
		{profile: string(SidecarDiskIOProfileStandard), weight: 100, ok: true},
		{profile: string(SidecarDiskIOProfileHigh), weight: 200, ok: true},
		{profile: "burst", ok: false},
	}
	for _, tc := range tests {
		got, ok := SidecarDiskIOProfileWeight(tc.profile)
		if ok != tc.ok || got != tc.weight {
			t.Errorf("SidecarDiskIOProfileWeight(%q) = (%d, %v), want (%d, %v)", tc.profile, got, ok, tc.weight, tc.ok)
		}
		if gotValid := ValidSidecarDiskIOProfile(tc.profile); gotValid != (tc.ok || tc.profile == "") {
			t.Errorf("ValidSidecarDiskIOProfile(%q) = %v", tc.profile, gotValid)
		}
	}
}
