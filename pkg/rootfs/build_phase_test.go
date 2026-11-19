package rootfs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The Phase hook reports each step of an app layer build, in order, so
// imaged can time them (the layer step logged only its total before).
func TestBuildReportsPhasesInOrder(t *testing.T) {
	guest := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(guest, []byte("INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	var phases []string
	_, err := NewBuilder(&mkfsFakeRunner{fill: []byte("FAKE-EXT4")}).Build(t.Context(), BuildInput{
		Storage:       newTestStorage(t),
		StorageKey:    "apps/slug/dep.ext4",
		Plan:          api.PlanFree,
		GuestInitPath: guest,
		Manifest:      api.AppManifest{Entrypoint: []string{"/app/server"}},
		Phase:         func(name string) { phases = append(phases, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"extract", "stage", "sbom", "mkfs", "publish", "sign"}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
}
