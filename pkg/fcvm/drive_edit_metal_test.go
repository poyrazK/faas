//go:build linux && metal

package fcvm

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/guestdisk"
)

// metalDriveSession opens a fixture drive the test built itself with
// e2fsprogs directly; production always goes through vmmd-guestdisk.
func metalDriveSession(t *testing.T, drive string) *guestdisk.Session {
	t.Helper()
	ed, err := guestdisk.New(guestdisk.Config{ScratchRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s, err := ed.Open(drive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}
