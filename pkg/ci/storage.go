package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrStorageUnavailable = errors.New("CI storage unavailable")

// PhysicalObjects must publish with create-only semantics. Retire atomically
// replaces data with a permanent, zero-byte tombstone, including absent objects.
// A delayed writer can therefore never recreate a retired payload.
type PhysicalObjects interface {
	Publish(context.Context, string, RuntimeObject, io.Reader) error
	Open(context.Context, string) (io.ReadCloser, error)
	Retire(context.Context, string) error
}

type storageKey struct {
	Account, Execution, Attempt, Kind, Name string
}

func parseStorageKey(key string) (storageKey, error) {
	parts := strings.Split(key, "/")
	if len(key) > api.CIStorageKeyMaxBytes || len(parts) < 8 || parts[0] != "ci" || parts[1] != "accounts" || parts[3] != "executions" || parts[5] != "attempts" ||
		!ValidID(parts[2]) || !ValidID(parts[4]) || !ValidID(parts[6]) {
		return storageKey{}, ErrStorageUnavailable
	}
	out := storageKey{Account: parts[2], Execution: parts[4], Attempt: parts[6], Name: strings.Join(parts[7:], "/")}
	switch {
	case len(parts) == 9 && parts[7] == "source" && strings.HasSuffix(parts[8], ".tar.gz"):
		digest := strings.TrimSuffix(parts[8], ".tar.gz")
		if !artifactDigestPattern.MatchString("sha256:" + digest) {
			return out, ErrStorageUnavailable
		}
		out.Kind = "source"
	case len(parts) == 9 && parts[7] == "output" && parts[8] == "logs.bin":
		out.Kind = "logs"
	case len(parts) >= 10 && parts[7] == "output" && parts[8] == "artifacts" && validArtifact(strings.Join(parts[9:], "/")) && !strings.ContainsAny(strings.Join(parts[9:], "/"), "*?["):
		out.Kind = "artifact"
	default:
		return out, ErrStorageUnavailable
	}
	return out, nil
}

// ManagedBlobs is the only production JobsBlobStore for CI. The journal owns
// quotas and I/O leases; no storage listing or customer-supplied physical key is
// used for recovery. Published descriptors and physical IDs are immutable.
type ManagedBlobs struct {
	Store    *PostgresStore
	Physical PhysicalObjects
}

func (b *ManagedBlobs) Put(ctx context.Context, key string, body io.Reader) error {
	parsed, err := parseStorageKey(key)
	if err != nil || b == nil || b.Store == nil || b.Physical == nil {
		return ErrStorageUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	file, object, err := spoolStorageBody(ctx, parsed, body)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	claim, err := b.Store.reserveStorageWrite(ctx, key, parsed, object)
	if err != nil || claim.Ready {
		return err
	}
	defer b.release(ctx, claim)
	if err := b.Physical.Publish(ctx, claim.PhysicalKey(), object, file); err != nil {
		return ErrStorageUnavailable
	}
	return b.Store.commitStorageWrite(ctx, claim)
}

func spoolStorageBody(ctx context.Context, key storageKey, body io.Reader) (*os.File, RuntimeObject, error) {
	bound := int64(api.CIArtifactMaxBytes)
	if key.Kind == "logs" {
		bound = api.CILogMaxBytesPerAttempt
	}
	file, err := os.CreateTemp("", "gregale-ci-storage-*")
	if err != nil {
		return nil, RuntimeObject{}, ErrStorageUnavailable
	}
	valid := false
	defer func() {
		if !valid {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()
	hash := sha256.New()
	count, err := copyJobsBlob(ctx, io.MultiWriter(file, hash), io.LimitReader(body, bound+1))
	object := RuntimeObject{Name: key.Name, Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Bytes: count}
	if err != nil || count > bound || key.Kind == "source" && (count == 0 || key.Name != "source/"+strings.TrimPrefix(object.Digest, "sha256:")+".tar.gz") {
		return nil, object, ErrStorageUnavailable
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, object, ErrStorageUnavailable
	}
	valid = true
	return file, object, nil
}

func (b *ManagedBlobs) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if _, err := parseStorageKey(key); err != nil || b == nil || b.Store == nil || b.Physical == nil {
		return nil, ErrStorageUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIJobsTransferSeconds)*time.Second)
	claim, err := b.Store.claimStorageRead(ctx, key)
	if err != nil {
		cancel()
		return nil, err
	}
	body, err := b.Physical.Open(ctx, claim.PhysicalKey())
	if err != nil || body == nil {
		b.release(ctx, claim)
		cancel()
		return nil, ErrStorageUnavailable
	}
	return &managedReader{ReadCloser: body, finish: func() { b.release(ctx, claim); cancel() }}, nil
}

type managedReader struct {
	io.ReadCloser
	finish func()
}

func (r *managedReader) Close() error {
	err := r.ReadCloser.Close()
	r.finish()
	return err
}

func (b *ManagedBlobs) release(ctx context.Context, claim storageClaim) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	_ = b.Store.releaseStorage(ctx, claim)
}

func (b *ManagedBlobs) SourceForAttempt(ctx context.Context, req JobsSourceRequest) (SourceArchive, bool, error) {
	archive, ready, err := b.Store.cachedStorageSource(ctx, req)
	if err != nil || ready || archive.Key == "" {
		return archive, ready, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	parsed, _ := parseStorageKey(archive.Key)
	claim, err := b.Store.reserveStorageWrite(ctx, archive.Key, parsed, RuntimeObject{Name: parsed.Name, Digest: archive.Digest, Bytes: archive.Bytes})
	if err != nil || claim.Ready {
		return archive, claim.Ready, err
	}
	defer b.release(ctx, claim)
	body, err := b.Physical.Open(ctx, claim.PhysicalKey())
	if errors.Is(err, ErrNotFound) {
		return SourceArchive{}, false, nil
	}
	if err != nil {
		return SourceArchive{}, false, ErrStorageUnavailable
	}
	defer func() { _ = body.Close() }()
	hash := sha256.New()
	count, err := copyJobsBlob(ctx, hash, io.LimitReader(body, archive.Bytes+1))
	if err != nil || count != archive.Bytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != archive.Digest {
		return SourceArchive{}, false, ErrStorageUnavailable
	}
	if err := b.Store.commitStorageWrite(ctx, claim); err != nil {
		return SourceArchive{}, false, err
	}
	return archive, true, nil
}

func (b *ManagedBlobs) Sweep(ctx context.Context) (int, error) {
	keys, err := b.Store.storageCandidates(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, key := range keys {
		claim, err := b.Store.claimStorageRetirement(ctx, key)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrLeaseBusy) {
			continue
		}
		if err != nil {
			return count, err
		}
		err = b.retire(ctx, claim)
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (b *ManagedBlobs) retire(ctx context.Context, claim storageClaim) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	defer b.release(ctx, claim)
	if err := b.Physical.Retire(ctx, claim.PhysicalKey()); err != nil {
		return ErrStorageUnavailable
	}
	return b.Store.commitStorageRetirement(ctx, claim)
}

func RunStorageRetention(ctx context.Context, cfg Config, blobs *ManagedBlobs, log *slog.Logger) {
	if !cfg.RetentionEnabled || blobs == nil {
		return
	}
	ticker := time.NewTicker(time.Duration(api.CIStorageCleanupSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, time.Duration(api.CIStorageCleanupSeconds)*time.Second)
			_, err := blobs.Sweep(sweepCtx)
			recordWorkerCycle(ctx, err)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Warn("CI storage retirement pending")
			}
		}
	}
}
