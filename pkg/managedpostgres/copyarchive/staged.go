package copyarchive

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// StagedArchive keeps authenticated plaintext in an unlinked private file.
// Only StageRetained can construct it. It grants no target SQL placement,
// durable import dispatch, cluster-global completion or stage readiness.
type StagedArchive struct {
	mu          sync.Mutex
	file        *os.File
	requirement copyinventory.DatabaseExport
	receipt     Receipt
	attempted   bool
}

func (*StagedArchive) String() string     { return "private staged PostgreSQL archive" }
func (s *StagedArchive) GoString() string { return s.String() }
func (s *StagedArchive) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ PrivateArchive bool }{true})
}

func validRetainedReceipt(d copyinventory.DatabaseExport, r Receipt) bool {
	digest, err := hex.DecodeString(r.CiphertextSHA256)
	return validRequirement(d) && r.Scope.Equal(d.Scope) && r.InventoryFingerprint == d.InventoryFingerprint && r.SourceDatabaseOID == d.Database.OID &&
		r.PlainBytes >= 5 && r.PlainBytes <= api.PostgresCopyArchiveMaxBytes && r.CiphertextBytes > r.PlainBytes && r.CiphertextBytes <= api.PostgresCopyArchiveCiphertextMaxBytes &&
		err == nil && len(digest) == 32 && hex.EncodeToString(digest) == r.CiphertextSHA256
}

func copyRequirement(d copyinventory.DatabaseExport) (copyinventory.DatabaseExport, error) {
	data, err := json.Marshal(databaseHeader(d))
	if err != nil {
		return copyinventory.DatabaseExport{}, pgerrors.ErrInvalid
	}
	var h header
	if json.Unmarshal(data, &h) != nil {
		return copyinventory.DatabaseExport{}, pgerrors.ErrInvalid
	}
	return copyinventory.DatabaseExport{Scope: h.Scope, InventoryFingerprint: h.InventoryFingerprint, Database: h.Database, CapturedAllowConnections: h.CapturedAllowConnections,
		AuthenticatedReaderDatabase: h.ReaderDatabase, AuthenticatedReaderRoleOID: h.ReaderRoleOID}, nil
}

// StageRetained authenticates the retained header, every age payload byte and
// exact ciphertext length/hash before plaintext may reach any target. It reads
// the backend once: a later key replacement cannot substitute the staged input.
// The caller freezes the backend/key and reserves private local spool capacity.
// Limits are bounded by the existing central plaintext/ciphertext ceilings.
func StageRetained(ctx context.Context, backend Backend, key string, d copyinventory.DatabaseExport, identities []*age.X25519Identity, expected Receipt,
	scratchRoot string, maxPlainBytes, maxCipherBytes int64) (*StagedArchive, error) {
	if backend == nil || !validRetainedReceipt(d, expected) || !validOwnedKey(d, key) || !filepath.IsAbs(scratchRoot) ||
		maxPlainBytes < 5 || maxPlainBytes > api.PostgresCopyArchiveMaxBytes || maxCipherBytes < 1 || maxCipherBytes > api.PostgresCopyArchiveCiphertextMaxBytes {
		return nil, pgerrors.ErrInvalid
	}
	if expected.PlainBytes > maxPlainBytes || expected.CiphertextBytes > maxCipherBytes {
		return nil, pgerrors.ErrQuotaExceeded
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	retained, err := copyRequirement(d)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(scratchRoot, "gregale-postgres-import-")
	if err != nil {
		return nil, pgerrors.ErrUnavailable
	}
	// Unlink before writing the first plaintext byte. Process termination then
	// releases the inode automatically; no named plaintext survives a crash.
	if err := os.Remove(file.Name()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, pgerrors.ErrUnavailable
	}
	s := &StagedArchive{file: file, requirement: retained}
	success := false
	defer func() {
		if !success {
			_ = s.Close()
		}
	}()
	actual, err := readRetainedTo(ctx, backend, key, retained, identities, maxCipherBytes, maxPlainBytes, file)
	if err != nil {
		return nil, err
	}
	if !SameReceipt(expected, actual) {
		return nil, pgerrors.ErrConflict
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, pgerrors.ErrUnavailable
	}
	s.receipt = actual
	success = true
	return s, nil
}

func (s *StagedArchive) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	file := s.file
	s.file = nil
	if file.Close() != nil {
		return pgerrors.ErrUnavailable
	}
	return nil
}
