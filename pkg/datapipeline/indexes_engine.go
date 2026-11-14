package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func indexKeysSQL(keys []api.DataPipelineIndexKey) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = ident(k.Column)
		if k.TenantExpression {
			out[i] = "(" + ident(k.Column) + "::text COLLATE \"C\")"
		}
		if k.Descending {
			out[i] += " DESC"
		}
	}
	return strings.Join(out, ",")
}
func indexSpec(c IndexChange) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", c.Generation, c.TableOID, indexKeysSQL(c.Keys))))
	return hex.EncodeToString(sum[:])
}
func indexObjectMarker(p Pipeline, c IndexChange) string {
	return marker(p) + ":query-index:" + indexSpec(c)
}

// Only system catalogs and relation sizes are inspected. Customer rows and
// filter values never enter advice or the control catalog.
func inspectIndexTable(ctx context.Context, p Pipeline, c *IndexChange, q receiptSQL, owner string) (int64, error) {
	var oid uint32
	var signature string
	var bytes, dbBytes int64
	var owned bool
	err := q.QueryRow(ctx, `SELECT c.oid,pg_table_size(c.oid),pg_database_size(current_database()),
 coalesce(obj_description(c.oid,'pg_class'),'')=$2 AND pg_get_userbyid(c.relowner)=$3 AND c.relkind='r',
 (SELECT md5(string_agg(a.attnum::text||':'||a.attname||':'||a.atttypid::text||':'||a.atttypmod::text||':'||a.attcollation::text,',' ORDER BY a.attnum)) FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)
 FROM pg_class c WHERE c.oid=to_regclass($1)`, tableIdent(c.Table), marker(p), owner).Scan(&oid, &bytes, &dbBytes, &owned, &signature)
	if err != nil || !owned {
		return 0, ErrConflict
	}
	var backed bool
	if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND initialized AND NOT retiring AND $3=ANY(tables))`, p.ID, p.Generation, c.Table).Scan(&backed); err != nil || !backed {
		return 0, ErrConflict
	}
	names := make([]string, len(c.Keys))
	for i, k := range c.Keys {
		names[i] = k.Column
	}
	var found int
	if err = q.QueryRow(ctx, `SELECT count(*) FROM pg_attribute WHERE attrelid=$1 AND attnum>0 AND NOT attisdropped AND attname=ANY($2::text[])`, oid, names).Scan(&found); err != nil || found != len(names) {
		return 0, ErrConflict
	}
	if c.TableOID != 0 && (c.TableOID != oid || c.ColumnsSignature != signature) {
		return 0, ErrConflict
	}
	c.TableOID, c.ColumnsSignature, c.TableBytes = oid, signature, bytes
	return dbBytes, nil
}

func ensureIndexReceipts(ctx context.Context, q receiptSQL) error {
	_, err := q.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.query_indexes(
 pipeline_id uuid NOT NULL REFERENCES gregale_pipeline.mirrors(pipeline_id) ON DELETE CASCADE,
 generation bigint NOT NULL,spec text NOT NULL,advice_id text NOT NULL,index_oid oid NOT NULL,index_name text NOT NULL,
 PRIMARY KEY(pipeline_id,generation,spec)); REVOKE ALL ON gregale_pipeline.query_indexes FROM PUBLIC`)
	return err
}

// Advice and bounded DDL are source-independent. This method never acknowledges
// a replication observation; ordinary reconciliation follows its catalog ack.
func (e *Engine) reconcileIndex(ctx context.Context, p Pipeline, target *pgx.Conn, owner string, current func(context.Context) error) (*IndexChange, error) {
	c := p.IndexChange
	if c == nil || c.State != "requested" && c.State != "approved" {
		return nil, nil
	}
	copy := *c
	c = &copy
	block := func(reason, message string) (*IndexChange, error) {
		c.State, c.CanApply, c.Reason, c.Message = "blocked", false, reason, message
		return c, nil
	}
	if !stableIndexState(p) || c.Generation != p.Generation {
		return block("index_state_changed", "The mirror changed. Wait for a stable state and request another preview.")
	}
	r := api.DataPipelineIndexRequest{Table: c.Table, Filters: c.Filters, OrderBy: c.OrderBy, MaxIndexBytes: c.StorageBudgetBytes}
	keys, err := indexShape(p, r)
	if err != nil || indexKeysSQL(keys) != indexKeysSQL(c.Keys) {
		return block("index_request_invalid", "The saved query shape is invalid. Request another preview.")
	}
	if e.IndexStorageLimit == nil {
		return block("index_storage_unknown", "The mirror storage allowance is unavailable. Request a new preview after it is available.")
	}
	limit, err := e.IndexStorageLimit(ctx, p)
	if err != nil || limit <= 0 {
		return block("index_storage_unknown", "The mirror storage allowance is unavailable. Request a new preview after it is available.")
	}
	if c.State == "requested" {
		dbBytes, err := inspectIndexTable(ctx, p, c, target, owner)
		if err != nil {
			return block("index_target_changed", "The selected mirror table or its ownership changed. Request another preview.")
		}
		c.StorageLimitBytes = limit
		// A conservative size estimate avoids ANALYZE or reading customer rows on a
		// logical subscriber whose planner statistics may not have been collected.
		estimate := max(int64(8192), c.TableBytes*2)
		c.EstimatedIndexBytes = &estimate
		c.IndexName = "gregale_query_" + indexSpec(*c)[:24]
		c.Statement = "CREATE INDEX " + ident(c.IndexName) + " ON " + tableIdent(c.Table) + " (" + indexKeysSQL(c.Keys) + ")"
		c.State, c.CanApply, c.Reason = "preview", true, ""
		c.Message = "Conservative size estimate (twice the table size, minimum one page), not a latency prediction. An approved build briefly locks mirror writes and rolls back after its time or storage limit. Source writes are unaffected."
		switch {
		case c.TableBytes > api.DataPipelineIndexTableMaxBytes:
			c.CanApply, c.Reason = false, "index_table_too_large"
		case estimate > c.StorageBudgetBytes:
			c.CanApply, c.Reason = false, "index_budget_exceeded"
		case dbBytes > limit-estimate:
			c.CanApply, c.Reason = false, "index_storage_exceeded"
		}
		var guard bool
		if err = target.QueryRow(ctx, `SELECT has_parameter_privilege(current_user,'temp_file_limit','SET')`).Scan(&guard); err != nil {
			return nil, err
		}
		if !guard {
			c.CanApply, c.Reason = false, "index_guard_unavailable"
			c.Message = "Advice is available, but this provider role cannot enforce the temporary-file budget. Operator qualification is required before applying indexes."
		}
		// Inspection of existing owned advice never adopts an unmarked object.
		var exists bool
		if err = target.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tableIdent(c.IndexName)).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			c.CanApply, c.Reason = false, "index_already_exists"
		}
		c.ID = indexAdviceID(p, *c)
		return c, nil
	}
	if c.ID != indexAdviceID(p, *c) || c.PreviewRevision != p.Revision-1 || c.StorageLimitBytes != limit {
		return block("index_preview_stale", "The approved preview no longer matches the mirror. Request another preview.")
	}
	// A receipt is checked before freshness so a lost catalog acknowledgement is
	// safe to replay even after the original observation expires.
	var installed bool
	if err = target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.query_indexes') IS NOT NULL`).Scan(&installed); err != nil {
		return nil, err
	}
	var actual int64
	var receipt bool
	if installed {
		if err = target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gregale_pipeline.query_indexes r JOIN pg_class i ON i.oid=r.index_oid JOIN pg_index x ON x.indexrelid=i.oid
 WHERE r.pipeline_id=$1 AND r.generation=$2 AND r.spec=$3 AND r.advice_id=$4 AND i.relname=$5 AND i.relnamespace='public'::regnamespace AND x.indrelid=$6 AND x.indisvalid AND coalesce(obj_description(i.oid,'pg_class'),'')=$7)`, p.ID, p.Generation, indexSpec(*c), c.ID, c.IndexName, c.TableOID, indexObjectMarker(p, *c)).Scan(&receipt); err != nil {
			return nil, err
		}
	}
	if receipt {
		if err = target.QueryRow(ctx, `SELECT pg_relation_size(to_regclass($1))`, tableIdent(c.IndexName)).Scan(&actual); err != nil {
			return nil, err
		}
		c.State, c.CanApply, c.AppliedBytes, c.Message = "applied", false, &actual, "The approved index is installed on the owned mirror."
		return c, nil
	}
	baseline := p
	baseline.Revision = c.PreviewRevision
	if !recentIndexBaseline(baseline, c, time.Now()) {
		return block("index_preview_stale", "The monitoring evidence expired. Request another preview.")
	}
	if err = current(ctx); err != nil {
		return nil, err
	}
	actual, err = buildIndex(ctx, p, c, target, owner, current)
	if err != nil {
		if fence := current(ctx); fence != nil {
			return nil, fence
		}
		return block("index_build_stopped", "The bounded build was rolled back: its time, lock, size, storage or ownership check failed. Request another preview after reviewing mirror health.")
	}
	c.State, c.CanApply, c.AppliedBytes, c.Message = "applied", false, &actual, "The approved index is installed on the owned mirror."
	return c, nil
}

func buildIndex(ctx context.Context, p Pipeline, c *IndexChange, target *pgx.Conn, owner string, current func(context.Context) error) (int64, error) {
	build, cancel := context.WithTimeout(ctx, api.DataPipelineIndexBuildTimeout)
	defer cancel()
	tx, err := target.Begin(build)
	if err != nil {
		return 0, err
	}
	defer func() {
		rollback, done := context.WithTimeout(context.WithoutCancel(ctx), api.DataPipelineIndexBuildTimeout)
		defer done()
		_ = tx.Rollback(rollback)
	}()
	settings := []string{
		"SET LOCAL statement_timeout = " + literal(strconv.FormatInt(api.DataPipelineIndexBuildTimeout.Milliseconds(), 10)+"ms"),
		"SET LOCAL lock_timeout = " + literal(strconv.FormatInt(api.DataPipelineIndexLockTimeout.Milliseconds(), 10)+"ms"),
		"SET LOCAL maintenance_work_mem = " + literal(strconv.Itoa(api.DataPipelineIndexMemoryKB)+"kB"),
		"SET LOCAL temp_file_limit = " + literal(strconv.FormatInt(api.DataPipelineIndexBuildMaxBytes/1024, 10)+"kB"),
		"LOCK TABLE " + tableIdent(c.Table) + " IN SHARE MODE",
	}
	for _, s := range settings {
		if _, err = tx.Exec(build, s); err != nil {
			return 0, err
		}
	}
	dbBytes, err := inspectIndexTable(build, p, c, tx, owner)
	if err != nil || c.TableBytes > api.DataPipelineIndexTableMaxBytes || c.EstimatedIndexBytes == nil || dbBytes > c.StorageLimitBytes-*c.EstimatedIndexBytes {
		return 0, ErrConflict
	}
	if err = ensureIndexReceipts(build, tx); err != nil {
		return 0, err
	}
	// Scoped table replacement drops its indexes automatically. Retire only
	// receipts whose exact objects no longer exist so repairs do not consume
	// the live-index quota. Conflicting extant objects remain fenced.
	if _, err = tx.Exec(build, `DELETE FROM gregale_pipeline.query_indexes r WHERE pipeline_id=$1 AND generation=$2 AND NOT EXISTS(SELECT 1 FROM pg_class c WHERE c.oid=r.index_oid)`, p.ID, p.Generation); err != nil {
		return 0, err
	}
	var count int
	if err = tx.QueryRow(build, `SELECT count(*) FROM gregale_pipeline.query_indexes WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&count); err != nil {
		return 0, err
	}
	if count >= api.DataPipelineIndexesMax {
		return 0, ErrQuota
	}
	statement := "CREATE INDEX " + ident(c.IndexName) + " ON " + tableIdent(c.Table) + " (" + indexKeysSQL(c.Keys) + ")"
	if statement != c.Statement {
		return 0, ErrConflict
	}
	if _, err = tx.Exec(build, statement); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(build, "COMMENT ON INDEX "+tableIdent(c.IndexName)+" IS "+literal(indexObjectMarker(p, *c))); err != nil {
		return 0, err
	}
	var actual int64
	if err = tx.QueryRow(build, `SELECT pg_relation_size(to_regclass($1)),pg_database_size(current_database())`, tableIdent(c.IndexName)).Scan(&actual, &dbBytes); err != nil {
		return 0, err
	}
	if actual > c.StorageBudgetBytes || dbBytes > c.StorageLimitBytes {
		return 0, ErrQuota
	}
	if _, err = tx.Exec(build, `INSERT INTO gregale_pipeline.query_indexes(pipeline_id,generation,spec,advice_id,index_oid,index_name) VALUES($1,$2,$3,$4,to_regclass($5),$6)`, p.ID, p.Generation, indexSpec(*c), c.ID, tableIdent(c.IndexName), c.IndexName); err != nil {
		return 0, err
	}
	if err = tx.QueryRow(build, `SELECT pg_database_size(current_database())`).Scan(&dbBytes); err != nil {
		return 0, err
	}
	if dbBytes > c.StorageLimitBytes {
		return 0, ErrQuota
	}
	if err = current(build); err != nil {
		return 0, err
	}
	if err = tx.Commit(build); err != nil {
		return 0, err
	}
	return actual, nil
}
