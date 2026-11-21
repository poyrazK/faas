package runfilestore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/runfilestore"
)

type blobStore struct {
	mu   sync.Mutex
	data []byte
	fail bool
}

func (b *blobStore) Put(ctx context.Context, _ string, r io.Reader) error {
	if b.fail {
		return errors.New("synthetic storage failure")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	b.data = data
	b.mu.Unlock()
	return nil
}
func (b *blobStore) Get(context.Context, string) (io.ReadCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return io.NopCloser(bytes.NewReader(b.data)), nil
}
func (b *blobStore) Delete(context.Context, string) error { return nil }
func digest(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}
func TestStoredFileEncryptionRoundTripAndScope(t *testing.T) {
	ctx := context.Background()
	account, id := uuid.NewString(), uuid.NewString()
	root, _ := age.GenerateX25519Identity()
	key := runfile.Key(account, id, uuid.NewString())
	private, sealed, err := runfilestore.NewKey(account, id, key, []*age.X25519Recipient{root.Recipient()})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := runfilestore.OpenKey(account, id, key, sealed, []*age.X25519Identity{root})
	if err != nil || opened != private {
		t.Fatal(err)
	}
	if _, err = runfilestore.OpenKey(uuid.NewString(), id, key, sealed, []*age.X25519Identity{root}); err == nil {
		t.Fatal("key opened under another account")
	}
	data := bytes.Repeat([]byte("private-run-file"), 300000)
	ref := runfile.Ref{Descriptor: runfile.Descriptor{ID: id, Path: "data.bin", SizeBytes: int64(len(data)), SHA256: digest(data)}, StorageKey: key, PrivateKey: private}
	store := &blobStore{}
	written, err := runfilestore.Put(ctx, store, account, ref, io.NopCloser(bytes.NewReader(data)), ref.SizeBytes, ref.SHA256)
	if err != nil || written.SHA256 != ref.SHA256 {
		t.Fatal(written, err)
	}
	if bytes.Contains(store.data, []byte("private-run-file")) {
		t.Fatal("plaintext in storage")
	}
	r, err := runfilestore.Open(ctx, store, account, ref)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("round trip", err)
	}
	store.data[len(store.data)-1] ^= 1
	r, err = runfilestore.Open(ctx, store, account, ref)
	if err == nil {
		_, err = io.Copy(io.Discard, r)
		_ = r.Close()
	}
	if err == nil {
		t.Fatal("corrupt ciphertext accepted")
	}
}
func TestStoredFileRejectsLengthDigestAndEarlyStorageFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
		sha  string
		fail bool
	}{{"short", 4, digest([]byte("abc")), false}, {"long", 2, digest([]byte("abc")), false}, {"digest", 3, digest([]byte("bad")), false}, {"storage", 3, digest([]byte("abc")), true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			account, id := uuid.NewString(), uuid.NewString()
			key := runfile.Key(account, id, uuid.NewString())
			identity, _ := age.GenerateX25519Identity()
			ref := runfile.Ref{Descriptor: runfile.Descriptor{ID: id}, StorageKey: key, PrivateKey: identity.String()}
			if _, err := runfilestore.Put(ctx, &blobStore{fail: tc.fail}, account, ref, io.NopCloser(bytes.NewBufferString("abc")), tc.size, tc.sha); err == nil {
				t.Fatal("invalid transfer succeeded")
			}
		})
	}
}
func TestStoredFileCancellationClosesAndJoinsSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	account, id := uuid.NewString(), uuid.NewString()
	identity, _ := age.GenerateX25519Identity()
	ref := runfile.Ref{Descriptor: runfile.Descriptor{ID: id}, StorageKey: runfile.Key(account, id, uuid.NewString()), PrivateKey: identity.String()}
	reader, writer := io.Pipe()
	defer writer.Close()
	done := make(chan error, 1)
	go func() { _, err := runfilestore.Put(ctx, &blobStore{}, account, ref, reader, 100, ""); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation published")
		}
	case <-time.After(time.Second):
		t.Fatal("transfer worker did not join")
	}
}
