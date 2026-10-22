package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func tableWorkflow(p Pipeline) bool {
	return p.SchemaChange != nil && len(CopiedTables(p.SchemaChange)) > 0 && p.SchemaChange.State != "complete"
}
func relationNames(relations []relation) []string {
	names := make([]string, len(relations))
	for i, r := range relations {
		names[i] = r.Name
	}
	return names
}
func tablePlan(p Pipeline, before, after []relation) SchemaChange {
	plan := schemaPlan(p, before, after) // Reuse exact definition/generation identity.
	if p.SchemaChange != nil && p.SchemaChange.SelectionID != "" {
		plan.SelectionID = p.SchemaChange.SelectionID
		sum := sha256.Sum256([]byte(plan.ID + ":" + plan.SelectionID))
		plan.ID = hex.EncodeToString(sum[:])
	}
	plan.State, plan.Supported, plan.Changes = "pending", len(before) > 0, nil
	plan.RequestedTables = relationNames(after)
	for _, old := range before {
		i := slices.IndexFunc(after, func(r relation) bool { return r.Name == old.Name })
		if i < 0 || !reflect.DeepEqual(old, after[i]) {
			plan.Supported = false
		}
	}
	for _, r := range after {
		if slices.ContainsFunc(before, func(old relation) bool { return old.Name == r.Name }) {
			continue
		}
		safe := !r.Unsupported && len(primaryColumns(r)) > 0
		for _, c := range r.Columns {
			safe = safe && !c.Unsupported
		}
		plan.AddedTables = append(plan.AddedTables, r.Name)
		plan.Changes = append(plan.Changes, SchemaColumnChange{Table: r.Name, Column: "*", Kind: "table_added", Safe: safe, Reason: "Copy and synchronize this table. Every saved reader can read every row after verification and API refresh."})
		plan.Supported = plan.Supported && safe
	}
	plan.Supported = plan.Supported && len(plan.AddedTables) > 0 && len(after) <= api.DataPipelineTablesMax
	if !plan.Supported {
		plan.State = "unsupported"
	}
	return plan
}

func (s *Service) tableCapacity(ctx context.Context, account, id string) error {
	if s.CapacityStatus == nil {
		return ErrUnavailable
	}
	c, err := s.CapacityStatus(ctx, account, id)
	if err != nil {
		return err
	}
	if c == nil || c.State != "ready" {
		return connectioncapacity.ErrUnknown
	}
	return nil
}
func (s *Service) RequestTables(ctx context.Context, account, id string, revision int64, tables []string) error {
	if err := s.admit(ctx, account); err != nil {
		return err
	}
	if err := s.tableCapacity(ctx, account, id); err != nil {
		return err
	}
	store, ok := s.Store.(interface {
		RequestTables(context.Context, string, string, int64, []string) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.RequestTables(ctx, account, id, revision, tables)
}
func (s *Service) CancelTables(ctx context.Context, account, id string, revision int64) error {
	if err := s.admit(ctx, account); err != nil {
		return err
	}
	store, ok := s.Store.(interface {
		CancelTables(context.Context, string, string, int64) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.CancelTables(ctx, account, id, revision)
}

// Existing pipelines consume their reserved peak; discovery need not reserve a
// second pipeline or require an empty destination. Capacity is separately checked
// against the existing reservation and again by the worker before remote changes.
func checkExistingSelection(c api.DataConnectionCheck, p Pipeline, tables []string) error {
	if !c.Fresh || (c.State != "passed" && c.State != "blocked") || c.DatabaseID != "" && c.DatabaseID != p.DatabaseID {
		return ErrCheckRequired
	}
	for _, issue := range c.Checks {
		if strings.HasPrefix(issue.Code, "source_capacity_") || strings.HasPrefix(issue.Code, "mirror_capacity_") || slices.Contains([]string{"destination_not_empty", "source_replication_slots_capacity", "source_wal_senders_capacity", "destination_replication_workers_capacity"}, issue.Code) {
			continue
		}
		if issue.State != "passed" {
			return ErrCheckRequired
		}
	}
	c.State = "passed"
	return checkSelection(c, tables)
}
func (s *PostgresStore) checkedTables(ctx context.Context, tx pgx.Tx, p Pipeline, tables []string) error {
	r, err := s.q.PipelineCheckForCreate(ctx, tx, sqlc.PipelineCheckForCreateParams{AccountID: p.AccountID, ConnectionID: p.SourceConnectionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckRequired
	}
	if err != nil {
		return storeError(err)
	}
	c, err := describeCheck(sqlc.PipelineCheckGetRow(r))
	if err != nil {
		return err
	}
	return checkExistingSelection(c.DataConnectionCheck, p, tables)
}
func (s *PostgresStore) RequestTables(ctx context.Context, account, id string, revision int64, added []string) error {
	p, err := s.Get(ctx, account, id)
	if err != nil {
		return err
	}
	if revision < 1 || len(added) == 0 {
		return ErrInvalid
	}
	if revision != p.Revision {
		return ErrConflict
	}
	tables := append(slices.Clone(p.Tables), added...)
	if err = ValidateCreate(api.CreateDataPipelineRequest{Name: p.Name, SourceConnectionID: p.SourceConnectionID, DatabaseID: p.DatabaseID, ReadSubjects: p.ReadSubjects, Tables: tables}); err != nil {
		return err
	}
	slices.Sort(tables)
	added = slices.Clone(added)
	slices.Sort(added)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.checkedTables(ctx, tx, p, tables); err != nil {
		return err
	}
	encoded, _ := json.Marshal(SchemaChange{SelectionID: uuid.NewString(), State: "requested", AddedTables: added, RequestedTables: tables})
	n, err := s.q.PipelineRequestTables(ctx, tx, sqlc.PipelineRequestTablesParams{AccountID: account, ID: id, Revision: revision, Plan: encoded, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return storeError(tx.Commit(ctx))
}
func (s *PostgresStore) CancelTables(ctx context.Context, account, id string, revision int64) error {
	if !validID(account) || !validID(id) || revision < 1 {
		return ErrInvalid
	}
	n, err := s.q.PipelineCancelTables(ctx, s.pool, sqlc.PipelineCancelTablesParams{AccountID: account, ID: id, Revision: revision})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
