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
	Data          []json.RawMessage
}

func verificationQuery(r relation, lower, upper []string) (string, []any) {
	keys := primaryColumns(r)
	args := []any{}
	names, order, textKeys := []string{}, []string{}, []string{}
	for _, c := range r.Columns {
		names = append(names, ident(c.Name))
	}
	for _, c := range keys {
		order = append(order, collated(ident(c.Name), c))
		textKeys = append(textKeys, ident(c.Name)+"::text")
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
	byteLimit := "$" + strconv.Itoa(len(args))
	query := "SELECT substring(pg_catalog.convert_to(pg_catalog.to_jsonb(t)::text,'UTF8') FROM 1 FOR " + byteLimit + ")," + strings.Join(textKeys, ",") + " FROM (SELECT " + strings.Join(names, ",") + " FROM " + tableIdent(r.Name) + where + " ORDER BY " + strings.Join(order, ",") + " LIMIT " + rowLimit + ") t"
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
	query, args := verificationQuery(r, lower, upper)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return verificationDigest{}, err
	}
	defer rows.Close()
	result := verificationDigest{}
	sum := sha256.New()
	keys := primaryColumns(r)
	for rows.Next() {
		var data []byte
		values := make([]string, len(keys))
		dest := []any{&data}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if err = rows.Scan(dest...); err != nil {
			return result, err
		}
		if len(data) > api.DataPipelineVerificationBytesMax {
			result.Limited = true
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
	storage, err := verificationSourceStorage(ctx, sourceTx, []relation{r})
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
		c.Progress.State, c.Progress.Reason = "limited", "verification_row_limit"
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
		c.Progress.State, c.Progress.Reason = "limited", "verification_row_limit"
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
	encoded, _ := json.Marshal(a)
	expected, _ := json.Marshal(b)
	return string(encoded) == string(expected)
}
