package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	gcs "cloud.google.com/go/storage"
)

// copyingGCSStore adds the server-side rewrite to the in-memory GCS fake.
type copyingGCSStore struct{ *memoryGCSStore }

func (s copyingGCSStore) Copy(_ context.Context, bucket, srcKey, dstKey string) error {
	object, ok := s.objects[bucket+"/"+srcKey]
	if !ok {
		return gcs.ErrObjectNotExist
	}
	s.objects[bucket+"/"+dstKey] = memoryGCSObject{body: append([]byte(nil), object.body...), metadata: object.metadata, generation: 1}
	return nil
}

func TestGCSCopyObjectCopiesServerSide(t *testing.T) {
	ctx := context.Background()
	backend := &GCSStorageBackend{bucket: "artifacts", store: copyingGCSStore{newMemoryGCSStore()}}
	if err := backend.Put(ctx, "sources/old.tar.gz", strings.NewReader("archive")); err != nil {
		t.Fatal(err)
	}
	cached, err := NewLocalCacheBackend(backend, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, copier := range []ObjectCopier{backend, cached} {
		if err := copier.CopyObject(ctx, "sources/old.tar.gz", "sources/new.tar.gz"); err != nil {
			t.Fatalf("%T: %v", copier, err)
		}
		r, err := backend.Get(ctx, "sources/new.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r)
		_ = r.Close()
		if string(body) != "archive" {
			t.Fatalf("%T: copy = %q", copier, body)
		}
	}
	if err := backend.CopyObject(ctx, "sources/missing.tar.gz", "sources/x.tar.gz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source = %v, want ErrNotFound", err)
	}
	plain := &GCSStorageBackend{bucket: "artifacts", store: newMemoryGCSStore()}
	if err := plain.CopyObject(ctx, "sources/old.tar.gz", "sources/new.tar.gz"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("store without copy = %v, want ErrUnsupported", err)
	}
}
