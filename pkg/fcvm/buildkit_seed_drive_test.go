package fcvm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seedBuilderSpec() ColdBootSpec {
	s := validColdSpec()
	s.LayerKey = "/srv/fc/builder/drive/build-1.ext4"
	s.SkipReady = true
	s.BuildKitSeedDrive = "/srv/fc/builder/drive/build-1-buildkit.ext4"
	return s
}

// adr: 20261030122204 — a builder's seed copy is a writable drive2 after its
// scratch layer, so the guest finds it at /dev/vdc.
func TestColdBootConfigBuilderSeedIsWritableDrive2(t *testing.T) {
	cfg := BuildColdBootConfig(seedBuilderSpec(), 0)
	if len(cfg.Drives) != 3 {
		t.Fatalf("want base, layer and seed drives, got %d: %+v", len(cfg.Drives), cfg.Drives)
	}
	seed := cfg.Drives[2]
	if seed.DriveID != DriveBuildKitSeed || seed.IsReadOnly || seed.IsRootDevice || seed.PathOnHost != "/srv/fc/builder/drive/build-1-buildkit.ext4" {
		t.Fatalf("drive2 = %+v, want the writable seed copy", seed)
	}
	if !cfg.EphemeralWritable {
		t.Fatal("a builder's drives must stay ephemeral writable")
	}
	if got := BuildColdBootConfig(validColdSpec(), 0); len(got.Drives) != 2 {
		t.Fatalf("a spec without a seed has %d drives, want 2", len(got.Drives))
	}
}

func TestColdBootSpecValidateSeedOnlyForBuilders(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*ColdBootSpec)
		wantErr bool
	}{
		{name: "builder", edit: func(*ColdBootSpec) {}},
		{name: "app VM", edit: func(s *ColdBootSpec) { s.SkipReady = false }, wantErr: true},
		{name: "app task", edit: func(s *ColdBootSpec) { s.AppTask = true }, wantErr: true},
		{name: "execution", edit: func(s *ColdBootSpec) { s.Networkless = true }, wantErr: true},
		{name: "agent run", edit: func(s *ColdBootSpec) { s.AgentInput, s.Networkless = true, true }, wantErr: true},
		{name: "sidecars", edit: func(s *ColdBootSpec) {
			s.Workloads = []WorkloadSpec{{Name: WorkloadNameMain, StorageKey: s.LayerKey}}
			s.LayerKey = ""
		}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := seedBuilderSpec()
			tc.edit(&s)
			if err := s.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}

func TestWritableDriveImageNameKeepsTheSeedApart(t *testing.T) {
	if got := writableDriveImageName(Drive{DriveID: DriveLayer}); got != layerImageName {
		t.Fatalf("layer jail name = %q", got)
	}
	if got := writableDriveImageName(Drive{DriveID: DriveBuildKitSeed}); got != buildkitSeedImageName || got == layerImageName {
		t.Fatalf("seed jail name = %q, want %q", got, buildkitSeedImageName)
	}
}

// The export reads drive1; a seed copy beside it must not make the scratch
// drive ambiguous.
func TestResolveDriveImageIgnoresTheSeedCopy(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 30*time.Second)
	root := v.chrootRoot("build-1")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"build-1.ext4", buildkitSeedImageName} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := v.resolveDriveImage("build-1")
	if err != nil || filepath.Base(got) != "build-1.ext4" {
		t.Fatalf("resolveDriveImage = %q, %v; want the scratch drive", got, err)
	}
}
