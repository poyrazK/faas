//go:build linux || darwin

// adr: 567 — ambiguous kernel evidence must not grant helper retirement.
package fcvm

import "testing"

func TestNativeHostHelperCgroupEvidenceRejectsAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		raw       string
		populated bool
		valid     bool
	}{
		{"populated 0\nfrozen 0\n", false, true},
		{"populated 1\nfrozen 0\n", true, true},
		{"populated 0\n", false, true},
		{"frozen 0\n", false, false},
		{"populated 0\npopulated 1\n", false, false},
		{"populated 2\n", false, false},
		{"populated 0 extra\n", false, false},
		{"populated 0\nunknown 0\n", false, false},
		{"", false, false},
	} {
		populated, err := parseNativeHelperCgroupPopulated(tc.raw)
		if (err == nil) != tc.valid || tc.valid && populated != tc.populated {
			t.Errorf("events=%q populated=%v err=%v", tc.raw, populated, err)
		}
	}
	for _, tc := range []struct {
		raw   string
		want  string
		valid bool
	}{
		{"0::/faas.slice/faas-cp.slice/faas-vmmd.service\n", "faas.slice/faas-cp.slice/faas-vmmd.service", true},
		{"0::/\n", "", true},
		{"0::/../../unrelated\n", "", false},
		{"0:://unrelated\n", "", false},
		{"0::/one\n0::/two\n", "", false},
		{"0::/one\n1:memory:/other\n", "", false},
		{"1:memory:/other\n", "", false},
	} {
		path, err := nativeHostHelperParent(tc.raw)
		if (err == nil) != tc.valid || tc.valid && path != tc.want {
			t.Errorf("membership=%q path=%q err=%v", tc.raw, path, err)
		}
	}
}
