package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// Counters are native observations, not verified row totals or percentages.
// High-water marks avoid treating repeated unsuccessful copies as new progress.
type TableCopyProgress struct {
	Table, Phase, NativeState, PeakState string
	Rows, Bytes, PeakRows, PeakBytes     int64
	WorkerActive, CountersKnown, Stalled bool
	ObservedAt, ProgressAt               time.Time
}

type nativeCopyRelation struct {
	oid           uint32
	name, state   string
	worker, known bool
	rows, bytes   int64
}

func isBlockedReason(err error, reason string) bool {
	var b *blocked
	return errors.As(err, &b) && b.code == reason
}

func nativeCopyRelations(ctx context.Context, p Pipeline, target *pgx.Conn) ([]nativeCopyRelation, error) {
	rows, err := target.Query(ctx, `SELECT c.oid,n.nspname,c.relname,r.srsubstate::text,coalesce(obj_description(c.oid,'pg_class'),'')=$2,
 st.pid IS NOT NULL,pc.pid IS NOT NULL AND pc.tuples_processed IS NOT NULL AND pc.bytes_processed IS NOT NULL,coalesce(pc.tuples_processed,0),coalesce(pc.bytes_processed,0)
 FROM pg_subscription_rel r JOIN pg_subscription s ON s.oid=r.srsubid
 JOIN pg_class c ON c.oid=r.srrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 LEFT JOIN pg_stat_subscription st ON st.subid=s.oid AND st.relid=c.oid
 LEFT JOIN pg_stat_progress_copy pc ON pc.pid=st.pid AND pc.datid=s.subdbid
 WHERE s.subname=$1 AND s.subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`, pipelineName(p), marker(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []nativeCopyRelation{}
	for rows.Next() {
		var r nativeCopyRelation
		var schema string
		var owned bool
		if err = rows.Scan(&r.oid, &schema, &r.name, &r.state, &owned, &r.worker, &r.known, &r.rows, &r.bytes); err != nil {
			return nil, err
		}
		if schema != "public" || !containsTable(p.Tables, r.name) || !slices.Contains([]string{"i", "d", "f", "s", "r"}, r.state) {
			return nil, &blocked{"replication_state_lost", true}
		}
		if !owned {
			return nil, &blocked{"table_addition_destination_conflict", false}
		}
		result = append(result, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, before := range p.SchemaChange.Before {
		if liveTableRepair(p.SchemaChange) && before.Name == p.SchemaChange.Recovery.Table {
			continue
		}
		if !slices.ContainsFunc(result, func(r nativeCopyRelation) bool { return r.name == before.Name && r.state == "r" }) {
			return nil, &blocked{"replication_state_lost", true}
		}
	}
	return result, nil
}

func copyStateRank(state string) int {
	return slices.Index([]string{"", "i", "d", "f", "s", "r"}, state)
}

func copyProgress(previous TableCopyProgress, current TableCopyProgress, now time.Time) TableCopyProgress {
	current.ObservedAt = now
	current.PeakRows = max(previous.PeakRows, current.Rows)
	current.PeakBytes = max(previous.PeakBytes, current.Bytes)
	current.ProgressAt = previous.ProgressAt
	current.PeakState = previous.PeakState
	if copyStateRank(current.NativeState) > copyStateRank(current.PeakState) {
		current.PeakState = current.NativeState
	}
	if current.ProgressAt.IsZero() || current.ProgressAt.After(now) || current.PeakRows > previous.PeakRows || current.PeakBytes > previous.PeakBytes || copyStateRank(current.PeakState) > copyStateRank(previous.PeakState) {
		current.ProgressAt = now
	}
	current.Stalled = current.Phase != "ready" && current.Phase != "recovering" && now.Sub(current.ProgressAt) >= api.DataPipelineCopyStallTimeout
	return current
}

func ensureTableCopyReceipt(ctx context.Context, target receiptSQL) error {
	_, err := target.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.table_copies(
 pipeline_id uuid PRIMARY KEY REFERENCES gregale_pipeline.mirrors(pipeline_id) ON DELETE CASCADE,
 generation bigint NOT NULL,change_id text NOT NULL,progress jsonb NOT NULL DEFAULT '[]')`)
	return err
}

func loadTableCopies(ctx context.Context, p Pipeline, target receiptSQL) ([]TableCopyProgress, error) {
	var encoded []byte
	err := target.QueryRow(ctx, `SELECT progress FROM gregale_pipeline.table_copies WHERE pipeline_id=$1 AND generation=$2 AND change_id=$3`, p.ID, p.Generation, p.SchemaApproval).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return p.SchemaChange.Copies, nil
	}
	if err != nil {
		return nil, err
	}
	var result []TableCopyProgress
	if err = json.Unmarshal(encoded, &result); err != nil {
		return nil, ErrConflict
	}
	return result, nil
}

func saveTableCopies(ctx context.Context, p Pipeline, target receiptSQL, copies []TableCopyProgress) error {
	encoded, _ := json.Marshal(copies)
	tag, err := target.Exec(ctx, `INSERT INTO gregale_pipeline.table_copies(pipeline_id,generation,change_id,progress)
 SELECT pipeline_id,generation,schema_change,$4::jsonb FROM gregale_pipeline.mirrors
 WHERE pipeline_id=$1 AND generation=$2 AND schema_change=$3 AND NOT retiring
 ON CONFLICT(pipeline_id) DO UPDATE SET generation=excluded.generation,change_id=excluded.change_id,progress=excluded.progress`, p.ID, p.Generation, p.SchemaApproval, encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func observeTableCopies(ctx context.Context, p Pipeline, target *pgx.Conn, current func(context.Context) error) (int, error) {
	relations, err := nativeCopyRelations(ctx, p, target)
	if err != nil {
		return 0, err
	}
	if err = ensureTableCopyReceipt(ctx, target); err != nil {
		return 0, err
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	previous, err := loadTableCopies(ctx, p, tx)
	if err != nil {
		return 0, err
	}
	var stage string
	if err = tx.QueryRow(ctx, `SELECT table_stage FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND schema_change=$3 AND NOT retiring FOR UPDATE`, p.ID, p.Generation, p.SchemaApproval).Scan(&stage); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	copies := make([]TableCopyProgress, 0, len(CopiedTables(p.SchemaChange)))
	ready := 0
	for _, r := range relations {
		if r.state == "r" {
			ready++
		}
	}
	for _, name := range CopiedTables(p.SchemaChange) {
		next := TableCopyProgress{Table: name, Phase: "waiting"}
		if stage == "copying" || stage == "refreshing" {
			next.Phase = "missing"
		}
		if r := p.SchemaChange.Recovery; r != nil && r.Table == name && r.State == "approved" && slices.Contains([]string{"retiring", "detached", "cleared", "published"}, r.Stage) {
			next.Phase = "recovering"
		}
		if i := slices.IndexFunc(relations, func(r nativeCopyRelation) bool { return r.name == name }); i >= 0 {
			r := relations[i]
			next.NativeState, next.WorkerActive, next.CountersKnown, next.Rows, next.Bytes = r.state, r.worker, r.known, r.rows, r.bytes
			next.Phase = map[string]string{"i": "waiting", "d": "copying", "f": "catching_up", "s": "catching_up", "r": "ready"}[r.state]
		}
		var old TableCopyProgress
		if i := slices.IndexFunc(previous, func(c TableCopyProgress) bool { return c.Table == name }); i >= 0 {
			old = previous[i]
		}
		copies = append(copies, copyProgress(old, next, now))
	}
	if err = saveTableCopies(ctx, p, tx, copies); err != nil {
		return 0, err
	}
	if err = current(ctx); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	p.SchemaChange.Copies = copies
	return ready, nil
}

func resetTableCopy(ctx context.Context, p Pipeline, target receiptSQL, name string) error {
	copies, err := loadTableCopies(ctx, p, target)
	if err != nil {
		return err
	}
	for i, c := range copies {
		if c.Table == name {
			copies[i] = TableCopyProgress{Table: name, Phase: "waiting", ProgressAt: time.Now().UTC()}
		}
	}
	return saveTableCopies(ctx, p, target, copies)
}
