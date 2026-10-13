package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation completes only
// an authenticated private reservation that never acquired a creation journal.
// Even a planned native creation belongs to the qualified retirement protocol.
// Original adoption provenance is required even when source release is incomplete.
// Source holds and native release authority are never changed by local cleanup.
func (s *PgStore) ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(ctx context.Context, l ProjectEnvironmentCloneLease, id string) error {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) {
		return ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Synchronize before operation/app locks, matching configuration acquisition.
	if _, err = sqlc.New().LockCloneCheckpointUnusedReservationClock(ctx, tx); err != nil {
		return mapErr(err)
	}
	op, err := lockCloneWorkloadOperationTx(ctx, tx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return err
	}
	if op.Status != CloneOperationCompensating {
		return ErrConflict
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	q := sqlc.New()
	if _, err = q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
		return mapErr(err)
	}
	if _, err = readCheckpointObjectTargetTx(ctx, tx, op, id); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return err
	}
	if err = authenticateUnusedCheckpointAdoptionTx(ctx, tx, op, views); err != nil {
		return err
	}
	var appID string
	for _, v := range views {
		for _, b := range v.Buckets {
			if b.ID == id {
				if appID != "" {
					return ErrConflict
				}
				appID = v.AppID
			}
		}
	}
	if appID == "" {
		return ErrConflict
	}
	want, source, err := capturedCloneBucketReservation(op, views, appID, id)
	if err != nil {
		return err
	}
	manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, id)
	if err != nil {
		return ErrConflict
	}
	row, err := readCloneCheckpointReleaseTargetTx(ctx, tx, op, appID, manifest.TargetBucketID, true)
	if err != nil {
		return ErrConflict
	}
	bucket := objectBucketFromSQL(row)
	if row.LeaseToken.Valid || row.LeaseUntil.Valid {
		return ErrConflict
	}
	want.ID = bucket.ID
	want.PhysicalName = "gregale-" + strings.ReplaceAll(bucket.ID, "-", "")
	placement := bucket
	if placement.State == "deleted" {
		placement.State = "provisioning"
	}
	if validateCloneBucketReservation(want, placement, source) != nil || (bucket.State != "provisioning" && bucket.State != "deleted") {
		return ErrConflict
	}
	// Missing native authority is not enough if local access was ever published.
	if err = checkpointObjectRetirementWithdrawalTx(ctx, tx, op, ProjectEnvironmentCloneCheckpointObjectTargetPlan{SourceBucketID: id, TargetBucketID: bucket.ID, AppID: appID}); err != nil {
		return err
	}
	saved, err := q.ReadCloneCheckpointUnusedObjectReservation(ctx, tx, sqlc.ReadCloneCheckpointUnusedObjectReservationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id)})
	switch {
	case err == nil:
		var original ObjectBucket
		if json.Unmarshal(saved.Reservation, &original) != nil || !reflect.DeepEqual(original, want) || pgUUIDString(saved.TargetBucketID) != bucket.ID || !saved.CompletedAt.Valid || bucket.State != "deleted" {
			return ErrConflict
		}
	case errors.Is(mapErr(err), ErrNotFound):
		if bucket.State != "provisioning" {
			return ErrConflict
		}
		raw, err := json.Marshal(want)
		if err != nil {
			return ErrInvalidArgument
		}
		n, err := q.CompleteCloneCheckpointUnusedObjectReservation(ctx, tx, sqlc.CompleteCloneCheckpointUnusedObjectReservationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), TargetBucketID: mustPgUUID(bucket.ID), Reservation: raw, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		n, err = q.DeleteCloneCheckpointUnusedObjectReservationBucket(ctx, tx, sqlc.DeleteCloneCheckpointUnusedObjectReservationBucketParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), TargetBucketID: mustPgUUID(bucket.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
	default:
		return mapErr(err)
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

// A missing release journal is acceptable only for a fully adopted checkpoint.
// Reconstructing a receipt authenticates its ORIGINAL recorded hash; it never
// manufactures a capture or a release observation from resource absence.
func authenticateUnusedCheckpointAdoptionTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings) error {
	in, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if errors.Is(err, ErrNotFound) {
		if len(op.Resources) < 3 || op.Resources[len(op.Resources)-1].Kind != "data_checkpoint_adoption" {
			return ErrConflict
		}
		compensation, compensationErr := readCheckpointSourceCompensationTx(ctx, tx, op)
		if compensationErr == nil {
			if compensation.Plan.Receipt == nil || compensation.Plan.Release != nil {
				return ErrConflict
			}
			in = ProjectEnvironmentCloneCheckpointRelease{Plan: compensation.Plan.Capture, Receipt: *compensation.Plan.Receipt,
				AdoptionSHA256: op.Resources[len(op.Resources)-1].SourceVersion, State: "planned"}
		} else if !errors.Is(compensationErr, ErrNotFound) {
			return compensationErr
		} else {
			row, err := sqlc.New().ReadProjectEnvironmentCloneConfigurationGuard(ctx, tx, sqlc.ReadProjectEnvironmentCloneConfigurationGuardParams{ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID)})
			if err != nil {
				if errors.Is(mapErr(err), ErrNotFound) {
					return ErrConflict
				}
				return mapErr(err)
			}
			fence, err := cloneConfigurationFenceFromSQL(op, row)
			if err != nil {
				return err
			}
			p := ProjectEnvironmentCloneCheckpointPlan{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, SourceEnvironment: op.SourceEnvironment, SourceRevisionHash: op.SourceRevisionHash, SourceReleaseSetID: op.SourceReleaseSetID, ConfigurationGeneration: fence.Generation, ConfigurationHeldAt: fence.HeldAt.UTC()}
			databases := map[string]bool{}
			for _, v := range views {
				for _, db := range v.Postgres {
					if databases[db.DatabaseID] {
						continue
					}
					databases[db.DatabaseID] = true
					hash, err := ProjectEnvironmentCloneDatabaseSourceHash(db)
					if err != nil {
						return ErrConflict
					}
					p.Sources = append(p.Sources, ProjectEnvironmentCloneCheckpointSource{Kind: "managed_postgres", ID: db.DatabaseID, BackendID: db.BackendID, BackendFingerprint: db.BackendFingerprint, ProviderResourceID: db.ProviderResourceID, DataResourceID: db.DataResourceID, DefinitionSHA256: hash})
				}
				for _, b := range v.Buckets {
					p.Sources = append(p.Sources, ProjectEnvironmentCloneCheckpointSource{Kind: "object_storage", ID: b.ID, AppID: v.AppID, Scope: v.SourceScope, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, ProviderResourceID: b.PhysicalName, DefinitionSHA256: cloneCheckpointSHA(b)})
				}
			}
			sort.Slice(p.Sources, func(i, j int) bool { return p.Sources[i].Kind+p.Sources[i].ID < p.Sources[j].Kind+p.Sources[j].ID })
			captured, err := time.Parse(time.RFC3339Nano, op.Resources[0].CapturePoint)
			if err != nil {
				return ErrConflict
			}
			r := ProjectEnvironmentCloneCheckpointReceipt{RequestID: op.ID, PlanSHA256: op.Resources[0].SourceVersion, CheckpointID: op.Resources[0].TargetID, CapturedAt: captured.UTC(), CompleteWriterCoverage: true}
			for _, source := range p.Sources {
				var identity string
				switch source.Kind {
				case "managed_postgres":
					snapshot, err := readClonePostgresSnapshotTx(ctx, tx, op, source.ID)
					if err != nil {
						return ErrConflict
					}
					identity = snapshot.ProviderSnapshotID
				case "object_storage":
					manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, source.ID)
					if err != nil {
						return ErrConflict
					}
					identity = manifest.Hash
				}
				r.Sources = append(r.Sources, ProjectEnvironmentCloneCheckpointSourceReceipt{Kind: source.Kind, ID: source.ID, CaptureIdentity: identity})
			}
			in = ProjectEnvironmentCloneCheckpointRelease{Plan: p, Receipt: r, AdoptionSHA256: op.Resources[len(op.Resources)-1].SourceVersion, State: "planned"}
		}
	} else if err != nil {
		return err
	}
	// Validate the persisted request shape independently of the contents-only
	// reader. No unknown or rewound release state can authorize compensation.
	switch in.State {
	case "planned":
		if !in.RequestedAt.IsZero() || in.ObservationSHA256 != "" {
			return ErrConflict
		}
	case "requested", "released":
		if in.RequestedAt.IsZero() || in.RequestedAt.Before(in.Receipt.CapturedAt) || in.RequestedAt.After(time.Now()) {
			return ErrConflict
		}
		if in.State == "requested" && in.ObservationSHA256 != "" || in.State == "released" && !validCloneObjectSHA256(in.ObservationSHA256) {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	if !validCloneObjectSHA256(in.AdoptionSHA256) {
		return ErrConflict
	}
	return validateCloneCheckpointAdoptionJournalsTx(ctx, tx, op, in, false, true, true)
}
