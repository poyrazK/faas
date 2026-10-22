package oci

import "testing"

// adr: 1239 — a customer image without USER runs as root, as under Docker.
func TestImageProcessUserDefaultsToRoot(t *testing.T) {
	for in, want := range map[string]string{
		"":          "0",
		"0":         "0",
		"app":       "app",
		"1001":      "1001",
		"node:node": "node:node",
	} {
		if got := ImageProcessUser(in); got != want {
			t.Fatalf("ImageProcessUser(%q) = %q, want %q", in, got, want)
		}
	}
	manifest, err := ManifestFromConfig(Config{Cmd: []string{"nginx"}, User: ImageProcessUser("")})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.EffectiveUser() != "0" {
		t.Fatalf("effective user = %q, want root", manifest.EffectiveUser())
	}
}
