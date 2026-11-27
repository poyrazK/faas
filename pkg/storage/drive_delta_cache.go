package storage

// Node cache side of snapshot drive deltas (ADR-20261115213230): the cache
// uploads a delta in place of the drive and rebuilds the drive when it
// fetches one, so every reader of the cache sees whole drives.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// SetDriveDeltas makes Put upload a snapshot drive handed over as a
// DriveDeltaSource as a delta against its layer. Every node that reads the
// parent must run a release that decodes deltas before any node writes one;
// fetching decodes them whether or not this is set.
func (c *LocalCacheBackend) SetDriveDeltas(enabled bool) {
	c.driveDeltas.Store(enabled)
}

// DriveDeltas reports whether Put writes drive deltas.
func (c *LocalCacheBackend) DriveDeltas() bool { return c.driveDeltas.Load() }

// isDriveDeltaPayload reports whether path, a local parent's file for key,
// holds a drive delta rather than the drive. The cache must not hand that
// file out as the drive: a VM restored from it boots the encoded payload
// (a 1.6 MB "disk"). Only the cache's own entry or a fetch that rebuilds the
// delta is the drive. It reads the delta's magic, whether or not deltas are
// on now, since deltas written earlier stay in the parent. It fails closed:
// a drive file it cannot read (out of descriptors, an I/O error) counts as a
// delta, so the caller falls through to the cache entry or a fetch rather
// than risk handing out the payload. The cost is one open and an 8-byte read
// of a local file, for snapshot drive keys only.
func isDriveDeltaPayload(key, path string) bool {
	if !isSnapshotDriveKey(key) {
		return false
	}
	f, err := os.Open(path) //nolint:forbidigo // path is the local parent's own file for key.
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	magic := make([]byte, len(driveDeltaMagic))
	n, err := io.ReadFull(f, magic)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return n == len(magic) && string(magic) == driveDeltaMagic
}

// putParent uploads the spooled artifact for key. A snapshot drive with a
// layer goes up as a delta when deltas are on and the layer is on this node;
// anything else, or any failure to build the delta, uploads the whole file.
func (c *LocalCacheBackend) putParent(ctx context.Context, key, parentKey string, spool *os.File, src io.Reader) error {
	if source, ok := src.(DriveDeltaSource); ok && c.driveDeltas.Load() && isSnapshotDriveKey(key) && source.BaseKey != "" {
		delta, err := c.buildDriveDelta(ctx, key, spool, source.BaseKey)
		if err == nil {
			defer func() {
				_ = delta.Close()
				_ = os.Remove(delta.Name())
			}()
			if err := c.parent.Put(ctx, parentKey, delta); err != nil {
				return err
			}
			_, err := spool.Seek(0, io.SeekStart)
			return err
		}
		slog.Warn("storage: cache: drive delta unavailable; uploading the whole drive", "key", key, "base", source.BaseKey, "err", err)
		if _, err := spool.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	return c.parent.Put(ctx, parentKey, spool)
}

// buildDriveDelta writes the delta of drive against the cached copy of
// baseKey to a temporary file beside drive and returns it rewound.
func (c *LocalCacheBackend) buildDriveDelta(ctx context.Context, key string, drive *os.File, baseKey string) (*os.File, error) {
	if err := validateKey(baseKey); err != nil {
		return nil, err
	}
	if isSnapshotDriveKey(baseKey) {
		return nil, fmt.Errorf("base %q is itself a snapshot drive", baseKey)
	}
	basePath, local, err := c.LocalPath(baseKey)
	if err != nil {
		return nil, err
	}
	if !local {
		return nil, fmt.Errorf("base %q is not on this node", baseKey)
	}
	base, err := os.Open(basePath) //nolint:forbidigo // cache-resolved artifact path.
	if err != nil {
		return nil, err
	}
	defer func() { _ = base.Close() }()
	out, err := os.CreateTemp(filepath.Dir(drive.Name()), ".faas-drive-delta-*")
	if err != nil {
		return nil, err
	}
	stats, err := encodeDriveDelta(ctx, out, drive, base, baseKey)
	if err == nil {
		err = out.Sync()
	}
	if err == nil {
		_, err = out.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return nil, err
	}
	slog.Debug("storage: cache: drive delta", "key", key, "base", baseKey,
		"data_blocks", stats.DataBlocks, "zero_blocks", stats.ZeroBlocks)
	return out, nil
}

// fetchedDrive returns the reader to install for a snapshot drive read from
// the parent: the body itself, or the drive rebuilt from a delta.
func (c *LocalCacheBackend) fetchedDrive(ctx context.Context, key string, body io.Reader) (io.Reader, func(), error) {
	br := bufio.NewReaderSize(body, 1<<20)
	magic, err := br.Peek(len(driveDeltaMagic))
	if err != nil || string(magic) != driveDeltaMagic {
		// A short or unreadable head is not a delta; the normal fill
		// reports any read error.
		return br, func() {}, nil //nolint:nilerr // the fill reads br again and reports it.
	}
	rebuilt, err := c.rebuildDriveDelta(ctx, key, br)
	if err != nil {
		return nil, nil, err
	}
	return rebuilt, func() {
		_ = rebuilt.Close()
		_ = os.Remove(rebuilt.Name())
	}, nil
}

// rebuildDriveDelta clones the delta's base from this cache (fetching it when
// needed), checks it is the layer the delta was made against, and applies the
// delta. The result is a rewound temporary file in the cache directory.
func (c *LocalCacheBackend) rebuildDriveDelta(ctx context.Context, key string, r *bufio.Reader) (*os.File, error) {
	h, err := readDriveDeltaHeader(r)
	if err != nil {
		return nil, err
	}
	if isSnapshotDriveKey(h.baseKey) {
		return nil, fmt.Errorf("storage: drive delta %q: base %q is a snapshot drive", key, h.baseKey)
	}
	basePath, local, err := c.LocalPath(h.baseKey)
	if err == nil && !local {
		rc, ferr := c.fetch(ctx, h.baseKey, c.parentKey(h.baseKey), false)
		if ferr != nil {
			return nil, fmt.Errorf("storage: drive delta %q: fetch base: %w", key, ferr)
		}
		_ = rc.Close()
		basePath, local, err = c.LocalPath(h.baseKey)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: drive delta %q: resolve base %q: %w", key, h.baseKey, err)
	}
	if !local {
		return nil, fmt.Errorf("storage: drive delta %q: base %q is not cached", key, h.baseKey)
	}
	base, err := os.Open(basePath) //nolint:forbidigo // cache-resolved artifact path.
	if err != nil {
		return nil, fmt.Errorf("storage: drive delta %q: open base: %w", key, err)
	}
	defer func() { _ = base.Close() }()
	info, err := base.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() != h.baseSize {
		return nil, fmt.Errorf("storage: drive delta %q: base %q is %d bytes, delta was made against %d: %w",
			key, h.baseKey, info.Size(), h.baseSize, errDriveDeltaBaseChanged)
	}
	digest, err := driveDataDigest(ctx, base, info.Size())
	if err != nil {
		return nil, fmt.Errorf("storage: drive delta %q: digest base: %w", key, err)
	}
	if digest != h.baseDigest {
		return nil, fmt.Errorf("storage: drive delta %q: base %q changed since the delta was made: %w", key, h.baseKey, errDriveDeltaBaseChanged)
	}

	path, _ := c.cacheFileFor(key)
	if err := ensureSharedCacheDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	out, err := os.CreateTemp(filepath.Dir(path), ".faas-drive-rebuild-*")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = out.Close()
			_ = os.Remove(out.Name())
		}
	}()
	if !cloneInto(out, base) {
		if _, err := base.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := copyArtifactContext(ctx, out, base, h.baseKey); err != nil {
			return nil, fmt.Errorf("storage: drive delta %q: copy base: %w", key, err)
		}
	}
	if err := applyDriveDelta(ctx, r, h, out); err != nil {
		return nil, fmt.Errorf("storage: drive delta %q: %w", key, err)
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ok = true
	return out, nil
}

// errDriveDeltaBaseChanged reports a delta whose layer no longer matches.
var errDriveDeltaBaseChanged = errors.New("storage: drive delta base changed")
