// adr: 592
package fcvm

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
)

type parentMountAdmissionStorage struct {
	storage.StorageBackend
	gets int
}

func (s *parentMountAdmissionStorage) Get(context.Context, string) (io.ReadCloser, error) {
	s.gets++
	return nil, errors.New("must not read storage before capacity admission")
}

func TestParentMountCapacityRefusesBeforeNativeWork(t *testing.T) {
	for _, operation := range []string{"legacy mount", "legacy copy", "verified copy", "overlay mount"} {
		t.Run(operation, func(t *testing.T) {
			registry := vmmdmount.NewRegistry(1)
			active, err := registry.ReserveMount(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer active.Release(t.Context())
			backend := &parentMountAdmissionStorage{}
			manager := &Manager{parentMounts: registry, storage: backend}
			expected := imagechain.ParentMaterialization{Artifact: imagechain.BaseArtifact{StorageKey: "base/parent.ext4", Digest: imagechain.Digest([]byte("bytes")), Bytes: 5}, TargetDir: "/dev/shm/faas-base-staging/target"}
			switch operation {
			case "legacy mount":
				_, err = manager.MountParentExt4(t.Context(), expected.Artifact.StorageKey)
			case "legacy copy":
				err = manager.MaterializeParentExt4(t.Context(), expected.Artifact.StorageKey, expected.TargetDir)
			case "verified copy":
				var receipt imagechain.ParentMaterialization
				receipt, err = manager.MaterializeVerifiedParentExt4(t.Context(), expected)
				if receipt != (imagechain.ParentMaterialization{}) {
					t.Fatal("capacity refusal returned a receipt")
				}
			case "overlay mount":
				err = manager.MountOverlayParent(t.Context(), "lower", "upper", "work", "merged")
			}
			if !errors.Is(err, vmmdmount.ErrMountCapacity) || backend.gets != 0 {
				t.Fatalf("native work preceded capacity admission: %v, reads=%d", err, backend.gets)
			}
		})
	}
}

func TestParentMountFailedSourceReleasesReservation(t *testing.T) {
	registry := vmmdmount.NewRegistry(1)
	backend := &parentMountAdmissionStorage{}
	manager := &Manager{parentMounts: registry, storage: backend}
	if _, err := manager.MountParentExt4(t.Context(), "base/parent.ext4"); err == nil {
		t.Fatal("failed source accepted")
	}
	lease, err := registry.ReserveMount(t.Context())
	if err != nil {
		t.Fatalf("failed source leaked capacity: %v", err)
	}
	if err := lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}
