// adr: 593
package fcvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/storage"
)

type runtimeSourceTestBackend struct {
	storage.StorageBackend
	data map[string][]byte
	gets atomic.Int32
	open func(context.Context, string) (io.ReadCloser, error)
}

func (b *runtimeSourceTestBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b.gets.Add(1)
	if b.open != nil {
		return b.open(ctx, key)
	}
	return io.NopCloser(bytes.NewReader(b.data[key])), nil
}

func runtimeSourceFixture(kind, workload, key string, data []byte) runtimeadmission.ArtifactSource {
	hash := sha256.Sum256(data)
	return runtimeadmission.ArtifactSource{Kind: kind, WorkloadName: workload, StorageKey: key, Digest: "sha256:" + hex.EncodeToString(hash[:]), Bytes: int64(len(data))}
}

func TestSealedRuntimeSourcesShareReadOnlyBytesAndIsolateWritableDrives(t *testing.T) {
	body := []byte("complete ext4 includes unused capacity\x00\x00")
	source := runtimeSourceFixture("base-image", "", "base/a.ext4", body)
	backend := &runtimeSourceTestBackend{data: map[string][]byte{source.StorageKey: body}}
	cache := newRuntimeSourceCache()
	t.Cleanup(func() { _ = cache.release("one"); _ = cache.release("two") })
	one, err := cache.acquire(t.Context(), backend, "one", source)
	if err != nil {
		t.Fatal(err)
	}
	backend.data[source.StorageKey] = []byte("mutable key now names unrelated content")
	two, err := cache.acquire(t.Context(), backend, "two", source)
	if err != nil || one != two || backend.gets.Load() != 1 {
		t.Fatalf("shared source err=%v reads=%d", err, backend.gets.Load())
	}
	got, err := os.ReadFile(two)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("mutable storage changed the sealed input")
	}
	info, _ := os.Stat(two)
	rootInfo, _ := os.Stat(cache.root)
	if info.Mode().Perm() != 0o444 || rootInfo.Mode().Perm() != 0o700 {
		t.Fatal("source protections missing")
	}
	private := t.TempDir()
	if _, err := stageWritableAs(private, one, layerImageName, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, layerImageName), []byte("guest writes"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(one)
	if !bytes.Equal(got, body) {
		t.Fatal("writable drive aliases its approved source")
	}
	if err := cache.release("one"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(two); err != nil {
		t.Fatal("first consumer removed another consumer's source")
	}
	if err := cache.release("two"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(two); !os.IsNotExist(err) || cache.root != "" || len(cache.entries) != 0 {
		t.Fatal("last release leaked a source")
	}
}

type runtimeSourceFaultReader struct {
	io.Reader
	readErr, closeErr error
}

func (r *runtimeSourceFaultReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF && r.readErr != nil {
		err = r.readErr
	}
	return n, err
}
func (r *runtimeSourceFaultReader) Close() error { return r.closeErr }

func TestSealedRuntimeSourcesRefuseCorruptionAndStreamFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*runtimeadmission.ArtifactSource, *runtimeSourceTestBackend)
	}{
		{"digest", func(a *runtimeadmission.ArtifactSource, _ *runtimeSourceTestBackend) {
			a.Digest = "sha256:" + hex.EncodeToString(make([]byte, 32))
		}},
		{"truncated", func(_ *runtimeadmission.ArtifactSource, b *runtimeSourceTestBackend) {
			b.data["base/a.ext4"] = []byte("x")
		}},
		{"overflow", func(_ *runtimeadmission.ArtifactSource, b *runtimeSourceTestBackend) {
			b.data["base/a.ext4"] = []byte("approved plus trailing bytes")
		}},
		{"read failure", func(_ *runtimeadmission.ArtifactSource, b *runtimeSourceTestBackend) {
			b.open = func(context.Context, string) (io.ReadCloser, error) {
				return &runtimeSourceFaultReader{Reader: bytes.NewReader([]byte("approved")), readErr: io.ErrUnexpectedEOF}, nil
			}
		}},
		{"close failure", func(_ *runtimeadmission.ArtifactSource, b *runtimeSourceTestBackend) {
			b.open = func(context.Context, string) (io.ReadCloser, error) {
				return &runtimeSourceFaultReader{Reader: bytes.NewReader([]byte("approved")), closeErr: io.ErrUnexpectedEOF}, nil
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := runtimeSourceFixture("base-image", "", "base/a.ext4", []byte("approved"))
			backend := &runtimeSourceTestBackend{data: map[string][]byte{source.StorageKey: []byte("approved")}}
			test.mutate(&source, backend)
			cache := newRuntimeSourceCache()
			path, err := cache.acquire(t.Context(), backend, "one", source)
			if err == nil || path != "" || len(cache.entries) != 0 || cache.root != "" {
				t.Fatalf("failure retained source: path=%q err=%v entries=%d", path, err, len(cache.entries))
			}
		})
	}
}

func TestSealedRuntimeSourcesCancellationClosesBlockedStream(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	opened := make(chan struct{})
	backend := &runtimeSourceTestBackend{open: func(context.Context, string) (io.ReadCloser, error) { close(opened); return reader, nil }}
	cache := newRuntimeSourceCache()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	source := runtimeSourceFixture("base-image", "", "base/a.ext4", []byte("approved"))
	go func() { _, err := cache.acquire(ctx, backend, "one", source); result <- err }()
	<-opened
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || cache.root != "" || len(cache.entries) != 0 {
			t.Fatalf("cancel err=%v retained=%d", err, len(cache.entries))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled download did not join the blocked stream")
	}
}

func TestSealedRuntimeSourcesConcurrentConsumersDownloadOnce(t *testing.T) {
	source := runtimeSourceFixture("base-image", "", "base/a.ext4", []byte("approved"))
	backend := &runtimeSourceTestBackend{data: map[string][]byte{source.StorageKey: []byte("approved")}}
	cache := newRuntimeSourceCache()
	var wg sync.WaitGroup
	for _, instance := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.acquire(t.Context(), backend, instance, source); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if backend.gets.Load() != 1 {
		t.Fatal("shared immutable base duplicated per consumer")
	}
	for _, instance := range []string{"one", "two", "three"} {
		if err := cache.release(instance); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSealedRuntimeSourcesNeverBorrowLocalStorageInode(t *testing.T) {
	root := t.TempDir()
	backend, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("approved local file including metadata\x00\x00")
	source := runtimeSourceFixture("base-image", "", "base/a.ext4", body)
	if err := backend.Put(t.Context(), source.StorageKey, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	cache := newRuntimeSourceCache()
	t.Cleanup(func() { _ = cache.release("one") })
	protected, err := cache.acquire(t.Context(), backend, "one", source)
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, source.StorageKey)
	localInfo, _ := os.Stat(local)
	protectedInfo, _ := os.Stat(protected)
	if os.SameFile(localInfo, protectedInfo) {
		t.Fatal("approved input borrowed a mutable backend inode")
	}
	if err := os.WriteFile(local, []byte("attacker replaces backend contents after verification"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(protected)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("local-path or hardlink shortcut bypassed stream protection")
	}
}

func TestSealedRuntimeSourcesTeardownDoesNotWaitForAnotherDownload(t *testing.T) {
	cache := newRuntimeSourceCache()
	body := []byte("approved base")
	source := runtimeSourceFixture("base-image", "", "base/a.ext4", body)
	backend := &runtimeSourceTestBackend{data: map[string][]byte{source.StorageKey: body}}
	path, err := cache.acquire(t.Context(), backend, "resident", source)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	opened := make(chan struct{})
	blocked := &runtimeSourceTestBackend{open: func(context.Context, string) (io.ReadCloser, error) { close(opened); return reader, nil }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := cache.acquire(ctx, blocked, "waking", runtimeSourceFixture("app-layer", "", "rootfs/main.ext4", []byte("different main")))
		result <- err
	}()
	<-opened
	released := make(chan error, 1)
	go func() { released <- cache.release("resident") }()
	select {
	case err := <-released:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("native teardown waited on an unrelated source download")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("resident input remained after release")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) || cache.root != "" {
		t.Fatalf("late download cleanup err=%v", err)
	}
}
