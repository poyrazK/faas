package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// StorageObjectDeletion is one requested delete of a shared-store object
// (ADR-20261115180000): a compute node in defer mode records it, the control
// plane's object GC decides it.
type StorageObjectDeletion struct {
	StorageKey  string
	Reason      string
	RequestedBy string
	RequestedAt time.Time
	Attempts    int
	// Generation, when positive, is the one object generation to delete
	// (an exclusive-artifact retirement, slice 2); 0 deletes the key.
	Generation int64
}

// Outcomes of a decided object deletion request.
const (
	StorageObjectDeleted = "deleted"
	StorageObjectRefused = "refused"
)

// StorageObjectDeletionRequester records object deletion requests (the
// compute side).
type StorageObjectDeletionRequester interface {
	RequestStorageObjectDeletion(ctx context.Context, key, reason string) error
}

// StorageObjectRetirementRequester records generation-conditioned retirements
// of exclusive artifacts (ADR-20261115180000 slice 2).
type StorageObjectRetirementRequester interface {
	RequestStorageObjectRetirement(ctx context.Context, key string, generation int64, reason string) error
}

// StorageObjectDeletionStore is the control plane's side: due requests,
// their outcomes and the reference check.
type StorageObjectDeletionStore interface {
	DueStorageObjectDeletions(ctx context.Context, now time.Time, limit int) ([]StorageObjectDeletion, error)
	DecideStorageObjectDeletion(ctx context.Context, d StorageObjectDeletion, outcome, detail string) error
	RetryStorageObjectDeletion(ctx context.Context, d StorageObjectDeletion, next time.Time, detail string) error
	// StorageObjectReferenced reports whether a live row still names the
	// object, and the key's class. An unclassified key is always referenced:
	// the control plane deletes only objects it knows how to check.
	StorageObjectReferenced(ctx context.Context, key string) (referenced bool, class string, err error)
}

// Classes of shared-store keys the object GC knows how to check.
const (
	StorageObjectClassSnapshot = "snapshot"
	StorageObjectClassLayer    = "layer"
	StorageObjectClassPlatform = "platform"
	// StorageObjectClassSource is a build's uploaded source archive
	// (sources/<build>.tar.gz, ADR-20261117190000).
	StorageObjectClassSource       = "source"
	StorageObjectClassUnclassified = "unclassified"
)

// encryptedTwinSuffix is crashcrypt.Suffix (ADR-733 sealed objects); state
// cannot import crashcrypt.
const encryptedTwinSuffix = ".age"

// ClassifyStorageObject returns a key's class and, for a snapshot object,
// the memory key of its capture and (for a v2 capture) its directory.
func ClassifyStorageObject(key string) (class, memKey, family string) {
	base := strings.TrimSuffix(key, encryptedTwinSuffix)
	switch {
	case strings.HasPrefix(base, "base/"), strings.HasPrefix(base, "kernel/"):
		return StorageObjectClassPlatform, "", ""
	case strings.HasPrefix(base, "apps/"), strings.HasPrefix(base, "layers/"), strings.HasPrefix(base, "jobs/"):
		return StorageObjectClassLayer, "", ""
	case strings.HasPrefix(key, "sources/"):
		return StorageObjectClassSource, "", ""
	case !strings.HasPrefix(base, "snap/"):
		return StorageObjectClassUnclassified, "", ""
	}
	parts := strings.Split(base, "/")
	if len(parts) < 3 || parts[1] == "" {
		return StorageObjectClassUnclassified, "", ""
	}
	rest := parts[2:]
	prefix := "snap/" + parts[1] + "/"
	if rest[0] == "warm" {
		prefix, rest = prefix+"warm/", rest[1:]
	}
	if len(rest) == 4 && rest[0] == "captures" && rest[2] == "v2" && rest[1] != "" && rest[3] != "" {
		family = prefix + "captures/" + rest[1] + "/v2/"
		return StorageObjectClassSnapshot, family + "mem", family
	}
	if len(rest) == 1 && rest[0] != "" {
		return StorageObjectClassSnapshot, prefix + "mem", ""
	}
	return StorageObjectClassUnclassified, "", ""
}

func (s *PgStore) RequestStorageObjectDeletion(ctx context.Context, key, reason string) error {
	reason = safetext.Truncate(reason, 200)
	if err := sqlc.New().RequestStorageObjectDeletion(ctx, s.pool, sqlc.RequestStorageObjectDeletionParams{StorageKey: key, Reason: reason}); err != nil {
		return fmt.Errorf("state: request deletion of %q: %w", key, mapErr(err))
	}
	return nil
}

func (s *PgStore) RequestStorageObjectRetirement(ctx context.Context, key string, generation int64, reason string) error {
	if generation <= 0 {
		return ErrInvalidArgument
	}
	reason = safetext.Truncate(reason, 200)
	if err := sqlc.New().RequestStorageObjectDeletion(ctx, s.pool, sqlc.RequestStorageObjectDeletionParams{StorageKey: key, Reason: reason,
		Generation: pgtype.Int8{Int64: generation, Valid: true}}); err != nil {
		return fmt.Errorf("state: request retirement of %q generation %d: %w", key, generation, mapErr(err))
	}
	return nil
}

func (s *PgStore) DueStorageObjectDeletions(ctx context.Context, now time.Time, limit int) ([]StorageObjectDeletion, error) {
	rows, err := sqlc.New().DueStorageObjectDeletions(ctx, s.pool, sqlc.DueStorageObjectDeletionsParams{
		Now: pgtype.Timestamptz{Time: now, Valid: true}, MaxRows: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]StorageObjectDeletion, 0, len(rows))
	for _, r := range rows {
		out = append(out, StorageObjectDeletion{StorageKey: r.StorageKey, Reason: r.Reason, RequestedBy: r.RequestedBy,
			RequestedAt: r.RequestedAt.Time, Attempts: int(r.Attempts), Generation: r.Generation.Int64})
	}
	return out, nil
}

func (s *PgStore) DecideStorageObjectDeletion(ctx context.Context, d StorageObjectDeletion, outcome, detail string) error {
	if outcome != StorageObjectDeleted && outcome != StorageObjectRefused {
		return ErrInvalidArgument
	}
	_, err := sqlc.New().DecideStorageObjectDeletion(ctx, s.pool, sqlc.DecideStorageObjectDeletionParams{
		State: outcome, LastError: truncateDetail(detail), StorageKey: d.StorageKey,
		RequestedAt: pgtype.Timestamptz{Time: d.RequestedAt, Valid: true}})
	return mapErr(err)
}

func (s *PgStore) RetryStorageObjectDeletion(ctx context.Context, d StorageObjectDeletion, next time.Time, detail string) error {
	_, err := sqlc.New().RetryStorageObjectDeletion(ctx, s.pool, sqlc.RetryStorageObjectDeletionParams{
		LastError: truncateDetail(detail), NextAttemptAt: pgtype.Timestamptz{Time: next, Valid: true},
		StorageKey: d.StorageKey, RequestedAt: pgtype.Timestamptz{Time: d.RequestedAt, Valid: true}})
	return mapErr(err)
}

func (s *PgStore) StorageObjectReferenced(ctx context.Context, key string) (bool, string, error) {
	class, memKey, family := ClassifyStorageObject(key)
	base := strings.TrimSuffix(key, encryptedTwinSuffix)
	q := sqlc.New()
	switch class {
	case StorageObjectClassSnapshot:
		ref, err := q.SnapshotObjectReferenced(ctx, s.pool, sqlc.SnapshotObjectReferencedParams{StorageKey: base, MemKey: memKey, Family: family})
		if err != nil {
			return true, class, mapErr(err)
		}
		return !ref.Valid || ref.Bool, class, nil
	case StorageObjectClassLayer:
		ref, err := q.LayerArtifactHasReferences(ctx, s.pool, sqlc.LayerArtifactHasReferencesParams{
			StorageKey: base, RollbackGenerations: int32(api.SnapshotRollbackRetentionDeployments)})
		if err != nil {
			return true, class, mapErr(err)
		}
		if !ref.Valid || ref.Bool {
			return true, class, nil
		}
		if retained, err := s.OperationJobImageRetained(ctx, base); retained || err != nil {
			return true, class, err
		}
		if !storagekey.IsJobLayer(base) {
			return false, class, nil
		}
		// A job's published image, or any attempt of a job still
		// materializing (its worker may be writing an unpublished key).
		job, _ := storagekey.JobLayerJobID(base)
		jobUUID, err := uuid.Parse(job)
		if err != nil {
			return true, class, nil //nolint:nilerr // a key IsJobLayer accepted always parses
		}
		jobRef, err := q.JobArtifactReferenced(ctx, s.pool, sqlc.JobArtifactReferencedParams{JobID: pgtype.UUID{Bytes: jobUUID, Valid: true}, StorageKey: base})
		if err != nil {
			return true, class, mapErr(err)
		}
		return jobRef, class, nil
	case StorageObjectClassSource:
		return s.sourceObjectReferenced(ctx, key)
	default:
		return true, class, nil
	}
}

func truncateDetail(s string) string {
	if len(s) > 1000 {
		return s[:1000]
	}
	return s
}

// SourceOrphanGrace is how long a source archive with no build row is kept:
// apid uploads sources/<build>.tar.gz before it creates the build, so an
// apid crash in between leaves an orphan (builderd's source GC rule).
const SourceOrphanGrace = 24 * time.Hour

// sourceObjectReferenced keeps a source archive while its build is queued or
// running, and keeps any whose name does not carry a UUIDv7 creation time or
// that is younger than SourceOrphanGrace.
func (s *PgStore) sourceObjectReferenced(ctx context.Context, key string) (bool, string, error) {
	const class = StorageObjectClassSource
	name, ok := strings.CutSuffix(strings.TrimPrefix(key, "sources/"), ".tar.gz")
	id, err := uuid.Parse(name)
	if !ok || err != nil || id.Version() != 7 || id.String() != name {
		return true, class, nil //nolint:nilerr // a name without a UUIDv7 creation time is kept, not an error
	}
	sec, nsec := id.Time().UnixTime()
	if time.Since(time.Unix(sec, nsec)) < SourceOrphanGrace {
		return true, class, nil
	}
	ref, err := sqlc.New().BuildSourceReferenced(ctx, s.pool, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		return true, class, mapErr(err)
	}
	return ref, class, nil
}
