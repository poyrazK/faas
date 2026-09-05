package leakcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResidentBytesAtDiscoversCanonicalAndLegacyPlanSlices(t *testing.T) {
	root := t.TempDir()
	write := func(parent, instance, value string) {
		t.Helper()
		dir := filepath.Join(root, parent, instance)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "memory.current"), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("faas-tenant-scale.slice", "vm-canonical", "4096")
	write("faas-tenant-tenant-hobby.slice", "vm-legacy", "2048")
	write("", "vm-two-level", "1024")
	write("unrelated.slice", "vm-ignore", "8192")

	got := residentBytesAt(root)
	if got["vm-canonical"] != 4096 || got["vm-legacy"] != 2048 || got["vm-two-level"] != 1024 {
		t.Fatalf("resident bytes = %#v", got)
	}
	if _, ok := got["vm-ignore"]; ok {
		t.Fatalf("unrelated systemd slice was traversed: %#v", got)
	}
}

func TestResidentBytesAtSkipsUnreadableAndMalformedScopes(t *testing.T) {
	root := t.TempDir()
	for _, instance := range []string{"vm-missing", "vm-malformed"} {
		if err := os.MkdirAll(filepath.Join(root, "faas-tenant-free.slice", instance), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "faas-tenant-free.slice", "vm-malformed", "memory.current"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := residentBytesAt(root); len(got) != 0 {
		t.Fatalf("resident bytes = %#v, want empty", got)
	}
}
