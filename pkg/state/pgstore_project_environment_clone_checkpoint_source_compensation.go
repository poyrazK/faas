package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointSourceCompensationStore = (*PgStore)(nil)

// Synchronize before operation/app locks, just as configuration acquisition
// does. Completed replay never reads current source rows or another owner's
// holds: the immutable plan and original local journals remain its authority.
func (s *PgStore) checkpointSourceCompensationTx(ctx context.Context, l ProjectEnvironmentCloneLease, work func(context.Context, pgx.Tx, ProjectEnvironmentCloneOperation) error) error {
	if !validCloneLeaseIdentity(l) {
		return ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err = q.LockCloneCheckpointUnusedReservationClock(ctx, tx); err != nil {
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
	if _, err = q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
		return mapErr(err)
	}
	if err = work(ctx, tx, op); err != nil {
		return err
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func checkpointSourceCompensationFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointSourceCompensation) (ProjectEnvironmentCloneCheckpointSourceCompensation, error) {
	in := ProjectEnvironmentCloneCheckpointSourceCompensation{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || pgUUIDString(r.OperationID) != in.Plan.Capture.OperationID || validateCheckpointSourceCompensationPlan(in.Plan) != nil || !checkpointObjectRetirementJournalShape(in.State, "compensated", in.RequestedAt, in.ObservationSHA256) {
		return in, ErrConflict
	}
	if (in.State != "planned") != r.RequestedAt.Valid || (in.State == "compensated") != r.CompensatedAt.Valid || (in.State == "compensated") != r.ObservationSha256.Valid || (in.State == "compensated") != (len(r.Observation) > 0) {
		return in, ErrConflict
	}
	if in.State == "compensated" && (json.Unmarshal(r.Observation, &in.Observation) != nil || validateCheckpointSourceCompensationObservation(in, in.Observation) != nil || !in.Observation.CompensatedAt.Equal(r.CompensatedAt.Time)) {
		return in, ErrConflict
	}
	return in, nil
}

func readCheckpointSourceCompensationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointSourceCompensation, error) {
	r, err := sqlc.New().ReadCloneCheckpointSourceCompensation(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return ProjectEnvironmentCloneCheckpointSourceCompensation{}, mapErr(err)
	}
	in, err := checkpointSourceCompensationFromSQL(r)
	if err != nil {
		return in, err
	}
	err = authenticateCheckpointSourceCompensationTx(ctx, tx, op, in.Plan, in.State != "compensated")
	if errors.Is(err, ErrNotFound) {
		err = ErrConflict
	}
	if err == nil && in.State == "compensated" {
		err = checkpointSourceCompensationNoHoldsTx(ctx, tx, op)
	}
	return in, err
}

func checkpointSourceCompensationNoHoldsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
	held, err := sqlc.New().CloneCheckpointSourceCompensationHasHolds(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	if !held.Valid || held.Bool {
		return ErrConflict
	}
	return nil
}

// Rebuild only from frozen binding records. The generation and timestamp must
// additionally match the owned guard or the original successful-release journal.
func cloneCheckpointSourcesFromBindings(views []ProjectEnvironmentCloneBindings) ([]ProjectEnvironmentCloneCheckpointSource, error) {
	sources := map[string]ProjectEnvironmentCloneCheckpointSource{}
	for _, v := range views {
		for _, db := range v.Postgres {
			hash, err := ProjectEnvironmentCloneDatabaseSourceHash(db)
			if err != nil {
				return nil, ErrConflict
			}
			s := ProjectEnvironmentCloneCheckpointSource{Kind: "managed_postgres", ID: db.DatabaseID, BackendID: db.BackendID, BackendFingerprint: db.BackendFingerprint, ProviderResourceID: db.ProviderResourceID, DataResourceID: db.DataResourceID, DefinitionSHA256: hash}
			key := s.Kind + "\x00" + s.ID
			if old, ok := sources[key]; ok && old != s {
				return nil, ErrConflict
			}
			sources[key] = s
		}
		for _, b := range v.Buckets {
			s := ProjectEnvironmentCloneCheckpointSource{Kind: "object_storage", ID: b.ID, AppID: v.AppID, Scope: v.SourceScope, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, ProviderResourceID: b.PhysicalName, DefinitionSHA256: cloneCheckpointSHA(b)}
			key := s.Kind + "\x00" + s.ID
			if _, ok := sources[key]; ok {
				return nil, ErrConflict
			}
			sources[key] = s
		}
	}
	out := make([]ProjectEnvironmentCloneCheckpointSource, 0, len(sources))
	for _, s := range sources {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind+"\x00"+out[i].ID < out[j].Kind+"\x00"+out[j].ID })
	return out, nil
}

func authenticateCheckpointSourceCompensationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointSourceCompensationPlan, requireHolds bool) error {
	if err := validateCheckpointSourceCompensationPlan(p); err != nil {
		return fmt.Errorf("source compensation original plan: %w", err)
	}
	if len(op.Resources) == 0 || op.Resources[0] != p.Intent {
		return fmt.Errorf("source compensation original intent: %w", ErrConflict)
	}
	c := p.Capture
	if c.OperationID != op.ID || c.AccountID != op.AccountID || c.ProjectID != op.ProjectID || c.SourceEnvironment != op.SourceEnvironment || c.SourceRevisionHash != op.SourceRevisionHash || c.SourceReleaseSetID != op.SourceReleaseSetID {
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
	sources, err := cloneCheckpointSourcesFromBindings(views)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(c.Sources, sources) {
		return fmt.Errorf("source compensation frozen sources: %w", ErrConflict)
	}
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if errors.Is(err, ErrNotFound) {
		if p.Release != nil {
			return ErrConflict
		}
	} else if err != nil {
		return err
	} else if p.Release == nil || !reflect.DeepEqual(release, *p.Release) {
		return ErrConflict
	}
	if p.Receipt == nil {
		if len(op.Resources) != 1 {
			return ErrConflict
		}
	} else {
		if validateCloneCheckpointAdoptionReceiptContents(op, ProjectEnvironmentCloneCheckpointAdoption{Receipt: *p.Receipt}) != nil {
			return ErrConflict
		}
		if len(op.Resources) > 2 {
			adopted := ProjectEnvironmentCloneCheckpointRelease{Plan: c, Receipt: *p.Receipt, State: "planned", AdoptionSHA256: op.Resources[len(op.Resources)-1].SourceVersion}
			if p.Release != nil {
				adopted = *p.Release
			}
			if err = validateCloneCheckpointAdoptionJournalsTx(ctx, tx, op, adopted, false, true, true); err != nil {
				return err
			}
		} else if p.Release != nil {
			return ErrConflict
		}
	}
	if !requireHolds {
		return nil
	}
	if p.Release != nil && p.Release.State == "released" {
		return checkpointSourceCompensationNoHoldsTx(ctx, tx, op)
	}
	if err := validateCheckpointSourceCompensationHoldsTx(ctx, tx, op, c); err != nil {
		return fmt.Errorf("source compensation original holds: %w", err)
	}
	return nil
}

func validateCheckpointSourceCompensationHoldsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointPlan) error {
	q := sqlc.New()
	guard, err := q.ReadProjectEnvironmentCloneConfigurationGuard(ctx, tx, sqlc.ReadProjectEnvironmentCloneConfigurationGuardParams{ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID)})
	if err != nil {
		return mapErr(err)
	}
	fence, err := cloneConfigurationFenceFromSQL(op, guard)
	if err != nil || fence.Generation != p.ConfigurationGeneration || !fence.HeldAt.Equal(p.ConfigurationHeldAt) {
		return ErrConflict
	}
	pg, err := q.ListClonePostgresWriteFences(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	objects, err := cloneObjectWriteFencesTx(ctx, tx, op)
	if err != nil {
		return err
	}
	databases := map[string]ProjectEnvironmentCloneCheckpointSource{}
	buckets := map[string]ProjectEnvironmentCloneCheckpointSource{}
	for _, source := range p.Sources {
		if source.Kind == "managed_postgres" {
			databases[source.ID] = source
		} else {
			buckets[source.ID] = source
		}
	}
	if len(pg) != len(databases) || len(objects) != len(buckets) {
		return ErrConflict
	}
	for _, row := range pg {
		f := clonePostgresWriteFenceFromSQL(row)
		source, ok := databases[f.SourceDatabaseID]
		if !ok || f.State != "held" || f.SourceVersion != source.DefinitionSHA256 || f.BackendID != source.BackendID || f.BackendFingerprint != source.BackendFingerprint || f.SourceProviderResourceID != source.ProviderResourceID || f.SourceDataResourceID != source.DataResourceID {
			return ErrConflict
		}
		if err = lockClonePostgresFenceSource(ctx, tx, op, f); err != nil {
			return err
		}
	}
	for _, f := range objects {
		source, ok := buckets[f.Bucket.ID]
		if !ok || f.Bucket.BackendID != source.BackendID || f.Bucket.BackendFingerprint != source.BackendFingerprint || f.Bucket.PhysicalName != source.ProviderResourceID || f.Requests != 0 || f.NativeGrants != 0 || f.Deletions != 0 || f.Protections != 0 || f.Uploads != 0 || f.Multipart != 0 {
			return ErrConflict
		}
	}
	return nil
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointSourceCompensation(ctx context.Context, l ProjectEnvironmentCloneLease, p ProjectEnvironmentCloneCheckpointSourceCompensationPlan) (ProjectEnvironmentCloneCheckpointSourceCompensation, error) {
	var out ProjectEnvironmentCloneCheckpointSourceCompensation
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointSourceCompensationTx(ctx, tx, op)
		if err == nil {
			if !reflect.DeepEqual(out.Plan, p) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err = authenticateCheckpointSourceCompensationTx(ctx, tx, op, p, true); err != nil {
			return err
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return ErrInvalidArgument
		}
		if len(raw) > api.CheckpointSourceCompensationMaxBytes {
			return ErrQuotaExceeded
		}
		r, err := sqlc.New().InsertCloneCheckpointSourceCompensation(ctx, tx, sqlc.InsertCloneCheckpointSourceCompensationParams{OperationID: mustPgUUID(op.ID), Plan: raw, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointSourceCompensationFromSQL(r)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointSourceCompensation{}, err
	}
	return out, nil
}

func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointSourceCompensation(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointSourceCompensation, bool, error) {
	var out ProjectEnvironmentCloneCheckpointSourceCompensation
	dispatch := false
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointSourceCompensationTx(ctx, tx, op)
		if err != nil || out.State != "planned" {
			return err
		}
		r, err := sqlc.New().ClaimCloneCheckpointSourceCompensation(ctx, tx, sqlc.ClaimCloneCheckpointSourceCompensationParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointSourceCompensationFromSQL(r)
		dispatch = err == nil
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointSourceCompensation{}, false, err
	}
	return out, dispatch, nil
}

func (s *PgStore) ProjectEnvironmentCloneCheckpointSourceCompensationForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointSourceCompensation, error) {
	var out ProjectEnvironmentCloneCheckpointSourceCompensation
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointSourceCompensationTx(ctx, tx, op)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointSourceCompensation{}, err
	}
	return out, nil
}

func (s *PgStore) FinishProjectEnvironmentCloneCheckpointSourceCompensation(ctx context.Context, l ProjectEnvironmentCloneLease, o ProjectEnvironmentCloneCheckpointSourceCompensationObservation) error {
	return s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointSourceCompensationTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if err = validateCheckpointSourceCompensationObservation(in, o); err != nil {
			return err
		}
		if in.State == "compensated" {
			return nil
		}
		raw, err := json.Marshal(o)
		if err != nil {
			return ErrInvalidArgument
		}
		if len(raw) > api.CheckpointSourceCompensationMaxBytes {
			return ErrQuotaExceeded
		}
		q := sqlc.New()
		if err = cloneCheckpointReleaseWriteResult(q.FinishCloneCheckpointSourceCompensation(ctx, tx, sqlc.FinishCloneCheckpointSourceCompensationParams{OperationID: mustPgUUID(op.ID), Observation: raw, ObservationSha256: cloneCheckpointSHA(o), CompensatedAt: pgtype.Timestamptz{Time: o.CompensatedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})); err != nil {
			return err
		}
		// Successful original release already opened the local holds. Never touch a
		// later capture's source rows or holds, including on first compensation.
		if in.Plan.Release == nil || in.Plan.Release.State != "released" {
			for _, source := range o.Sources {
				if source.Kind == "managed_postgres" {
					err = cloneCheckpointReleaseWriteResult(q.CompensateCloneCheckpointPostgresFence(ctx, tx, sqlc.CompensateCloneCheckpointPostgresFenceParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(source.ID), RemoteTerminalState: source.TerminalState, RemoteReleasedAt: pgtype.Timestamptz{Time: source.ReleasedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)}))
				} else {
					err = cloneCheckpointReleaseWriteResult(q.CompensateCloneCheckpointObjectFence(ctx, tx, sqlc.CompensateCloneCheckpointObjectFenceParams{OperationID: mustPgUUID(op.ID), BucketID: mustPgUUID(source.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)}))
				}
				if err != nil {
					return err
				}
			}
			c := in.Plan.Capture
			err = cloneCheckpointReleaseWriteResult(q.CompensateCloneCheckpointConfiguration(ctx, tx, sqlc.CompensateCloneCheckpointConfigurationParams{OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), Generation: c.ConfigurationGeneration, HeldAt: pgtype.Timestamptz{Time: c.ConfigurationHeldAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)}))
			if err != nil {
				return err
			}
		}
		return checkpointSourceCompensationNoHoldsTx(ctx, tx, op)
	})
}
