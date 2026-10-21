package api

import (
	"strings"
	"testing"
)

// adr: 1790
func TestValidateImageRunWorkdir(t *testing.T) {
	for _, tc := range []struct {
		dir string
		ok  bool
	}{
		{dir: "", ok: true},
		{dir: "/", ok: true},
		{dir: "/srv/data/", ok: true},
		{dir: "/" + strings.Repeat("d", ImageRunWorkdirMaxBytes-1), ok: true},
		{dir: "/" + strings.Repeat("d", ImageRunWorkdirMaxBytes)},
		{dir: "srv"},
		{dir: "./srv"},
		{dir: "/a\x00b"},
		{dir: "/a\nb"},
	} {
		if err := ValidateImageRunWorkdir(tc.dir); (err == nil) != tc.ok {
			t.Errorf("ValidateImageRunWorkdir(%q) = %v, want ok=%t", tc.dir, err, tc.ok)
		}
	}
}
