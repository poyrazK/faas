package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
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
	StorageObjectClassSnapshot     = "snapshot"
	StorageObjectClassLayer        = "layer"
	StorageObjectClassPlatform     = "platform"
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
	if len(reason) > 200 {
		reason = reason[:200]
	}
	if err := sqlc.New().RequestStorageObjectDeletion(ctx, s.pool, sqlc.RequestStorageObjectDeletionParams{StorageKey: key, Reason: reason}); err != nil {
		return fmt.Errorf("state: request deletion of %q: %w", key, mapErr(err))
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
			RequestedAt: r.RequestedAt.Time, Attempts: int(r.Attempts)})
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
		retained, err := s.OperationJobImageRetained(ctx, base)
		return retained || err != nil, class, err
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
