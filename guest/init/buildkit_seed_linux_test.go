package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWantBuildKitSeed(t *testing.T) {
	cases := []struct {
		name string
		m    api.BuildManifest
		want bool
	}{
		{name: "no seed", m: api.BuildManifest{}, want: false},
		{name: "seeded build", m: api.BuildManifest{BuildKitSeed: true}, want: true},
		{name: "seed run", m: api.BuildManifest{BuildKitSeedOnly: true}, want: true},
		{name: "warm builder keeps its own state", m: api.BuildManifest{BuildKitSeed: true, KeepWarm: true}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantBuildKitSeed(tc.m); got != tc.want {
				t.Fatalf("wantBuildKitSeed = %v, want %v", got, tc.want)
			}
		})
	}
}

// Anything that is not a block device is refused before mounting, so a
// missing or odd drive2 falls back instead of mounting a file.
func TestMountBuildKitSeedRefusesNonDevices(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "vdc")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "marker")
	for name, device := range map[string]string{"missing": filepath.Join(dir, "absent"), "regular file": regular} {
		t.Run(name, func(t *testing.T) {
			if _, err := mountBuildKitSeedAt(device, filepath.Join(dir, "mnt"), marker); err == nil {
				t.Fatal("mounted something that is not a block device")
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("a refused drive2 wrote the seeded marker")
			}
		})
	}
}

func TestBuildKitSeedFieldsAreOmittedWhenUnset(t *testing.T) {
	for _, v := range []any{api.BuildManifest{}, api.BuildDone{}} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "buildkit_seed") {
			t.Fatalf("%T marshals a seed field when unset: %s", v, raw)
		}
	}
}
