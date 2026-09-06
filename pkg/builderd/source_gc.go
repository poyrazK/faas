package builderd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const sourceObjectPrefix = "sources/"

// SourceGCSweepOnce removes source handoff objects that are older than maxAge.
// A source object remains protected while its build is queued or running. A
// missing build row is treated as an orphan only after the UUIDv7 age check,
// which prevents an apid crash between the upload and CreateBuildWithID from
// leaking the object forever.
//
// Source IDs are minted as UUIDv7 by apidsource. Unknown key shapes and older
// non-v7 keys are left alone so a cleanup pass cannot make a destructive guess
// about an object whose creation time is not encoded in its name.
func SourceGCSweepOnce(ctx context.Context, backend storage.StorageBackend, store state.Store, maxAge time.Duration, now time.Time, log *slog.Logger) (int, error) {
	if backend == nil {
		return 0, nil
	}
	if store == nil {
		return 0, errors.New("builderd: source gc: nil state store")
	}
	if maxAge <= 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	if log == nil {
		log = slog.Default()
	}

	lister, ok := backend.(storage.LocalArtifactLister)
	if !ok {
		return 0, errors.New("builderd: source gc: storage backend does not support List")
	}
	keys, err := lister.List(ctx, sourceObjectPrefix)
	if err != nil {
		return 0, fmt.Errorf("builderd: source gc: list: %w", err)
	}

	cutoff := now.Add(-maxAge)
	var candidates []string
	for _, key := range keys {
		buildID, createdAt, ok := sourceObjectBuild(key)
		if !ok || createdAt.After(cutoff) {
			continue
		}
		build, err := store.BuildByID(ctx, buildID)
		if err == nil {
			switch build.Status {
			case state.BuildQueued, state.BuildRunning:
				continue
			default:
				// Terminal rows are eligible once the source object has
				// crossed the retention boundary.
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			// Do not delete anything from a pass that cannot reliably
			// distinguish an orphan from a live build.
			return 0, fmt.Errorf("builderd: source gc: lookup build %s: %w", buildID, err)
		}
		candidates = append(candidates, key)
	}

	var deleteErrs []error
	deleted := 0
	for _, key := range candidates {
		if err := backend.Delete(ctx, key); err != nil {
			deleteErrs = append(deleteErrs, fmt.Errorf("%s: %w", key, err))
			continue
		}
		deleted++
		log.Info("builderd: removed expired source archive", "key", key, "cutoff", cutoff.Format(time.RFC3339))
	}
	if len(deleteErrs) != 0 {
		return deleted, fmt.Errorf("builderd: source gc: delete: %w", errors.Join(deleteErrs...))
	}
	return deleted, nil
}

// SourceGCSweepLoop runs source-object retention on a fixed cadence. It is
// separate from cache GC because source cleanup is registry/DB I/O while
// cache cleanup is local filesystem work.
func SourceGCSweepLoop(ctx context.Context, backend storage.StorageBackend, store state.Store, interval, maxAge time.Duration, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := SourceGCSweepOnce(ctx, backend, store, maxAge, time.Now(), log)
		if err != nil {
			log.Warn("builderd: source gc sweep", "err", err, "deleted", n)
		}
	}
}

func sourceObjectBuild(key string) (string, time.Time, bool) {
	if !strings.HasPrefix(key, sourceObjectPrefix) || !strings.HasSuffix(key, ".tar.gz") {
		return "", time.Time{}, false
	}
	idText := strings.TrimSuffix(strings.TrimPrefix(key, sourceObjectPrefix), ".tar.gz")
	u, err := uuid.Parse(idText)
	if err != nil || u.Version() != 7 {
		return "", time.Time{}, false
	}
	sec, nsec := u.Time().UnixTime()
	return idText, time.Unix(sec, nsec), true
}
