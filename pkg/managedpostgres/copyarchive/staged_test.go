// adr: 590
package copyarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func TestStagedArchiveAuthenticatesOnceAndLeavesNoNamedPlaintext(t *testing.T) {
	d, id, key, cipher, expected := retainedArchiveFixture(t)
	root := t.TempDir()
	b := &memoryArchiveBackend{objects: map[string][]byte{key: cipher}}
	s, err := StageRetained(t.Context(), b, key, d, []*age.X25519Identity{id}, expected, root, expected.PlainBytes, expected.CiphertextBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	info, err := s.file.Stat()
	if err != nil || info.Mode().Perm() != 0600 || info.Size() != expected.PlainBytes || !SameReceipt(s.receipt, expected) || b.gets != 1 {
		t.Fatalf("private staged input: %v", err)
	}
	if _, err := os.Stat(s.file.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plaintext still has a filesystem name: %v", err)
	}
	if files, err := os.ReadDir(root); err != nil || len(files) != 0 {
		t.Fatalf("scratch contains named data: %v", err)
	}
	d.Database.ACL[0] = "changed-original-acl"
	*d.Database.Locale = "changed-original-locale"
	if s.requirement.Database.ACL[0] == d.Database.ACL[0] || *s.requirement.Database.Locale == *d.Database.Locale {
		t.Fatal("staged requirement retains mutable caller metadata")
	}
	b.objects[key] = []byte("replaced-backend-input")
	data, err := io.ReadAll(s.file)
	if err != nil || int64(len(data)) != expected.PlainBytes || !bytes.HasPrefix(data, []byte("PGDMPprivate-row")) || b.gets != 1 {
		t.Fatalf("staged bytes depend on mutable storage: %v", err)
	}
	jsonData, _ := json.Marshal(s)
	for _, formatted := range []string{string(jsonData), fmt.Sprint(s), fmt.Sprintf("%#v", s)} {
		for _, private := range []string{s.file.Name(), d.Database.Name, "private-row"} {
			if strings.Contains(formatted, private) {
				t.Fatal("staged input exposes a private path or payload")
			}
		}
	}
	if err := s.Close(); err != nil || s.file != nil {
		t.Fatalf("close private input: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStagedArchiveRejectsTailDamageReceiptDriftAndBudgetsWithoutLeavingPlaintext(t *testing.T) {
	d, id, key, cipher, expected := retainedArchiveFixture(t)
	for _, tc := range []struct {
		name string
		edit func(*memoryArchiveBackend, *copyinventory.DatabaseExport, *Receipt, *int64, *int64)
		want error
		gets int
	}{
		{"changed_tail", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			b.objects[key][len(cipher)-1] ^= 1
		}, pgerrors.ErrConflict, 1},
		{"truncated_tail", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			b.objects[key] = b.objects[key][:len(cipher)-1]
		}, pgerrors.ErrConflict, 1},
		{"appended_tail", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			b.objects[key] = append(b.objects[key], 0)
			*c += 10
		}, pgerrors.ErrConflict, 1},
		{"missing", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			delete(b.objects, key)
		}, pgerrors.ErrNotFound, 1},
		{"storage_failure", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			b.getFailure = true
		}, pgerrors.ErrUnavailable, 1},
		{"digest_drift", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			r.CiphertextSHA256 = strings.Repeat("e", 64)
		}, pgerrors.ErrConflict, 1},
		{"count_drift", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			r.PlainBytes--
		}, pgerrors.ErrConflict, 1},
		{"sql_pin_drift", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			d.Database.OID++
		}, pgerrors.ErrInvalid, 0},
		{"plain_reservation", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) { *p-- }, pgerrors.ErrQuotaExceeded, 0},
		{"cipher_reservation", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) { *c-- }, pgerrors.ErrQuotaExceeded, 0},
		// A smaller forged expected count cannot evade the actual spool budget.
		{"actual_plain_budget", func(b *memoryArchiveBackend, d *copyinventory.DatabaseExport, r *Receipt, p, c *int64) {
			*p = 5
			r.PlainBytes = 5
		}, pgerrors.ErrQuotaExceeded, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &memoryArchiveBackend{objects: map[string][]byte{key: bytes.Clone(cipher)}}
			changed, receipt, plain, ciphertext := d, expected, expected.PlainBytes, expected.CiphertextBytes
			tc.edit(b, &changed, &receipt, &plain, &ciphertext)
			root := t.TempDir()
			if s, err := StageRetained(t.Context(), b, key, changed, []*age.X25519Identity{id}, receipt, root, plain, ciphertext); s != nil || !errors.Is(err, tc.want) || b.gets != tc.gets || strings.Contains(fmt.Sprint(err), "private-storage") {
				t.Fatalf("invalid staging returned authority: %v", err)
			}
			if files, err := os.ReadDir(root); err != nil || len(files) != 0 {
				t.Fatalf("failed staging left named plaintext: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b := &memoryArchiveBackend{}
	if s, err := StageRetained(ctx, b, key, d, []*age.X25519Identity{id}, expected, t.TempDir(), expected.PlainBytes, expected.CiphertextBytes); s != nil || !errors.Is(err, context.Canceled) || b.gets != 0 {
		t.Fatalf("cancelled staging accessed storage: %v", err)
	}
}

type trackedArchiveBody struct {
	io.Reader
	closed bool
}

func (r *trackedArchiveBody) Close() error { r.closed = true; return nil }

type getReplyErrorBackend struct{ body *trackedArchiveBody }

func (b getReplyErrorBackend) Put(context.Context, string, io.Reader) error {
	return pgerrors.ErrUnsupported
}
func (b getReplyErrorBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return b.body, errors.New("private-storage-error")
}

type shortStagingWriter struct{}

func (shortStagingWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestRetainedPrivateStagingClosesStorageAndSanitizesOutputFailures(t *testing.T) {
	d, id, key, cipher, expected := retainedArchiveFixture(t)
	body := &trackedArchiveBody{Reader: bytes.NewReader(cipher)}
	if r, err := ReadBack(t.Context(), getReplyErrorBackend{body}, key, d, []*age.X25519Identity{id}, expected.CiphertextBytes); !errors.Is(err, pgerrors.ErrUnavailable) || r != (Receipt{}) || !body.closed {
		t.Fatalf("storage error left an open body: %v", err)
	}
	for _, output := range []io.Writer{failingArchiveWriter{}, shortStagingWriter{}} {
		b := &memoryArchiveBackend{objects: map[string][]byte{key: cipher}}
		if r, err := readRetainedTo(t.Context(), b, key, d, []*age.X25519Identity{id}, expected.CiphertextBytes, expected.PlainBytes, output); !errors.Is(err, pgerrors.ErrUnavailable) || r != (Receipt{}) || strings.Contains(fmt.Sprint(err), "private-secret") {
			t.Fatalf("output failure returned receipt or diagnostics: %v", err)
		}
	}
}
