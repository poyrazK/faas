package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	gcs "cloud.google.com/go/storage"
)

type memoryGCSObject struct {
	body       []byte
	metadata   map[string]string
	generation int64
}

type memoryGCSStore struct {
	objects map[string]memoryGCSObject
	getErr  error
}

func newMemoryGCSStore() *memoryGCSStore {
	return &memoryGCSStore{objects: make(map[string]memoryGCSObject)}
}

func (s *memoryGCSStore) Put(_ context.Context, bucket, key string, body io.Reader, metadata map[string]string) error {
	payload, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	copyMetadata := make(map[string]string, len(metadata))
	for name, value := range metadata {
		copyMetadata[name] = value
	}
	objectKey := bucket + "/" + key
	generation := s.objects[objectKey].generation + 1
	s.objects[objectKey] = memoryGCSObject{body: payload, metadata: copyMetadata, generation: generation}
	return nil
}

func (s *memoryGCSStore) Get(_ context.Context, bucket, key string) (gcsArtifact, error) {
	if s.getErr != nil {
		return gcsArtifact{}, s.getErr
	}
	object, ok := s.objects[bucket+"/"+key]
	if !ok {
		return gcsArtifact{}, gcs.ErrObjectNotExist
	}
	return gcsArtifact{
		body:       io.NopCloser(bytes.NewReader(object.body)),
		metadata:   object.metadata,
		generation: object.generation,
	}, nil
}

func (s *memoryGCSStore) Delete(_ context.Context, bucket, key string) error {
	objectKey := bucket + "/" + key
	if _, ok := s.objects[objectKey]; !ok {
		return gcs.ErrObjectNotExist
	}
	delete(s.objects, objectKey)
	return nil
}

func (s *memoryGCSStore) List(_ context.Context, bucket, prefix string) ([]string, error) {
	var keys []string
	wanted := bucket + "/" + prefix
	for objectKey := range s.objects {
		if strings.HasPrefix(objectKey, wanted) {
			keys = append(keys, strings.TrimPrefix(objectKey, bucket+"/"))
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func TestGCSStorageBackendRoundTripAndList(t *testing.T) {
	store := newMemoryGCSStore()
	backend, err := newGCSStorageBackend("gregale-artifacts-test", snapshotCompressionNone, store)
	if err != nil {
		t.Fatalf("newGCSStorageBackend: %v", err)
	}
	ctx := t.Context()
	if err := backend.Put(ctx, "kernel/1.7.0", strings.NewReader("kernel")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	reader, err := backend.Get(ctx, "kernel/1.7.0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read=%v close=%v", readErr, closeErr)
	}
	if got := string(body); got != "kernel" {
		t.Fatalf("body = %q, want kernel", got)
	}
	keys, err := backend.List(ctx, "kernel/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "kernel/1.7.0" {
		t.Fatalf("keys = %v", keys)
	}
	if err := backend.Delete(ctx, "kernel/1.7.0"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := backend.Delete(ctx, "kernel/1.7.0"); err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
	if _, err := backend.Get(ctx, "kernel/1.7.0"); !IsNotFound(err) {
		t.Fatalf("Get missing err = %v, want ErrNotFound", err)
	}
}

func TestGCSStorageBackendZstdRoundTrip(t *testing.T) {
	store := newMemoryGCSStore()
	backend, err := newGCSStorageBackend("gregale-artifacts-test", snapshotCompressionZstd, store)
	if err != nil {
		t.Fatalf("newGCSStorageBackend: %v", err)
	}
	payload := bytes.Repeat([]byte("mostly-zero-pages\x00\x00\x00"), 4096)
	key := "snap/550e8400-e29b-41d4-a716-446655440000/mem"
	if err := backend.Put(t.Context(), key, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	stored := store.objects["gregale-artifacts-test/"+key]
	if stored.metadata[gcsEncodingMetadata] != snapshotCompressionZstd {
		t.Fatalf("encoding metadata = %q", stored.metadata[gcsEncodingMetadata])
	}
	if len(stored.body) >= len(payload) {
		t.Fatalf("compressed bytes = %d, want less than %d", len(stored.body), len(payload))
	}
	reader, err := backend.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("zstd round trip changed payload")
	}
}

func TestGCSPrivateSnapshotDriveIsCompressedWithoutMemoryCompression(t *testing.T) {
	store := newMemoryGCSStore()
	backend, err := newGCSStorageBackend("gregale-artifacts-test", snapshotCompressionNone, store)
	if err != nil {
		t.Fatal(err)
	}
	key := "snap/550e8400-e29b-41d4-a716-446655440000/captures/660e8400-e29b-41d4-a716-446655440001/v2/drive"
	body := make([]byte, 4<<20)
	copy(body, []byte("ext4-private-drive"))
	if err := backend.Put(t.Context(), key, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	stored := store.objects["gregale-artifacts-test/"+key]
	if stored.metadata[gcsEncodingMetadata] != snapshotCompressionZstd {
		t.Fatalf("encoding = %q, want zstd", stored.metadata[gcsEncodingMetadata])
	}
	r, err := backend.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, body) {
		t.Fatalf("round trip changed drive: read=%v close=%v", readErr, closeErr)
	}
}

func TestGCSStorageBackendDoesNotMaskPrimaryErrors(t *testing.T) {
	store := newMemoryGCSStore()
	store.getErr = errors.New("permission denied")
	backend, err := newGCSStorageBackend("gregale-artifacts-test", snapshotCompressionNone, store)
	if err != nil {
		t.Fatalf("newGCSStorageBackend: %v", err)
	}
	if _, err := backend.Get(t.Context(), "kernel/1.7.0"); err == nil || IsNotFound(err) {
		t.Fatalf("Get err = %v, want non-not-found error", err)
	}
}

func TestValidateGCSBucket(t *testing.T) {
	for _, bucket := range []string{"", "UPPERCASE", "has/slash", " leading-space"} {
		if err := validateGCSBucket(bucket); err == nil {
			t.Errorf("validateGCSBucket(%q) = nil", bucket)
		}
	}
	if err := validateGCSBucket("gregale-artifacts-5ae37259"); err != nil {
		t.Fatalf("valid bucket rejected: %v", err)
	}
}
