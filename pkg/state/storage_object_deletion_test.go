package state_test

// adr: 20261115180000

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestClassifyStorageObject(t *testing.T) {
	for _, tc := range []struct {
		key, class, mem, family string
	}{
		{"snap/d/captures/c/v2/mem", state.StorageObjectClassSnapshot, "snap/d/captures/c/v2/mem", "snap/d/captures/c/v2/"},
		{"snap/d/warm/captures/c/v2/vmstate.age", state.StorageObjectClassSnapshot, "snap/d/warm/captures/c/v2/mem", "snap/d/warm/captures/c/v2/"},
		{"snap/d/vmstate", state.StorageObjectClassSnapshot, "snap/d/mem", ""},
		{"snap/d/warm/mem", state.StorageObjectClassSnapshot, "snap/d/warm/mem", ""},
		{"apps/slug/d.ext4", state.StorageObjectClassLayer, "", ""},
		{"layers/d.ext4", state.StorageObjectClassLayer, "", ""},
		{"jobs/j.ext4", state.StorageObjectClassLayer, "", ""},
		{"base/base-amd64.ext4", state.StorageObjectClassPlatform, "", ""},
		{"kernel/1.10.0", state.StorageObjectClassPlatform, "", ""},
		{"sbom/d.json", state.StorageObjectClassUnclassified, "", ""},
		{"snap/d/captures/c/v1/mem", state.StorageObjectClassUnclassified, "", ""},
		{"snap//mem", state.StorageObjectClassUnclassified, "", ""},
	} {
		class, mem, family := state.ClassifyStorageObject(tc.key)
		if class != tc.class || mem != tc.mem || family != tc.family {
			t.Errorf("%s = %s %q %q, want %s %q %q", tc.key, class, mem, family, tc.class, tc.mem, tc.family)
		}
	}
}
