package copyarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type Produce func(context.Context, io.Writer) (Receipt, error)

// Backend is deliberately neutral to artifact drivers and control-plane state.
// Get maps a missing owned key to pgerrors.ErrNotFound. Put consumes the stream
// atomically and must not publish a key when its input ends with an error.
type Backend interface {
	Put(context.Context, string, io.Reader) error
	Get(context.Context, string) (io.ReadCloser, error)
}

func validOwnedKey(d copyinventory.DatabaseExport, key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "postgres-copies" || parts[1] != d.Scope.OperationID || !strings.HasSuffix(parts[2], ".age") {
		return false
	}
	owner := strings.TrimSuffix(parts[2], ".age")
	id, err := uuid.Parse(owner)
	return err == nil && id != uuid.Nil && id.String() == owner && owner != d.Scope.OperationID && owner != d.Scope.SourceDatabaseID && owner != d.Scope.CaptureDatabaseID
}

// ReadBack fully reads an owned encrypted artifact, authenticating its header
// and age payload before returning a length/hash receipt. This does not parse
// every pg_restore TOC entry or establish imported/global dataset readiness.
// The caller pins the backend and selects the retained recipient identity.
func ReadBack(ctx context.Context, backend Backend, key string, d copyinventory.DatabaseExport, identities []*age.X25519Identity, maxCipherBytes int64) (Receipt, error) {
	return readRetainedTo(ctx, backend, key, d, identities, maxCipherBytes, api.PostgresCopyArchiveMaxBytes, io.Discard)
}

// Every consumer authenticates the complete encrypted stream before its output
// may supply authority. Import staging writes only to a private unlinked file.
func readRetainedTo(ctx context.Context, backend Backend, key string, d copyinventory.DatabaseExport, identities []*age.X25519Identity, maxCipherBytes, maxPlainBytes int64, output io.Writer) (Receipt, error) {
	if backend == nil || output == nil || !validRequirement(d) || !validOwnedKey(d, key) || maxCipherBytes < 1 || maxCipherBytes > api.PostgresCopyArchiveCiphertextMaxBytes ||
		maxPlainBytes < 5 || maxPlainBytes > api.PostgresCopyArchiveMaxBytes {
		return Receipt{}, pgerrors.ErrInvalid
	}
	body, err := backend.Get(ctx, key)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		if ctx.Err() != nil {
			return Receipt{}, ctx.Err()
		}
		if errors.Is(err, pgerrors.ErrNotFound) {
			return Receipt{}, pgerrors.ErrNotFound
		}
		return Receipt{}, pgerrors.ErrUnavailable
	}
	if body == nil {
		return Receipt{}, pgerrors.ErrUnavailable
	}
	closed := false
	defer func() {
		if !closed {
			_ = body.Close()
		}
	}()
	hash := sha256.New()
	cipher := &countReader{reader: io.TeeReader(io.LimitReader(body, maxCipherBytes+1), hash), ctx: ctx}
	plain, err := Open(identities, d, cipher)
	if err != nil {
		return Receipt{}, retainedReadError(ctx, cipher.bytes, maxCipherBytes, err)
	}
	written := &retainedOutput{writer: output}
	n, err := io.Copy(written, io.LimitReader(plain, maxPlainBytes+1))
	if n > maxPlainBytes {
		return Receipt{}, pgerrors.ErrQuotaExceeded
	}
	if err != nil {
		if written.failed {
			if ctx.Err() != nil {
				return Receipt{}, ctx.Err()
			}
			return Receipt{}, pgerrors.ErrUnavailable
		}
		return Receipt{}, retainedReadError(ctx, cipher.bytes, maxCipherBytes, err)
	}
	// No unauthenticated trailing ciphertext may become part of a receipt.
	tail, err := io.Copy(io.Discard, cipher)
	if err != nil || tail != 0 || cipher.bytes > maxCipherBytes {
		return Receipt{}, retainedReadError(ctx, cipher.bytes, maxCipherBytes, pgerrors.ErrConflict)
	}
	closed = true
	if err := body.Close(); err != nil {
		return Receipt{}, pgerrors.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	return Receipt{Scope: d.Scope, InventoryFingerprint: d.InventoryFingerprint, SourceDatabaseOID: d.Database.OID,
		PlainBytes: n, CiphertextBytes: cipher.bytes, CiphertextSHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

type retainedOutput struct {
	writer io.Writer
	failed bool
}

func (w *retainedOutput) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if err != nil || n != len(p) {
		w.failed = true
		if err == nil {
			err = io.ErrShortWrite
		}
	}
	return n, err
}

func retainedReadError(ctx context.Context, observed, limit int64, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if observed > limit {
		return pgerrors.ErrQuotaExceeded
	}
	if errors.Is(err, pgerrors.ErrQuotaExceeded) {
		return pgerrors.ErrQuotaExceeded
	}
	return pgerrors.ErrConflict
}

func SameReceipt(a, b Receipt) bool {
	return a.Scope.Equal(b.Scope) && a.InventoryFingerprint == b.InventoryFingerprint && a.SourceDatabaseOID == b.SourceDatabaseOID &&
		a.PlainBytes == b.PlainBytes && a.CiphertextBytes == b.CiphertextBytes && a.CiphertextSHA256 == b.CiphertextSHA256
}

// Upload is dispatched only by the first durable owner claim. Callers recover
// uncertain writes through ReadBack; they must never repeat/overwrite the Put.
// The producer respects cancellation and returns only a finalized Export result.
// Put errors after commit can recover through independently verified readback.
func Upload(ctx context.Context, backend Backend, key string, d copyinventory.DatabaseExport, identities []*age.X25519Identity, maxCipherBytes int64, produce Produce) (Receipt, error) {
	if backend == nil || produce == nil || !validRequirement(d) || !validOwnedKey(d, key) || maxCipherBytes < 1 || maxCipherBytes > api.PostgresCopyArchiveCiphertextMaxBytes {
		return Receipt{}, pgerrors.ErrInvalid
	}
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	budget := &boundedWriter{writer: writer, remaining: maxCipherBytes}
	type produced struct {
		receipt Receipt
		err     error
	}
	done := make(chan produced, 1)
	go func() {
		r, err := produce(uploadCtx, budget)
		if budget.err != nil {
			err = budget.err
		}
		if err != nil {
			_ = writer.CloseWithError(pgerrors.ErrUnavailable)
		} else {
			_ = writer.Close()
		}
		done <- produced{r, err}
	}()
	putErr := backend.Put(uploadCtx, key, reader)
	_ = reader.CloseWithError(pgerrors.ErrUnavailable)
	if putErr != nil {
		cancel()
	}
	var result produced
	select {
	case result = <-done:
	case <-ctx.Done():
		return Receipt{}, ctx.Err()
	}
	if ctx.Err() != nil {
		return Receipt{}, ctx.Err()
	}
	if result.err != nil {
		if errors.Is(result.err, pgerrors.ErrQuotaExceeded) {
			return Receipt{}, pgerrors.ErrQuotaExceeded
		}
		return Receipt{}, pgerrors.ErrUnavailable
	}
	verified, err := ReadBack(ctx, backend, key, d, identities, maxCipherBytes)
	if err != nil {
		return Receipt{}, err
	}
	if !SameReceipt(verified, result.receipt) {
		return Receipt{}, pgerrors.ErrConflict
	}
	return verified, nil
}

type countReader struct {
	reader io.Reader
	ctx    context.Context
	bytes  int64
}

func (r *countReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	return n, err
}
