package datapipeline

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func qualityQuery(p Pipeline, rule api.DataPipelineQualityRule, relations []relation) (string, []any, error) {
	index := slices.IndexFunc(relations, func(r relation) bool { return r.Name == rule.Table })
	if index < 0 {
		return "", nil, ErrInvalid
	}
	r := relations[index]
	columns, nonNull, order := []string{}, []string{}, []string{}
	for _, name := range rule.Columns {
		idx := slices.IndexFunc(r.Columns, func(c column) bool { return c.Name == name })
		if idx < 0 {
			return "", nil, ErrInvalid
		}
		columns = append(columns, "s."+ident(name))
		nonNull = append(nonNull, "s."+ident(name)+" IS NOT NULL")
		if rule.Kind == "unique" && r.Columns[idx].Type == "json" {
			return "", nil, ErrInvalid
		}
		if rule.Kind == "range" && !slices.Contains([]string{"int2", "int4", "int8", "numeric", "float4", "float8"}, r.Columns[idx].Type) {
			return "", nil, ErrInvalid
		}
	}
	for _, key := range primaryColumns(r) {
		order = append(order, collated("s."+ident(key.Name), key))
	}
	if len(order) == 0 {
		return "", nil, ErrInvalid
	}
	args := []any{api.DataPipelineQualityScanRowsMax + 1}
	violations := ""
	switch rule.Kind {
	case "required":
		predicates := make([]string, len(columns))
		for i, c := range columns {
			predicates[i] = c + " IS NULL"
		}
		violations = "SELECT count(*) FROM bounded s WHERE " + strings.Join(predicates, " OR ")
	case "unique":
		violations = "SELECT coalesce(sum(n-1),0)::bigint FROM (SELECT count(*) AS n FROM bounded s WHERE " + strings.Join(nonNull, " AND ") + " GROUP BY " + strings.Join(columns, ",") + " HAVING count(*)>1) duplicate_groups"
	case "range":
		predicates := []string{columns[0] + "::text IN ('NaN','Infinity','-Infinity')"}
		for i, bound := range []string{rule.Minimum, rule.Maximum} {
			if bound == "" {
				continue
			}
			args = append(args, bound)
			op := "<"
			if i == 1 {
				op = ">"
			}
			predicates = append(predicates, columns[0]+"::numeric"+op+"$"+strconv.Itoa(len(args))+"::numeric")
		}
		violations = "SELECT count(*) FROM bounded s WHERE " + columns[0] + " IS NOT NULL AND (" + strings.Join(predicates, " OR ") + ")"
	case "reference":
		link := api.DataPipelineRelationship{SourceTable: rule.Table, TargetTable: rule.ReferenceTable, SourceColumns: rule.Columns, TargetColumns: rule.ReferenceColumns}
		if _, err := relationshipBody(p, link, relations); err != nil {
			return "", nil, err
		}
		joins := []string{}
		for i, name := range rule.Columns {
			joins = append(joins, "t."+ident(rule.ReferenceColumns[i])+"=s."+ident(name))
		}
		if a := tenantAccess(p); a != nil {
			joins = append(joins, "t."+ident(a.Column)+`::text COLLATE "C"=s.`+ident(a.Column)+`::text COLLATE "C"`)
		}
		violations = "SELECT count(*) FROM bounded s WHERE " + strings.Join(nonNull, " AND ") + " AND NOT EXISTS(SELECT 1 FROM " + tableIdent(rule.ReferenceTable) + " t WHERE " + strings.Join(joins, " AND ") + ")"
	default:
		return "", nil, ErrInvalid
	}
	// MATERIALIZED bounds tuple count before applying a rule. A lookahead row
	// makes incomplete coverage explicit rather than declaring a sampled pass.
	query := "WITH sample AS MATERIALIZED (SELECT s.* FROM " + tableIdent(rule.Table) + " s ORDER BY " + strings.Join(order, ",") + " LIMIT $1),bounded AS (SELECT * FROM sample LIMIT ($1::bigint-1)) SELECT (SELECT count(*) FROM sample)>$1::bigint-1,(" + violations + ")"
	return query, args, nil
}

func (e *Engine) observeQuality(ctx context.Context, p Pipeline, ob *Observation, current func(context.Context) error) {
	if p.Operations == nil || p.Operations.Quality == nil || !workAllowed(p, time.Now()) {
		return
	}
	status := ob.OperationsStatus
	if status == nil {
		status = operationsStatus(p)
	}
	if status.Quality != nil && status.Quality.Generation == p.Generation && !runDue(status.Quality.NextRunAt, time.Now()) {
		return
	}
	now := time.Now().UTC()
	result := &api.DataPipelineQualityStatus{State: "unknown", CheckedAt: now.Format(time.RFC3339Nano), NextRunAt: nextWorkTime(p, now.Add(time.Duration(p.Operations.Quality.EverySeconds)*time.Second)).Format(time.RFC3339Nano), Generation: p.Generation, Results: []api.DataPipelineQualityResult{}}
	for _, r := range p.Operations.Quality.Rules {
		result.Results = append(result.Results, api.DataPipelineQualityResult{Name: r.Name, State: "unknown", CountIsLowerBound: true})
	}
	status.Quality = result
	ob.OperationsStatus = status
	err := e.withTarget(ctx, p, current, func(target *pgx.Conn, _ string) error {
		tx, err := target.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err = verificationSetting(ctx, tx); err != nil {
			return err
		}
		var encoded []byte
		if err = tx.QueryRow(ctx, `SELECT schema_spec FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND readable AND NOT retiring`, p.ID, p.Generation).Scan(&encoded); err != nil {
			return err
		}
		var relations []relation
		if json.Unmarshal(encoded, &relations) != nil {
			return ErrConflict
		}
		// Validate and lock all dependencies before any aggregate is evaluated.
		for _, r := range relations {
			if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(r.Name)+" IN ACCESS SHARE MODE"); err != nil {
				return err
			}
			if err = validateSnapshotTarget(ctx, p, tx, r); err != nil {
				return err
			}
			actual, _, err := inspectSourceTableMode(ctx, tx, r.Name, true)
			if err != nil {
				return err
			}
			if err = validateOwnedReadPolicy(ctx, p, tx, r.Name, actual.OID); err != nil {
				return err
			}
			var closed bool
			if err = tx.QueryRow(ctx, `SELECT pg_get_expr(polqual,polrelid)='false' FROM pg_policy WHERE polrelid=$1 AND polname='gregale_pipeline_read'`, actual.OID).Scan(&closed); err != nil {
				return err
			}
			if closed {
				return ErrConflict
			}
		}
		for i, rule := range p.Operations.Quality.Rules {
			query, args, err := qualityQuery(p, rule, relations)
			if err != nil {
				return err
			}
			var more bool
			var count int64
			if err = tx.QueryRow(ctx, query, args...).Scan(&more, &count); err != nil {
				return err
			}
			r := &result.Results[i]
			r.State = "passed"
			r.Violations = count
			r.CountIsLowerBound = more
			if more {
				r.State = "inconclusive"
			}
			if count > 0 {
				r.State = "failed"
			}
		}
		if err = current(ctx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	if err != nil {
		result.State = "unknown"
	}
	if err == nil {
		result.State = "passed"
	}
	for _, r := range result.Results {
		if r.State == "failed" {
			result.State = "failed"
			break
		}
		if r.State == "unknown" {
			result.State = "unknown"
		} else if r.State != "passed" && result.State == "passed" {
			result.State = "inconclusive"
		}
	}
}
