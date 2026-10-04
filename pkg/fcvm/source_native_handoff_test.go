package fcvm

// adr: 435. Exact staged bytes exercise handoff, not a live VM acknowledgement.

import (
	"os"
	"path/filepath"
	"testing"
)

func sourceNativeDriveFixture(t *testing.T, kind string) runtimeDriveFixture {
	t.Helper()
	f := newRuntimeDriveFixture(t)
	if err := f.vmm.releaseRuntimeSources(f.lease.Instance); err != nil {
		t.Fatal(err)
	}
	f.sources[1].Kind = kind
	if _, err := f.vmm.prepareVerifiedColdBoot(t.Context(), f.lease, f.spec, f.sources); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSourceNativeDriveHandoffKinds(t *testing.T) {
	for _, kind := range []string{"source-app-layer", "function-layer"} {
		t.Run(kind, func(t *testing.T) {
			f := sourceNativeDriveFixture(t, kind)
			f.pin(t)
			if err := os.WriteFile(filepath.Join(f.root, f.config.Drives[1].PathOnHost), []byte("env!"), 0o600); err != nil {
				t.Fatal(err)
			}
			handoff := f.measure(t)
			main := handoff.drives[1].observation
			if main.Source != f.sources[1] || main.Source.Kind != kind || main.ReadOnly || main.Producer == main.Injected || handoff.drives[0].observation.Source.Kind != "base-image" {
				t.Fatal("source handoff lost kind, private main or separate base")
			}
			if _, err := f.vmm.ObservedRuntimeDrives(t.Context(), f.lease); err == nil {
				t.Fatal("staged source bytes fabricated native observation")
			}
		})
	}
}
