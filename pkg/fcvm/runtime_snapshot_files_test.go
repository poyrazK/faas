// adr: 592
package fcvm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/storage"
)

type snapshotPublicationBackend struct {
	storage.StorageBackend
	put func(context.Context, string, io.Reader) error
}

func (b snapshotPublicationBackend) Put(ctx context.Context, key string, reader io.Reader) error {
	return b.put(ctx, key, reader)
}

func pinnedSnapshotFixture(t *testing.T) (capturedSnapshotFile, []byte) {
	t.Helper()
	body := append(bytes.Repeat([]byte{0}, 4096), bytes.Repeat([]byte("paused-snapshot"), 1024)...)
	path := filepath.Join(t.TempDir(), "mem")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := pinCapturedSnapshotFile(t.Context(), "snap/dep/captures/capture/v2/mem", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.file.Close() })
	return file, body
}

func TestMeasuredSnapshotPublicationRequiresCompleteExactConsumedStream(t *testing.T) {
	for _, test := range []struct {
		name string
		put  func(context.Context, string, io.Reader) error
		pass bool
	}{
		{"complete EOF", func(_ context.Context, _ string, r io.Reader) error { _, err := io.Copy(io.Discard, r); return err }, true},
		{"complete exact length", func(_ context.Context, _ string, r io.Reader) error {
			_, err := io.CopyN(io.Discard, r, int64(4096+len("paused-snapshot")*1024))
			return err
		}, true},
		{"prefix falsely succeeds", func(_ context.Context, _ string, r io.Reader) error { _, _ = io.CopyN(io.Discard, r, 10); return nil }, false},
		{"zero falsely succeeds", func(context.Context, string, io.Reader) error { return nil }, false},
		{"upload failed", func(context.Context, string, io.Reader) error { return context.DeadlineExceeded }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, _ := pinnedSnapshotFixture(t)
			err := publishMeasuredSnapshotFile(t.Context(), snapshotPublicationBackend{put: test.put}, file)
			if (err == nil) != test.pass {
				t.Fatalf("pass=%v, err=%v", test.pass, err)
			}
		})
	}
}

func TestMeasuredSnapshotPublicationRejectsSameLengthMutationEvenWithPreservedTime(t *testing.T) {
	file, body := pinnedSnapshotFixture(t)
	body[len(body)-1] ^= 1
	if err := os.WriteFile(file.path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file.path, file.info.ModTime(), file.info.ModTime()); err != nil {
		t.Fatal(err)
	}
	be := snapshotPublicationBackend{put: func(_ context.Context, _ string, r io.Reader) error { _, err := io.Copy(io.Discard, r); return err }}
	if err := publishMeasuredSnapshotFile(t.Context(), be, file); err == nil {
		t.Fatal("changed bytes inherited the paused capture digest")
	}
}

func TestMeasuredSnapshotPublicationRejectsReplacementPathAndSymlinks(t *testing.T) {
	file, body := pinnedSnapshotFixture(t)
	if err := os.Rename(file.path, file.path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file.path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	be := snapshotPublicationBackend{put: func(context.Context, string, io.Reader) error { called = true; return nil }}
	if err := publishMeasuredSnapshotFile(t.Context(), be, file); err == nil || called {
		t.Fatal("replacement path reached publication")
	}
	link := file.path + ".link"
	if err := os.Symlink(file.path, link); err != nil {
		t.Fatal(err)
	}
	if pinned, err := pinCapturedSnapshotFile(t.Context(), file.key, link); err == nil {
		_ = pinned.file.Close()
		t.Fatal("borrowed symlink became capture authority")
	}
}

func TestMeasuredSnapshotPublicationRetainsSparseBytesOnLocalBackend(t *testing.T) {
	file, body := pinnedSnapshotFixture(t)
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := publishMeasuredSnapshotFile(t.Context(), be, file); err != nil {
		t.Fatal(err)
	}
	reader, err := be.Get(t.Context(), file.key)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("sparse capture byte stream changed", err)
	}
}

func TestNativeSnapshotHandoffReleaseCancelsAndJoinsPublication(t *testing.T) {
	file, _ := pinnedSnapshotFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	handoff := &runtimeDriveHandoff{drives: []pinnedRuntimeDrive{{file: file.file}}}
	flight := &nativeSnapshotFlight{ctx: ctx, cancel: cancel, done: make(chan struct{}), handoff: handoff}
	handoff.snapshot = flight
	v := &JailerVMM{runtimeDriveHandoffs: map[string]*runtimeDriveHandoff{"instance": handoff}}
	entered, completed := make(chan struct{}), make(chan error, 1)
	be := snapshotPublicationBackend{put: func(ctx context.Context, _ string, _ io.Reader) error { close(entered); <-ctx.Done(); return ctx.Err() }}
	go func() {
		defer flight.finish()
		completed <- publishMeasuredSnapshotFile(ctx, be, file)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("publication did not start")
	}
	v.cancelNativeSnapshot("instance")
	if _, err := file.file.Stat(); err != nil || handoff.snapshot != nil {
		t.Fatal("capture was not joined before releasing its pinned drive", err)
	}
	if err := v.releaseRuntimeDriveHandoff("instance"); err != nil {
		t.Fatal(err)
	}
	if err := <-completed; !errors.Is(err, context.Canceled) {
		t.Fatal("handoff release failed to cancel publication", err)
	}
	if _, err := file.file.Stat(); err == nil || len(v.runtimeDriveHandoffs) != 0 || handoff.snapshot != nil {
		t.Fatal("released capture retained file or flight ownership")
	}
}

func TestNativeSnapshotFreshNamespaceRejectsPublishedParts(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := &JailerVMM{storage: be}
	spec := SnapshotSpec{StorageKey: "snap/dep/captures/capture/v2/mem", VMStateStorageKey: "snap/dep/captures/capture/v2/vmstate"}
	if err := v.checkFreshSnapshotNamespace(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if err := be.Put(t.Context(), spec.VMStateStorageKey, bytes.NewReader([]byte("previous"))); err != nil {
		t.Fatal(err)
	}
	if err := v.checkFreshSnapshotNamespace(t.Context(), spec); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("existing capture part permitted a retry overwrite", err)
	}
}
