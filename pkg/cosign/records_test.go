package cosign

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// dirStorage serves keys from files under a directory and resolves them as
// local paths, like the node cache.
type dirStorage struct{ root string }

func (d dirStorage) path(key string) string { return filepath.Join(d.root, filepath.FromSlash(key)) }

func (d dirStorage) Put(_ context.Context, key string, r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.path(key)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(d.path(key), body, 0o644)
}

func (d dirStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return os.OpenFile(d.path(key), os.O_RDONLY, 0)
}

func (d dirStorage) Delete(_ context.Context, key string) error { return os.Remove(d.path(key)) }

func (d dirStorage) LocalPath(key string) (string, bool, error) {
	_, err := os.Stat(d.path(key))
	return d.path(key), err == nil, nil
}

const (
	recordsLayerKey = "apps/test/layer.ext4"
	recordsSigKey   = "sigs/apps/test/layer.ext4.sig"
)

// signedDirLayer stores a signed 4 KiB layer and returns its storage and the
// verifying key's path.
func signedDirLayer(t *testing.T) (dirStorage, string) {
	t.Helper()
	ctx := context.Background()
	privPath, pubPath := keyPairTempDir(t)
	stor := dirStorage{root: t.TempDir()}
	layer := make([]byte, 4096)
	if _, err := rand.Read(layer); err != nil {
		t.Fatal(err)
	}
	if err := stor.Put(ctx, recordsLayerKey, bytes.NewReader(layer)); err != nil {
		t.Fatal(err)
	}
	signer, err := NewLocalSigner(privPath, stor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Sign(ctx, recordsLayerKey, recordsSigKey); err != nil {
		t.Fatal(err)
	}
	return stor, pubPath
}

// restartedVerifier is a new verifier over the same records, as after a
// schedd restart.
func restartedVerifier(t *testing.T, stor dirStorage, pubPath, recordsPath string) *LocalVerifier {
	t.Helper()
	v, err := NewLocalVerifier(pubPath, stor)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.RememberVerifications(recordsPath, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLocalVerifierRemembersVerificationsAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	stor, pubPath := signedDirLayer(t)
	recordsPath := filepath.Join(t.TempDir(), "layer-attestations.json")

	if err := restartedVerifier(t, stor, pubPath, recordsPath).Verify(ctx, recordsLayerKey, recordsSigKey); err != nil {
		t.Fatalf("first verification: %v", err)
	}
	fi, err := os.Stat(recordsPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("records file = %v, %v; want mode 0600", fi, err)
	}

	// The same file after a restart is not hashed again. Rewriting it in
	// place keeps its device, inode and size, so the verifier still trusts
	// the record: that is the proof it skipped the hash, and the documented
	// limit (an in-place write needs write access to the node cache).
	f, err := os.OpenFile(stor.path(recordsLayerKey), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("tampered"), 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := restartedVerifier(t, stor, pubPath, recordsPath).Verify(ctx, recordsLayerKey, recordsSigKey); err != nil {
		t.Fatalf("verification after a restart re-hashed the unchanged file: %v", err)
	}

	// A file the cache replaced is a different file: it is verified in full
	// and the tampered bytes are refused.
	body, err := os.ReadFile(stor.path(recordsLayerKey))
	if err != nil {
		t.Fatal(err)
	}
	replacement := stor.path(recordsLayerKey) + ".new"
	if err := os.WriteFile(replacement, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, stor.path(recordsLayerKey)); err != nil {
		t.Fatal(err)
	}
	err = restartedVerifier(t, stor, pubPath, recordsPath).Verify(ctx, recordsLayerKey, recordsSigKey)
	var p *api.Problem
	if !errors.As(err, &p) || p.Code != api.CodeSigInvalid {
		t.Fatalf("replaced tampered layer: err = %v, want sig_invalid", err)
	}
}

func TestAttestationRecordsHonourOnlyFreshRecordsUnderTheSameKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	id := fileIdentity{device: 1, inode: 2, size: 3}

	r, err := openAttestationRecords(path, "key-a", time.Hour, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.remember("layer", "sig", id); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		keyID string
		after time.Duration
		id    fileIdentity
		want  bool
	}{
		{name: "same key, file and age", keyID: "key-a", after: 59 * time.Minute, id: id, want: true},
		{name: "another signing key", keyID: "key-b", id: id},
		{name: "expired", keyID: "key-a", after: time.Hour, id: id},
		{name: "another inode", keyID: "key-a", id: fileIdentity{device: 1, inode: 9, size: 3}},
		{name: "another size", keyID: "key-a", id: fileIdentity{device: 1, inode: 2, size: 4}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			later := func() time.Time { return now.Add(tc.after) }
			reloaded, err := openAttestationRecords(path, tc.keyID, time.Hour, later)
			if err != nil {
				t.Fatal(err)
			}
			if got := reloaded.matches("layer", "sig", tc.id); got != tc.want {
				t.Fatalf("matches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAttestationRecordsRefuseAFileOthersCanWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := openAttestationRecords(path, "key", time.Hour, time.Now)
	if err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("open of a 0644 records file = %v, want a refusal", err)
	}
}

func TestAttestationRecordsTolerateAMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := openAttestationRecords(path, "key", time.Hour, time.Now)
	if err != nil || len(r.byKey) != 0 {
		t.Fatalf("malformed records = %v, %v; want an empty set", r, err)
	}
	if err := r.remember("layer", "sig", fileIdentity{device: 1, inode: 2, size: 3}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := openAttestationRecords(path, "key", time.Hour, time.Now)
	if err != nil || !reloaded.matches("layer", "sig", fileIdentity{device: 1, inode: 2, size: 3}) {
		t.Fatalf("rewritten records = %v, %v", reloaded, err)
	}
}

// A backend without local files streams the layer as before and records
// nothing: there is no file whose identity could stand for the bytes.
func TestLocalVerifierWithoutLocalFilesRecordsNothing(t *testing.T) {
	ctx := context.Background()
	privPath, pubPath := keyPairTempDir(t)
	stor := newMemStorage()
	if err := stor.Put(ctx, recordsLayerKey, bytes.NewReader([]byte("layer bytes"))); err != nil {
		t.Fatal(err)
	}
	signer, err := NewLocalSigner(privPath, stor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Sign(ctx, recordsLayerKey, recordsSigKey); err != nil {
		t.Fatal(err)
	}
	v, err := NewLocalVerifier(pubPath, stor)
	if err != nil {
		t.Fatal(err)
	}
	recordsPath := filepath.Join(t.TempDir(), "layer-attestations.json")
	if err := v.RememberVerifications(recordsPath, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(ctx, recordsLayerKey, recordsSigKey); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recordsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("records written without a local file: %v", err)
	}
}
