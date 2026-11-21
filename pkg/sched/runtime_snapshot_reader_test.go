package sched_test

import (
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRuntimeSnapshotReaderIsReadOnlyAndPrefersNative(t *testing.T) {
	canonical, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nativeRoot := t.TempDir()
	native, err := storage.NewLocalStorageBackend(nativeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := canonical.Put(t.Context(), "snapshot", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	if err := native.Put(t.Context(), "snapshot", strings.NewReader("private")); err != nil {
		t.Fatal(err)
	}
	reader, err := sched.NewRuntimeSnapshotReader(canonical, nativeRoot)
	if err != nil {
		t.Fatal(err)
	}
	read := func(want string) {
		t.Helper()
		body, err := reader.Get(t.Context(), "snapshot")
		if err != nil {
			t.Fatal(err)
		}
		defer body.Close()
		data, err := io.ReadAll(body)
		if err != nil || string(data) != want {
			t.Fatalf("read %q, want %q: %v", data, want, err)
		}
	}
	read("private")
	if reader.Put(t.Context(), "snapshot", strings.NewReader("replacement")) == nil || reader.Delete(t.Context(), "snapshot") == nil {
		t.Fatal("snapshot reader allowed mutation")
	}
	read("private")
	if err := native.Delete(t.Context(), "snapshot"); err != nil {
		t.Fatal(err)
	}
	read("old")
}
