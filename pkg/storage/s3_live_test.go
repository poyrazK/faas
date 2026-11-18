package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
)

// TestS3BackendLive runs the S3 backend against a real S3-compatible bucket.
// It is skipped unless FAAS_S3_LIVE_BUCKET is set, together with
// FAAS_S3_LIVE_ENDPOINT, FAAS_S3_LIVE_REGION, FAAS_S3_LIVE_ACCESS_KEY_ID and
// FAAS_S3_LIVE_SECRET_ACCESS_KEY. Use a disposable bucket: every object the
// test writes lives under one random prefix and is deleted at the end.
//
// Examples: GCS interoperability (endpoint https://storage.googleapis.com,
// region auto, HMAC key), OVHcloud (https://s3.bhs.io.cloud.ovh.net, bhs).
func TestS3BackendLive(t *testing.T) {
	bucket := os.Getenv("FAAS_S3_LIVE_BUCKET")
	if bucket == "" {
		t.Skip("FAAS_S3_LIVE_BUCKET not set")
	}
	cfg := S3Config{
		Bucket:          bucket,
		Endpoint:        os.Getenv("FAAS_S3_LIVE_ENDPOINT"),
		Region:          os.Getenv("FAAS_S3_LIVE_REGION"),
		AccessKeyID:     os.Getenv("FAAS_S3_LIVE_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("FAAS_S3_LIVE_SECRET_ACCESS_KEY"),
		PathStyle:       os.Getenv("FAAS_S3_LIVE_PATH_STYLE") == "1",
	}
	backend, err := NewS3StorageBackend(cfg, "zstd")
	if err != nil {
		t.Fatal(err)
	}
	backend.WithSparseExtents(true)
	ctx := t.Context()
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	prefix := "s3-live-test-" + hex.EncodeToString(id) + "/"
	t.Cleanup(func() {
		// t.Context is already cancelled when cleanups run.
		cleanupCtx := context.Background()
		keys, err := backend.List(cleanupCtx, "layers/"+prefix)
		if err != nil {
			t.Logf("cleanup list: %v", err)
		}
		for _, k := range append(keys, "kernel/"+prefix+"k", "snap/"+prefix+"mem", "apps/"+prefix+"rootfs.ext4") {
			if err := backend.Delete(cleanupCtx, k); err != nil {
				t.Logf("cleanup delete %s: %v", k, err)
			}
		}
	})

	small := "kernel/" + prefix + "k"
	if err := backend.Put(ctx, small, strings.NewReader("kernel bytes")); err != nil {
		t.Fatalf("small put: %v", err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, small)); string(got) != "kernel bytes" {
		t.Fatalf("small get = %q", got)
	}
	if ok, err := backend.Exists(ctx, small); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if ok, err := backend.Exists(ctx, "kernel/"+prefix+"missing"); err != nil || ok {
		t.Fatalf("Exists(missing) = %v, %v", ok, err)
	}
	if _, err := backend.Get(ctx, "kernel/"+prefix+"missing"); !IsNotFound(err) {
		t.Fatalf("Get(missing) = %v, want ErrNotFound", err)
	}

	// Multipart: three parts, unknown length.
	big := make([]byte, 2*s3PartSize+4321)
	_, _ = rand.Read(big)
	bigKey := "layers/" + prefix + "big"
	if err := backend.Put(ctx, bigKey, io.MultiReader(bytes.NewReader(big))); err != nil {
		t.Fatalf("multipart put: %v", err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, bigKey)); !bytes.Equal(got, big) {
		t.Fatal("multipart object differs")
	}

	// zstd-encoded snapshot memory: metadata must survive the round trip.
	mem := bytes.Repeat([]byte{0}, 8<<20)
	copy(mem, "guest memory")
	memKey := "snap/" + prefix + "mem"
	if err := backend.Put(ctx, memKey, bytes.NewReader(mem)); err != nil {
		t.Fatalf("snapshot put: %v", err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, memKey)); !bytes.Equal(got, mem) {
		t.Fatal("snapshot memory differs after zstd round trip")
	}

	// Extent encoding from a sparse regular file.
	f, err := os.CreateTemp(t.TempDir(), "rootfs")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(32 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("ext4 superblock"), 1024); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rootfsKey := "apps/" + prefix + "rootfs.ext4"
	if err := backend.Put(ctx, rootfsKey, f); err != nil {
		t.Fatalf("rootfs put: %v", err)
	}
	_ = f.Close()
	got := readAllAndClose(t, mustGet(t, backend, rootfsKey))
	if len(got) != 32<<20 || !bytes.Equal(got[1024:1024+15], []byte("ext4 superblock")) {
		t.Fatalf("rootfs differs: %d bytes", len(got))
	}

	copyKey := "layers/" + prefix + "copy"
	if err := backend.CopyObject(ctx, memKey, copyKey); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, copyKey)); !bytes.Equal(got, mem) {
		t.Fatal("copied object differs (metadata not copied?)")
	}

	keys, err := backend.List(ctx, "layers/"+prefix)
	if err != nil || len(keys) != 2 {
		t.Fatalf("List = %v, %v; want big and copy", keys, err)
	}
	if err := backend.Delete(ctx, small); err != nil {
		t.Fatal(err)
	}
	if err := backend.Delete(ctx, small); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if ok, _ := backend.Exists(ctx, small); ok {
		t.Fatal("object survived delete")
	}
}
