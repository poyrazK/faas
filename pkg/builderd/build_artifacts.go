package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildexport"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// publishBuildExport publishes immutable bytes before the claim-fenced state
// commit. An interrupted or stale attempt leaves only a retention-aged orphan.
func (b *Builderd) publishBuildExport(ctx context.Context, app state.App, acct state.Account, result BuildResult) (state.BuildArtifactReference, error) {
	if app.ComputeRegion == "" {
		return state.BuildArtifactReference{}, nil
	}
	if b.sourceStorage == nil {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: regional export storage is required")
	}
	lim, known := api.LimitsFor(acct.Plan)
	if !known || result.LayerBytes <= 0 || result.LayerBytes > int64(lim.AppLayerMaxMB)<<20 {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: export exceeds plan limit")
	}
	lease, _, err := buildexport.AcquireArtifact(result.LayerPath)
	if err != nil {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: lease export: %w", err)
	}
	defer func() { _ = lease.Close() }()
	f, err := buildexport.OpenArtifact(result.LayerPath)
	if err != nil {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: open export: %w", err)
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, result.LayerBytes+1))
	if err != nil {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: hash export: %w", err)
	}
	if n != result.LayerBytes {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: export size changed")
	}
	if err := ctx.Err(); err != nil {
		return state.BuildArtifactReference{}, err
	}
	publication, err := uuid.NewV7()
	if err != nil {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: export publication ID: %w", err)
	}
	ref := state.BuildArtifactReference{SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: n}
	ref.Key = region.BuildExportKey(app.ComputeRegion, publication.String(), ref.SHA256)
	if err := storage.ValidateArtifactRoute(b.sourceStorage, ref.Key); err != nil {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: export route: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: rewind export: %w", err)
	}
	hash.Reset()
	counter := &exportByteCounter{}
	r := io.TeeReader(io.LimitReader(f, ref.Bytes+1), io.MultiWriter(hash, counter))
	if err := b.sourceStorage.Put(ctx, ref.Key, r); err != nil {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: publish export: %w", err)
	}
	if counter.bytes != ref.Bytes || hex.EncodeToString(hash.Sum(nil)) != ref.SHA256 {
		return state.BuildArtifactReference{}, fmt.Errorf("builderd: published export changed or was not fully consumed")
	}
	return ref, nil
}

type exportByteCounter struct{ bytes int64 }

func (c *exportByteCounter) Write(p []byte) (int, error) {
	c.bytes += int64(len(p))
	return len(p), nil
}

// BuildArtifactGCSweepOnce plans the complete regional inventory before any
// deletion. Active materializations and fresh uncommitted uploads are retained.
func BuildArtifactGCSweepOnce(ctx context.Context, backend storage.StorageBackend, store state.Store, maxAge time.Duration, now time.Time) (int, error) {
	if backend == nil || maxAge <= 0 {
		return 0, nil
	}
	refs, ok := store.(state.BuildArtifactReferenceStore)
	if !ok {
		return 0, fmt.Errorf("builderd: export gc: retained reference lookup required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	keys, err := storage.ListArtifactKeys(ctx, backend, region.BuildExportPrefix)
	if err != nil {
		return 0, fmt.Errorf("builderd: export gc: inventory: %w", err)
	}
	var candidates []string
	for _, key := range keys {
		_, publication, _, err := region.ParseBuildExportKey(key)
		if err != nil {
			continue
		}
		id, _ := uuid.Parse(publication)
		sec, nsec := id.Time().UnixTime()
		if time.Unix(sec, nsec).After(now.Add(-maxAge)) {
			continue
		}
		active, err := refs.BuildArtifactReferencedForMaterialization(ctx, key)
		if err != nil {
			return 0, fmt.Errorf("builderd: export gc: reference lookup: %w", err)
		}
		if !active {
			candidates = append(candidates, key)
		}
	}
	deleted := 0
	for _, key := range candidates {
		if err := backend.Delete(ctx, key); err != nil {
			return deleted, fmt.Errorf("builderd: export gc: delete: %w", err)
		}
		deleted++
	}
	return deleted, nil
}
