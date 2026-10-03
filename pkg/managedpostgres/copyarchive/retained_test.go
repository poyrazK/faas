// adr:375
package copyarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type memoryArchiveBackend struct {
	objects                              map[string][]byte
	putReplyLoss, putFailure, getFailure bool
	puts, gets                           int
}

func (b *memoryArchiveBackend) Put(ctx context.Context, key string, r io.Reader) error {
	b.puts++
	if b.putFailure {
		return errors.New("private-storage-diagnostic")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if b.objects == nil {
		b.objects = map[string][]byte{}
	}
	b.objects[key] = data
	if b.putReplyLoss {
		return errors.New("private-lost-put-reply")
	}
	return nil
}

func (b *memoryArchiveBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b.gets++
	if b.getFailure {
		return nil, errors.New("private-storage-diagnostic")
	}
	data, ok := b.objects[key]
	if !ok {
		return nil, pgerrors.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func retainedArchiveFixture(t *testing.T) (copyinventory.DatabaseExport, *age.X25519Identity, string, []byte, Receipt) {
	t.Helper()
	d := archiveRequirement()
	id, _ := age.GenerateX25519Identity()
	key := "postgres-copies/" + d.Scope.OperationID + "/" + uuid.NewString() + ".age"
	raw, _ := json.Marshal(databaseHeader(d))
	dump := append([]byte("PGDMP"), bytes.Repeat([]byte("private-row"), 19000)...)
	cipher := encryptArchivePayload(t, id, raw, dump, magic, uint32(len(raw)))
	digest := sha256.Sum256(cipher)
	receipt := Receipt{Scope: d.Scope, InventoryFingerprint: d.InventoryFingerprint, SourceDatabaseOID: d.Database.OID, PlainBytes: int64(len(dump)), CiphertextBytes: int64(len(cipher)), CiphertextSHA256: hex.EncodeToString(digest[:])}
	return d, id, key, cipher, receipt
}

func TestRetainedArchiveUploadAndCommittedStorageReplyLossReadBack(t *testing.T) {
	for _, replyLoss := range []bool{false, true} {
		t.Run(fmt.Sprint(replyLoss), func(t *testing.T) {
			d, id, key, cipher, expected := retainedArchiveFixture(t)
			b := &memoryArchiveBackend{putReplyLoss: replyLoss}
			calls := 0
			r, err := Upload(t.Context(), b, key, d, []*age.X25519Identity{id}, int64(len(cipher)), func(ctx context.Context, w io.Writer) (Receipt, error) {
				calls++
				_, err := w.Write(cipher)
				return expected, err
			})
			if err != nil || !SameReceipt(r, expected) || calls != 1 || b.puts != 1 || b.gets != 1 {
				t.Fatalf("first upload/readback: %v", err)
			}
			// Recovery consumes the already stored artifact with no producer/Put.
			if recovered, err := ReadBack(t.Context(), b, key, d, []*age.X25519Identity{id}, int64(len(cipher))); err != nil || !SameReceipt(recovered, expected) || b.puts != 1 {
				t.Fatalf("retained recovery: %v", err)
			}
		})
	}
}

func TestRetainedArchiveRejectsMissingCorruptOversizedAndReboundArtifacts(t *testing.T) {
	d, id, key, cipher, _ := retainedArchiveFixture(t)
	for _, tc := range []struct {
		name string
		edit func(*memoryArchiveBackend, *copyinventory.DatabaseExport, *int64)
		want error
	}{
		{"missing", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, max *int64) { delete(b.objects, key) }, pgerrors.ErrNotFound},
		{"unavailable", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, max *int64) { b.getFailure = true }, pgerrors.ErrUnavailable},
		{"tail_changed", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, max *int64) {
			b.objects[key][len(cipher)-1] ^= 1
		}, pgerrors.ErrConflict},
		{"tail_truncated", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, max *int64) {
			b.objects[key] = b.objects[key][:len(cipher)-1]
		}, pgerrors.ErrConflict},
		{"trailing_bytes", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, max *int64) {
			b.objects[key] = append(b.objects[key], []byte("unexpected")...)
			*max += 100
		}, pgerrors.ErrConflict},
		{"quota", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, max *int64) {
			*max = int64(len(cipher) - 1)
		}, pgerrors.ErrQuotaExceeded},
		{"database", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, max *int64) { d.Database.OID++ }, pgerrors.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &memoryArchiveBackend{objects: map[string][]byte{key: bytes.Clone(cipher)}}
			changed := d
			max := int64(len(cipher))
			tc.edit(b, &changed, &max)
			if r, err := ReadBack(t.Context(), b, key, changed, []*age.X25519Identity{id}, max); !errors.Is(err, tc.want) || r != (Receipt{}) || strings.Contains(fmt.Sprint(err), "private-storage") {
				t.Fatalf("invalid retained artifact: %v", err)
			}
		})
	}
	for _, badKey := range []string{"layers/other", "postgres-copies/" + uuid.NewString() + "/" + uuid.NewString() + ".age", "postgres-copies/" + d.Scope.OperationID + "/../other.age"} {
		b := &memoryArchiveBackend{}
		if _, err := ReadBack(t.Context(), b, badKey, d, []*age.X25519Identity{id}, int64(len(cipher))); !errors.Is(err, pgerrors.ErrInvalid) || b.gets != 0 {
			t.Fatalf("unowned key read: %v", err)
		}
	}
}

func TestRetainedArchiveFailedUploadNeverReturnsReceiptOrPublishesPartialInput(t *testing.T) {
	d, id, key, cipher, expected := retainedArchiveFixture(t)
	for _, tc := range []struct {
		name       string
		putFailure bool
		max        int64
		produce    Produce
		want       error
	}{
		{"storage", true, int64(len(cipher)), func(ctx context.Context, w io.Writer) (Receipt, error) {
			_, err := w.Write(cipher)
			return expected, err
		}, pgerrors.ErrUnavailable},
		{"producer", false, int64(len(cipher)), func(ctx context.Context, w io.Writer) (Receipt, error) {
			_, _ = w.Write(cipher[:100])
			return Receipt{}, errors.New("private-producer-secret")
		}, pgerrors.ErrUnavailable},
		{"quota", false, int64(len(cipher) - 1), func(ctx context.Context, w io.Writer) (Receipt, error) {
			_, err := w.Write(cipher)
			return expected, err
		}, pgerrors.ErrQuotaExceeded},
		{"wrapped_quota", false, int64(len(cipher)), func(ctx context.Context, w io.Writer) (Receipt, error) {
			_, _ = w.Write(cipher[:100])
			return Receipt{}, fmt.Errorf("private-producer-secret: %w", pgerrors.ErrQuotaExceeded)
		}, pgerrors.ErrQuotaExceeded},
		{"receipt", false, int64(len(cipher)), func(ctx context.Context, w io.Writer) (Receipt, error) {
			_, err := w.Write(cipher)
			bad := expected
			bad.CiphertextSHA256 = strings.Repeat("a", 64)
			return bad, err
		}, pgerrors.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &memoryArchiveBackend{putFailure: tc.putFailure}
			if r, err := Upload(t.Context(), b, key, d, []*age.X25519Identity{id}, tc.max, tc.produce); !errors.Is(err, tc.want) || r != (Receipt{}) || strings.Contains(fmt.Sprint(err), "private-") {
				t.Fatalf("failed upload receipt/diagnostic: %v", err)
			}
			if tc.name != "receipt" && len(b.objects) != 0 {
				t.Fatal("partial producer input was published")
			}
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	b := &memoryArchiveBackend{}
	if r, err := Upload(ctx, b, key, d, []*age.X25519Identity{id}, int64(len(cipher)), func(ctx context.Context, w io.Writer) (Receipt, error) { <-ctx.Done(); return Receipt{}, ctx.Err() }); !errors.Is(err, context.DeadlineExceeded) || r != (Receipt{}) || len(b.objects) != 0 {
		t.Fatalf("cancelled upload: %v", err)
	}
}

func TestRetainedArchiveUploadsRealPostgresDumpAndRecoversWithoutSource(t *testing.T) {
	f := newArchiveFixture(t)
	id, _ := age.GenerateX25519Identity()
	key := "postgres-copies/" + f.requirement.Scope.OperationID + "/" + uuid.NewString() + ".age"
	b := &memoryArchiveBackend{}
	r, err := Upload(t.Context(), b, key, f.requirement, []*age.X25519Identity{id}, 8<<20, func(ctx context.Context, w io.Writer) (Receipt, error) {
		return Export(ctx, f.source, f.requirement, f.pgDump, id.Recipient(), w, 4<<20)
	})
	if err != nil || r.PlainBytes < 1000 {
		t.Fatalf("real dump storage/readback: %v", err)
	}
	if err := f.source.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if recovered, err := ReadBack(t.Context(), b, key, f.requirement, []*age.X25519Identity{id}, 8<<20); err != nil || !SameReceipt(recovered, r) || b.puts != 1 {
		t.Fatalf("recovery contacted source or changed bytes: %v", err)
	}
}
