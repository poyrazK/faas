package fcvm

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestExecutionTemplatePrivatePublicationCannotOverwriteOrEscape(t *testing.T) {
	v, canonical, native := nativeSnapshotTreesFixture(t)
	key := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
	backend, err := v.executionTemplateStorage(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := backend.(storage.LocalPathResolver); ok {
		t.Fatal("private producer exposed the shared local-path shortcut")
	}
	if err := backend.Put(t.Context(), key, strings.NewReader("pristine")); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(t.Context(), key, strings.NewReader("replacement")); !errors.Is(err, storage.ErrArtifactExists) {
		t.Fatalf("overwrote an exclusive artifact: %v", err)
	}
	other := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
	if err := backend.Put(t.Context(), other, strings.NewReader("foreign")); !errors.Is(err, storage.ErrInvalidKey) {
		t.Fatal("capture writer escaped its own namespace")
	}
	if _, err := canonical.Get(t.Context(), key); !storage.IsNotFound(err) {
		t.Fatal("template escaped into the shared writable store")
	}
	reader, err := native.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	m := &Manager{vmm: v}
	if err := m.RetireNativeRuntimeSnapshotObject(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Get(t.Context(), key); !storage.IsNotFound(err) {
		t.Fatal("manager did not forward private artifact retirement")
	}
}
