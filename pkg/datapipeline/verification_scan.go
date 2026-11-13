package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

type verificationRange struct {
	ID           int64
	Lower, Upper []string
	Infinite     bool
}
type verificationDigest struct {
	Hash          string
	Last          []string
	Rows, Bytes   int64
	More, Limited bool
	LimitReason   string
	Data          []json.RawMessage
}

func verificationQuery(r relation, lower, upper []string) (string, []any) {
	keys := primaryColumns(r)
	args := []any{}
	values, sizes, order, textKeys := []string{}, []string{}, []string{}, []string{}
	for _, c := range r.Columns {
		name := "v." + ident(c.Name)
		values = append(values, name+"::text")
		size := "pg_catalog.octet_length(" + name + "::text)::bigint"
		if c.Type == "bytea" {
			// verificationSetting pins hex output. Read the raw length without
			// allocating its potentially unrepresentable hexadecimal expansion.
			size = "pg_catalog.octet_length(" + name + ")::bigint*2+2"
		}
		sizes = append(sizes, "coalesce("+size+",0)")
	}
	for _, c := range keys {
		order = append(order, collated("v."+ident(c.Name), c))
		textKeys = append(textKeys, "v."+ident(c.Name)+"::text")
	}
	predicates := []string{}
	for _, bound := range []struct {
		values   []string
		operator string
	}{{lower, ">"}, {upper, "<="}} {
		if len(bound.values) == 0 {
			continue
		}
		params := []string{}
		for i, c := range keys {
			args = append(args, bound.values[i])
			params = append(params, collated("$"+strconv.Itoa(len(args))+"::text::"+c.Type, c))
		}
		predicates = append(predicates, "ROW("+strings.Join(order, ",")+")"+bound.operator+"ROW("+strings.Join(params, ",")+")")
	}
	where := ""
	if len(predicates) > 0 {
		where = " WHERE " + strings.Join(predicates, " AND ")
	}
	args = append(args, api.DataPipelineVerificationChunkRows+1)
	rowLimit := "$" + strconv.Itoa(len(args))
	args = append(args, api.DataPipelineVerificationBytesMax+1)
	byteLimit := "$" + strconv.Itoa(len(args)) + "::integer"
	args = append(args, api.DataPipelineVerificationChunkBytes)
	chunkLimit := "$" + strconv.Itoa(len(args))
	args = append(args, api.DataPipelineVerificationKeyBytesMax+1)
	keyLimit := "$" + strconv.Itoa(len(args))
	// Materialize only tuple locations and ordering, then encode rows until the
	// byte budget is reached. Every lookup uses the same statement snapshot;
	// the caller's SHARE lock also prevents a rewrite or a concurrent writer.
	// The lookahead row stays on the server. Draining a closed pgx.Rows must
	// never serialize the remaining 2,001 potentially multi-megabyte rows.
	data := "substring(pg_catalog.convert_to(pg_catalog.to_jsonb(ARRAY[" + strings.Join(values, ",") + "]::text[])::text,'UTF8') FROM 1 FOR " + byteLimit + ")"
	// OFFSET 0 prevents repeated evaluation of the encoded value when its size
	// and bytes are both referenced by the recursive step.
	keyData := "substring(pg_catalog.convert_to(pg_catalog.array_to_json(ARRAY[" + strings.Join(textKeys, ",") + "]::text[])::text,'UTF8') FROM 1 FOR " + keyLimit + ")"
	lookup := func(tid string) string {
		// Text lengths are a lower bound on the encoded array. Below that
		// bound, JSON escaping can expand it at most sixfold, within JSONB's
		// representation limit. A NULL sentinel stops the recursive batch.
		return "SELECT CASE WHEN size.too_large THEN NULL::bytea ELSE " + data + " END AS data," +
			"CASE WHEN size.too_large THEN NULL::bytea ELSE " + keyData + " END AS keys FROM " + tableIdent(r.Name) +
			" v CROSS JOIN LATERAL (SELECT (" + strings.Join(sizes, "+") + ")>=" + byteLimit + " AS too_large OFFSET 0) size WHERE v.ctid=" + tid + " OFFSET 0"
	}
	// A bounded tid[] gives each recursive step constant-time access to its
	// next tuple, rather than repeatedly scanning a materialized key relation.
	query := "WITH RECURSIVE keyset AS MATERIALIZED (SELECT array_agg(tid ORDER BY ord) AS tids FROM (SELECT v.ctid AS tid,row_number() OVER (ORDER BY " + strings.Join(order, ",") + ") AS ord FROM " + tableIdent(r.Name) + " v" + where + " ORDER BY " + strings.Join(order, ",") + " LIMIT " + rowLimit + ") bounded)," +
		"batch(ord,data,keys,used) AS (SELECT 1,r.data,r.keys,octet_length(r.data)::bigint FROM keyset k CROSS JOIN LATERAL (" + lookup("k.tids[1]") + ") r UNION ALL " +
		"SELECT b.ord+1,r.data,r.keys,b.used+octet_length(r.data) FROM batch b CROSS JOIN keyset k CROSS JOIN LATERAL (" + lookup("k.tids[b.ord+1]") + ") r WHERE b.used<" + chunkLimit + " AND octet_length(b.data)<" + byteLimit + ") " +
		"SELECT data,keys,ord<cardinality(tids) FROM batch CROSS JOIN keyset WHERE ord=1 OR used<=" + chunkLimit + " ORDER BY ord"
	return query, args
}
func hashVerificationRow(h hash.Hash, row []byte) {
	_, _ = fmt.Fprintf(h, "%d:", len(row))
	_, _ = h.Write(row)
}
func scanVerificationRange(ctx context.Context, tx pgx.Tx, r relation, lower, upper []string, split bool) (verificationDigest, error) {
	return scanVerificationRows(ctx, tx, r, lower, upper, split, false)
}
func scanVerificationRows(ctx context.Context, tx pgx.Tx, r relation, lower, upper []string, split, capture bool) (verificationDigest, error) {
	read, err := tx.Begin(ctx)
	if err != nil {
		return verificationDigest{}, err
	}
	defer func() { _ = read.Rollback(ctx) }()
	result, err := readVerificationRows(ctx, read, r, lower, upper, split, capture)
	if err != nil {
		// Builtin output functions can themselves exceed PostgreSQL's format
		// limits (for example compact JSONB numbers with huge text output).
		// Restore the parent transaction before persisting an inconclusive run.
		if rollbackErr := read.Rollback(ctx); rollbackErr != nil {
			return verificationDigest{}, err
		}
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "54000" {
			return verificationDigest{Limited: true, LimitReason: "verification_row_limit"}, nil
		}
		return result, err
	}
	if err = read.Commit(ctx); err != nil {
		return verificationDigest{}, err
	}
	return result, nil
}

func readVerificationRows(ctx context.Context, tx pgx.Tx, r relation, lower, upper []string, split, capture bool) (result verificationDigest, err error) {
	query, args := verificationQuery(r, lower, upper)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return verificationDigest{}, err
	}
	defer func() {
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
	}()
	sum := sha256.New()
	keys := primaryColumns(r)
	for rows.Next() {
		var data, keyData []byte
		var remaining bool
		dest := []any{&data, &keyData, &remaining}
		if err = rows.Scan(dest...); err != nil {
			return result, err
		}
		if data == nil || len(data) > api.DataPipelineVerificationBytesMax {
			result.Limited = true
			result.LimitReason = "verification_row_limit"
			return result, nil
		}
		// Bounds are stored in the private checkpoint and range journal. A
		// small numeric index key can expand to megabytes in text form, so the
		// row budget alone cannot bound those durable representations.
		if len(keyData) > api.DataPipelineVerificationKeyBytesMax {
			result.Limited, result.LimitReason = true, "verification_key_limit"
			return result, nil
		}
		var values []string
		if json.Unmarshal(keyData, &values) != nil || len(values) != len(keys) {
			return result, ErrConflict
		}
		encodedKeys, _ := json.Marshal(values)
		if len(encodedKeys) > api.DataPipelineVerificationKeyBytesMax {
			result.Limited = true
			result.LimitReason = "verification_key_limit"
			return result, nil
		}
		if result.Rows >= api.DataPipelineVerificationChunkRows || split && result.Rows > 0 && result.Bytes+int64(len(data)) > api.DataPipelineVerificationChunkBytes {
			result.More = true
			break
		}
		hashVerificationRow(sum, data)
		if capture {
			result.Data = append(result.Data, json.RawMessage(data))
		}
		result.More = remaining
		result.Last = values
		result.Rows++
		result.Bytes += int64(len(data))
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.Hash = hex.EncodeToString(sum.Sum(nil))
	return result, nil
}
func lockVerificationSource(ctx context.Context, source *pgxpool.Pool, names []string) (pgx.Tx, error) {
	tx, err := source.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = verificationSetting(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = tableIdent(name)
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+strings.Join(quoted, ",")+" IN SHARE MODE"); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func waitVerificationCatchup(ctx context.Context, p Pipeline, source *pgxpool.Pool) (bool, error) {
	catchup, cancel := context.WithTimeout(ctx, api.DataPipelineVerificationCatchupTimeout)
	defer cancel()
	var barrier string
	if err := source.QueryRow(catchup, `SELECT pg_current_wal_insert_lsn()::text`).Scan(&barrier); err != nil {
		if catchup.Err() != nil && ctx.Err() == nil {
			return false, nil
		}
		return false, err
	}
	poll := time.NewTicker(api.DataPipelineVerificationPoll)
	defer poll.Stop()
	for {
		var caught bool
		err := source.QueryRow(catchup, `SELECT coalesce(bool_or(confirmed_flush_lsn >= $2::pg_lsn AND plugin='pgoutput' AND database=current_database() AND active AND coalesce(wal_status,'')<>'lost'),false) FROM pg_replication_slots WHERE slot_name=$1`, pipelineName(p), barrier).Scan(&caught)
		if err != nil {
			if catchup.Err() != nil && ctx.Err() == nil {
				return false, nil
			}
			return false, err
		}
		if caught {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-catchup.Done():
			return false, nil
		case <-poll.C:
		}
	}
}
func dirtyVerificationRange(ctx context.Context, p Pipeline, target verificationSQL, r relation, index int) (*verificationRange, error) {
	ranges, _ := verificationNames(p, index)
	var row verificationRange
	var lower, upper []byte
	err := target.QueryRow(ctx, "SELECT id,lower_keys,upper_keys,high_inf FROM "+ranges+" WHERE dirty ORDER BY id LIMIT 1").Scan(&row.ID, &lower, &upper, &row.Infinite)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(lower, &row.Lower) != nil || json.Unmarshal(upper, &row.Upper) != nil {
		return nil, ErrConflict
	}
	if len(row.Lower) != 0 && len(row.Lower) != len(primaryColumns(r)) || !row.Infinite && len(row.Upper) != len(primaryColumns(r)) {
		return nil, ErrConflict
	}
	if row.Infinite {
		row.Upper = nil
	}
	return &row, nil
}
func writeVerificationRange(ctx context.Context, p Pipeline, tx pgx.Tx, r relation, index int, lower, upper []string, infinite bool) (int64, error) {
	ranges, _ := verificationNames(p, index)
	encoded, _ := json.Marshal(lower)
	encodedUpper, _ := json.Marshal(upper)
	columns := []string{"lower_keys", "upper_keys", "high_inf", "dirty"}
	params := []string{"$1::jsonb", "$2::jsonb", "$3", "false"}
	args := []any{encoded, encodedUpper, infinite}
	for i, c := range primaryColumns(r) {
		columns = append(columns, ident("hi"+strconv.Itoa(i)))
		args = append(args, nil)
		if !infinite {
			args[len(args)-1] = upper[i]
		}
		params = append(params, "$"+strconv.Itoa(len(args))+"::text::"+c.Type)
	}
	var id int64
	err := tx.QueryRow(ctx, "INSERT INTO "+ranges+" ("+strings.Join(columns, ",")+") VALUES("+strings.Join(params, ",")+") RETURNING id", args...).Scan(&id)
	return id, err
}

// Both ends are write-locked only for this bounded range. Tracking is registered
// before reading the mirror; subsequent target changes atomically dirty it.
func compareVerificationRange(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, c *verificationCheckpoint, index int, dirty *verificationRange, current func(context.Context) error) (bool, error) {
	r := relations[index]
	sourceTx, err := lockVerificationSource(ctx, source, []string{r.Name})
	if err != nil {
		return false, err
	}
	defer func() { _ = sourceTx.Rollback(ctx) }()
	caught, err := waitVerificationCatchup(ctx, p, source)
	if err != nil {
		return false, err
	}
	if !caught {
		c.Progress.Reason = "waiting_for_replication"
		return false, nil
	}
	actual, _, err := inspectSourceTable(ctx, source, r.Name)
	if err != nil {
		return false, err
	}
	if !sameVerificationRelation(actual, r) {
		return false, ErrConflict
	}
	_, storage, err := verificationStorage(ctx, p, sourceTx, target, []relation{r})
	if err != nil {
		return false, err
	}
	if len(c.Storage) != len(relations) || storage[0] != c.Storage[index] {
		return false, errVerificationSourceRewritten
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(r.Name)+" IN SHARE MODE"); err != nil {
		return false, err
	}
	if err = checkVerificationTracking(ctx, p, tx, relations, c.Progress.ID); err != nil {
		return false, err
	}
	original := c
	copy := *c
	c = &copy
	moved, err := compareLockedVerificationRange(ctx, p, sourceTx, tx, r, c, index, dirty)
	if err != nil {
		return false, err
	}
	if !moved && c.Progress.State == "running" {
		return false, nil
	}
	if err = saveVerification(ctx, tx, p, c); err != nil {
		return false, err
	}
	if err = current(ctx); err != nil {
		return false, err
	}
	var held int
	if err = sourceTx.QueryRow(ctx, "SELECT 1").Scan(&held); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	*original = *c
	return moved, nil
}

func compareLockedVerificationRange(ctx context.Context, p Pipeline, sourceTx, targetTx pgx.Tx, r relation, c *verificationCheckpoint, index int, dirty *verificationRange) (bool, error) {
	tx := targetTx
	var err error
	lower, upper := c.Cursor, []string(nil)
	infinite := true
	if dirty != nil {
		lower, upper, infinite = dirty.Lower, dirty.Upper, dirty.Infinite
	}
	columns, err := approvedBackfillColumns(p, r)
	if err != nil {
		return false, err
	}
	a, err := scanVerificationRows(ctx, sourceTx, r, lower, upper, true, len(columns) > 0)
	if err != nil {
		return false, err
	}
	if a.Limited {
		c.Progress.State, c.Progress.Reason, c.Cursor = "limited", a.LimitReason, nil
		return false, nil
	}
	headUpper, headInfinite := upper, infinite
	if a.More {
		headUpper, headInfinite = a.Last, false
	}
	if len(columns) > 0 {
		rows, err := backfillVerificationRows(ctx, p, tx, r, columns, a.Data)
		if err != nil {
			return false, err
		}
		c.Progress.BackfilledRows += rows
	}
	b, err := scanVerificationRange(ctx, tx, r, lower, headUpper, false)
	if err != nil {
		return false, err
	}
	if b.Limited {
		c.Progress.State, c.Progress.Reason, c.Cursor = "limited", b.LimitReason, nil
		return false, nil
	}
	if a.Hash != b.Hash || a.Rows != b.Rows || b.More {
		c.Progress.State, c.Progress.Reason = "mismatch", "replica_mismatch"
		c.Progress.Table = r.Name // Also retain the failed scope during the final seal.
		return false, nil
	}
	ranges, _ := verificationNames(p, index)
	if dirty == nil || a.More {
		if c.Progress.Ranges >= api.DataPipelineVerificationRangesMax {
			c.Progress.State, c.Progress.Reason = "limited", "verification_range_limit"
			return false, nil
		}
		c.Progress.Ranges++
	}
	if dirty != nil {
		if _, err = tx.Exec(ctx, "DELETE FROM "+ranges+" WHERE id=$1", dirty.ID); err != nil {
			return false, err
		}
	}
	if _, err = writeVerificationRange(ctx, p, tx, r, index, lower, headUpper, headInfinite); err != nil {
		return false, err
	}
	if dirty != nil && a.More {
		if _, err = writeVerificationRange(ctx, p, tx, r, index, headUpper, upper, infinite); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, "UPDATE "+ranges+" SET dirty=true WHERE id=(SELECT max(id) FROM "+ranges+")"); err != nil {
			return false, err
		}
	}
	c.Progress.Rows += a.Rows
	c.Progress.Bytes += a.Bytes
	if dirty == nil {
		if a.More {
			c.Cursor = a.Last
		} else {
			c.Table++
			c.Cursor = nil
			c.Progress.TablesCompleted = c.Table
		}
	}
	c.Progress.Reason = ""
	return true, nil
}
func sameVerificationRelation(a, b relation) bool {
	a = preserveMissingMetadata([]relation{a}, []relation{b})[0]
	encoded, _ := json.Marshal(a)
	expected, _ := json.Marshal(b)
	return string(encoded) == string(expected)
}
