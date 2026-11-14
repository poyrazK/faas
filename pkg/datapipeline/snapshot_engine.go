package datapipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func (s *PostgresStore) stageSnapshot(ctx context.Context, p Pipeline, status *api.DataPipelineOperationsStatus) error {
	n, err := s.q.PipelineStageSnapshot(ctx, s.pool, sqlc.PipelineStageSnapshotParams{ID: p.ID, AccountID: p.AccountID, Revision: p.Revision, LeaseToken: p.LeaseToken, Status: operationsJSON(status)})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (e *Engine) reconcileSnapshot(ctx context.Context, p Pipeline, raw string, current func(context.Context) error) Observation {
	ob := pipelineObservation(p)
	status := operationsStatus(p)
	ob.OperationsStatus = status
	err := e.withTarget(ctx, p, current, func(target *pgx.Conn, owner string) error {
		applied, err := applyReaders(ctx, p, target, current)
		if err != nil {
			return err
		}
		if applied != nil {
			ob.ReaderOnly = true
			ob.ReaderChange = applied
			return nil
		}
		if p.DesiredState == "deleted" {
			if p.Initialized {
				if err := allowMirrorReads(ctx, p, target, false); err != nil {
					return err
				}
			}
			ob.State, ob.Reason = "deleted", ""
			return nil
		}
		if p.DesiredState == "paused" {
			ob.State, ob.Reason = "paused", ""
			return nil
		}
		if workAllowed(p, time.Now()) {
			if change, err := e.reconcileIndex(ctx, p, target, owner, current); err != nil {
				return err
			} else if change != nil {
				ob.IndexOnly = true
				ob.IndexChange = change
				return nil
			}
		}
		if p.Initialized {
			if err := ensurePipelineReadGate(ctx, p, target); err != nil {
				return err
			}
			if err := e.refreshPipelineReadGate(ctx, p, target, current); err != nil {
				return err
			}
		}
		if status.Snapshot == nil {
			status.Snapshot = &api.DataPipelineSnapshotStatus{State: "due", NextRunAt: nextWorkTime(p, time.Now().UTC()).Format(time.RFC3339Nano)}
		}
		run := status.Snapshot
		// The committed target receipt precedes the catalog acknowledgement and
		// is checked even when the source credential is currently unavailable.
		if run.RunID != "" && run.State != "succeeded" {
			var exists bool
			if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.snapshot_runs') IS NOT NULL`).Scan(&exists); err != nil {
				return err
			}
			if exists {
				var saved []byte
				err = target.QueryRow(ctx, `SELECT receipt FROM gregale_pipeline.snapshot_runs WHERE pipeline_id=$1 AND generation=$2 AND run_id=$3`, p.ID, p.Generation, run.RunID).Scan(&saved)
				if err == nil {
					var receipt api.DataPipelineSnapshotStatus
					if json.Unmarshal(saved, &receipt) != nil || receipt.RunID != run.RunID || receipt.State != "succeeded" {
						return ErrConflict
					}
					status.Snapshot = &receipt
					if err = snapshotObserved(p, status, &ob); err != nil {
						return err
					}
					return e.finishEvolution(ctx, p, target, owner, &ob, current)
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
		}
		if !workAllowed(p, time.Now()) || run.State != "running" && !runDue(run.NextRunAt, time.Now()) {
			if err = snapshotObserved(p, status, &ob); err != nil {
				return err
			}
			if ob.State == "live" {
				return e.finishEvolution(ctx, p, target, owner, &ob, current)
			}
			return nil
		}
		if run.State != "running" {
			return ErrConflict
		} // The durable run intent is staged before endpoint work.
		if raw == "" {
			return &blocked{code: "snapshot_source_unavailable"}
		}
		captured, err := e.copySnapshot(ctx, p, target, owner, raw, run, current)
		if err != nil {
			return err
		}
		status.Snapshot = captured
		if err = snapshotObserved(p, status, &ob); err != nil {
			return err
		}
		return e.finishEvolution(ctx, p, target, owner, &ob, current)
	})
	if err != nil {
		run := status.Snapshot
		if run != nil && run.State == "running" {
			run.State, run.Reason = "failed", "snapshot_refresh_failed"
			var b *blocked
			if errors.As(err, &b) {
				run.Reason = b.code
			}
			run.NextRunAt = nextWorkTime(p, time.Now().UTC().Add(time.Duration(p.Operations.Snapshot.EverySeconds)*time.Second)).Format(time.RFC3339Nano)
		}
		if p.DesiredState == "running" && run != nil && run.State == "failed" && run.CapturedAt != "" {
			ob.State, ob.Reason = "live", "snapshot_stale"
			return ob
		}
	}
	return observeReconcileError(p, ob, err)
}

func snapshotObserved(p Pipeline, status *api.DataPipelineOperationsStatus, ob *Observation) error {
	r := status.Snapshot
	if r == nil || r.CapturedAt == "" {
		ob.State, ob.Reason = "validating", "snapshot_waiting"
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, r.CapturedAt)
	if err != nil {
		return ErrConflict
	}
	ob.Initialized = true
	ob.State, ob.Reason = "live", ""
	ob.TablesReady = len(p.Tables)
	ob.Verification = "passed"
	ob.VerifiedAt = t
	ob.AppliedThroughAt = t
	if r.State == "failed" || r.Reason != "" {
		ob.Reason = "snapshot_stale"
	}
	return nil
}

func (e *Engine) copySnapshot(ctx context.Context, p Pipeline, target *pgx.Conn, owner, raw string, run *api.DataPipelineSnapshotStatus, current func(context.Context) error) (*api.DataPipelineSnapshotStatus, error) {
	ctx = workloadContext(ctx, p)
	source, err := e.connectSource(ctx, raw)
	if err != nil {
		return nil, &blocked{code: "snapshot_source_unavailable"}
	}
	defer source.Close()
	checks, err := inspectCheckSettings(ctx, source, "snapshot")
	if err != nil {
		return nil, err
	}
	if err = firstBlocked(checks); err != nil {
		return nil, err
	}
	if err = e.admitSnapshot(ctx, p, raw, source, target); err != nil {
		return nil, err
	}
	capturedAt := time.Now().UTC()
	src, err := source.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Rollback(ctx) }()
	if err = verificationSetting(ctx, src); err != nil {
		return nil, err
	}
	relations := make([]relation, 0, len(p.Tables))
	for _, name := range p.Tables {
		r, table, err := inspectPipelineTable(ctx, p, src, name)
		if err != nil {
			return nil, err
		}
		if !table.Supported {
			return nil, &blocked{code: "snapshot_source_schema_unsupported"}
		}
		relations = append(relations, r)
	}
	if err = prepareMirror(ctx, p, target, owner, relations, relationHash(relations)); err != nil {
		return nil, &blocked{code: "snapshot_schema_changed"}
	}
	if err = e.refreshPipelineReadGate(ctx, p, target, current); err != nil {
		return nil, err
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return nil, err
	}
	var rows, byteCount int64
	stages := make([]string, len(relations))
	for i, r := range relations {
		stage := pgx.Identifier{"pg_temp", "gregale_snapshot_" + strconv.Itoa(i)}.Sanitize()
		stages[i] = stage
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(owner)); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "CREATE TEMP TABLE "+ident("gregale_snapshot_"+strconv.Itoa(i))+" (LIKE "+tableIdent(r.Name)+" INCLUDING ALL) ON COMMIT DROP"); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE NONE"); err != nil {
			return nil, err
		}
		var lower []string
		for {
			batch, err := readVerificationRows(ctx, src, r, lower, nil, true, true)
			if err != nil {
				return nil, err
			}
			if batch.Limited || rows+batch.Rows > p.Operations.Snapshot.MaxRows || byteCount+batch.Bytes > p.Operations.Snapshot.MaxBytes {
				return nil, &blocked{code: "snapshot_budget_exceeded"}
			}
			if batch.Rows > 0 {
				if err = insertSnapshotRows(ctx, tx, r, stage, batch.Data); err != nil {
					return nil, err
				}
				check, err := readVerificationRowsFrom(ctx, tx, r, lower, batch.Last, false, false, stage)
				if err != nil {
					return nil, err
				}
				if check.Limited || check.Hash != batch.Hash || check.Rows != batch.Rows || check.Bytes != batch.Bytes {
					return nil, &blocked{code: "snapshot_verification_failed"}
				}
			}
			rows += batch.Rows
			byteCount += batch.Bytes
			if !batch.More {
				break
			}
			if batch.Rows == 0 {
				return nil, ErrConflict
			}
			lower = batch.Last
			if err = current(ctx); err != nil {
				return nil, err
			}
		}
	}
	if err = src.Commit(ctx); err != nil {
		return nil, err
	}
	// A bounded capture can outlive capacity evidence. Requalify before publish.
	if err = e.admitSnapshot(ctx, p, raw, source, target); err != nil {
		return nil, err
	}
	// DELETE/INSERT preserve MVCC snapshots and public row-type identities.
	// Source/temporary copying finishes before these short publication locks.
	for _, r := range relations {
		if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(r.Name)+" IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return nil, err
		}
	}
	for i, r := range relations {
		if err = validateSnapshotTarget(ctx, p, tx, r); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(owner)); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM "+tableIdent(r.Name)); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO "+tableIdent(r.Name)+" SELECT * FROM "+stages[i]); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE NONE"); err != nil {
			return nil, err
		}
	}
	var readable bool
	if err = tx.QueryRow(ctx, `SELECT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&readable); err != nil {
		return nil, err
	}
	if !readable {
		if err = updateMirrorReads(ctx, p, tx, true); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET initialized=true WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation); err != nil {
		return nil, err
	}
	receipt := *run
	receipt.State = "succeeded"
	receipt.Reason = ""
	receipt.CapturedAt = capturedAt.Format(time.RFC3339Nano)
	receipt.NextRunAt = nextWorkTime(p, capturedAt.Add(time.Duration(p.Operations.Snapshot.EverySeconds)*time.Second)).Format(time.RFC3339Nano)
	receipt.Rows = rows
	receipt.Bytes = byteCount
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.snapshot_runs(pipeline_id uuid PRIMARY KEY REFERENCES gregale_pipeline.mirrors(pipeline_id) ON DELETE CASCADE,generation bigint NOT NULL,run_id text NOT NULL,receipt jsonb NOT NULL)`); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gregale_pipeline.snapshot_runs VALUES($1,$2,$3,$4::jsonb) ON CONFLICT(pipeline_id) DO UPDATE SET generation=excluded.generation,run_id=excluded.run_id,receipt=excluded.receipt`, p.ID, p.Generation, receipt.RunID, operationsJSON(receipt)); err != nil {
		return nil, err
	}
	if err = current(ctx); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (e *Engine) admitSnapshot(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn) error {
	if e.Capacity == nil {
		return nil
	}
	s, first := e.Capacity.source(ctx, ConnectionCheck{AccountID: p.AccountID, DataConnectionCheck: api.DataConnectionCheck{ConnectionID: p.SourceConnectionID, ConnectionRevision: p.SourceRevision}}, raw, source)
	t, second := e.Capacity.target(ctx, p.AccountID, p.DatabaseID, target)
	if first != nil || second != nil || e.Admit == nil || !s.Fresh(time.Now()) || !t.Fresh(time.Now()) {
		if first == nil {
			first = second
		}
		return capacityBlock(first)
	}
	if err := e.Admit(ctx, p); err != nil {
		return capacityBlock(err)
	}
	return nil
}

func insertSnapshotRows(ctx context.Context, tx pgx.Tx, r relation, table string, data []json.RawMessage) error {
	values := make([]string, len(r.Columns))
	for i, c := range r.Columns {
		values[i] = "(v.value->>" + strconv.Itoa(i) + ")::" + c.Type
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(data); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "INSERT INTO "+table+" SELECT "+strings.Join(values, ",")+" FROM pg_catalog.jsonb_array_elements($1::jsonb) v(value)", body.Bytes())
	return err
}

func validateSnapshotTarget(ctx context.Context, p Pipeline, tx pgx.Tx, r relation) error {
	actual, _, err := inspectSourceTableMode(ctx, tx, r.Name, true)
	if err != nil {
		return err
	}
	if len(actual.Columns) != len(r.Columns) {
		return ErrConflict
	}
	for i, c := range actual.Columns {
		want := r.Columns[i]
		if c.Name != want.Name || c.Type != want.Type || c.NotNull != want.NotNull || c.Key != want.Key {
			return ErrConflict
		}
	}
	if err = validateOwnedReadPolicy(ctx, p, tx, r.Name, actual.OID); err != nil {
		return err
	}
	var triggered bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=$1 AND NOT tgisinternal AND tgenabled<>'D')`, actual.OID).Scan(&triggered); err != nil {
		return err
	}
	if triggered {
		return ErrConflict
	}
	return nil
}

func (w *Worker) startSnapshot(ctx context.Context, p *Pipeline) error {
	if !snapshotMode(*p) || p.DesiredState != "running" || !workAllowed(*p, time.Now()) || p.ReaderChange != nil && p.ReaderChange.State == "approved" {
		return nil
	}
	status := operationsStatus(*p)
	if status.Snapshot != nil && (status.Snapshot.State == "running" || !runDue(status.Snapshot.NextRunAt, time.Now())) {
		return nil
	}
	if status.Snapshot == nil {
		status.Snapshot = &api.DataPipelineSnapshotStatus{}
	}
	if status.Snapshot.RunID == "" || status.Snapshot.State == "succeeded" {
		status.Snapshot.RunID = uuid.NewString()
	}
	status.Snapshot.State = "running"
	status.Snapshot.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	store, ok := w.Store.(interface {
		stageSnapshot(context.Context, Pipeline, *api.DataPipelineOperationsStatus) error
	})
	if !ok {
		return ErrUnavailable
	}
	if err := store.stageSnapshot(ctx, *p, status); err != nil {
		return err
	}
	p.OperationsStatus = status
	return nil
}
