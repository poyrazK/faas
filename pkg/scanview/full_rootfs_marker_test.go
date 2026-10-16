package scanview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFullRootfsMarkerResolvesAbsoluteParentsInsideGuest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "guest-etc", "faas"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/guest-etc", filepath.Join(root, "etc")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "guest-etc", "faas", ".full-rootfs")
	if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0444); err != nil {
		t.Fatal(err)
	}
	present, err := FullRootfsMarkerPresent(t.Context(), root)
	if err != nil || !present {
		t.Fatal("marker lookup used the host instead of guest root", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue+"suffix"), 0444); err != nil {
		t.Fatal(err)
	}
	if present, err := FullRootfsMarkerPresent(t.Context(), root); err == nil || present {
		t.Fatal("oversized marker selected full-rootfs")
	}
}
