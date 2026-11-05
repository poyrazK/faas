package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

// TableCancellation authorizes retirement of one table. Empty Kind denotes an
// unfinished addition; live denotes a completed table. Rows are retained privately.
type TableCancellation struct {
	Kind                                                      string `json:",omitempty"`
	RefreshRetry                                              int    `json:",omitempty"`
	ID, RequestID, Table, State, Stage, Retention             string
	TableOID, SourceTableOID, SubscriptionOID, PublicationOID uint32
}

func activeTableCancellation(p Pipeline) bool {
	return p.SchemaChange != nil && p.SchemaChange.Cancellation != nil && slices.Contains([]string{"requested", "pending", "approved"}, p.SchemaChange.Cancellation.State)
}

func CancellableTables(p Pipeline) []string {
	if liveTableRepair(p.SchemaChange) {
		return nil
	}
	if !tableWorkflow(p) || p.SchemaApproval != p.SchemaChange.ID || p.DesiredState != "running" || !p.ObservationFresh || !slices.Contains([]string{"live", "copying", "blocked"}, p.State) || len(p.SchemaChange.Before) == 0 {
		return nil
	}
	if c := p.SchemaChange.Cancellation; c != nil && c.State == "approved" {
		return nil
	}
	if r := p.SchemaChange.Recovery; r != nil && r.State == "approved" && r.Stage != "verifying" {
		return []string{r.Table}
	}
	return slices.Clone(p.SchemaChange.AddedTables)
}

func tableCancellationID(p Pipeline, c TableCancellation) string {
	c.ID, c.State, c.Stage = "", "", ""
	c.RefreshRetry = 0
	encoded, _ := json.Marshal(struct {
		Pipeline, Change string
		Generation       int64
		Cancellation     TableCancellation
	}{p.ID, p.SchemaApproval, p.Generation, c})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *Service) TableCancellation(ctx context.Context, account, id string, revision int64, table, approval string) error {
	if err := s.admit(ctx, account); err != nil {
		return err
	}
	if !validID(account) || !validID(id) || revision < 1 {
		return ErrInvalid
	}
	if approval != "" {
		if len(approval) != 64 {
			return ErrInvalid
		}
		if _, err := hex.DecodeString(approval); err != nil {
			return ErrInvalid
		}
	}
	store, ok := s.Store.(interface {
		TableCancellation(context.Context, string, string, int64, string, string) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.TableCancellation(ctx, account, id, revision, table, approval)
}

func (s *PostgresStore) TableCancellation(ctx context.Context, account, id string, revision int64, table, approval string) error {
	p, err := s.Get(ctx, account, id)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if p.Revision != revision {
		return ErrConflict
	}
	if p.SchemaChange != nil && p.SchemaChange.Cancellation != nil && p.SchemaChange.Cancellation.Kind != "" {
		return ErrConflict
	}
	if approval != "" && CanRetryTableRetirement(p) {
		return s.retryTableRetirement(ctx, p, approval)
	}
	var n int64
	if approval == "" && table == "" {
		n, err = s.q.PipelineDiscardTableCancellation(ctx, s.pool, sqlc.PipelineDiscardTableCancellationParams{AccountID: account, ID: id, Revision: revision})
	} else if approval == "" {
		if !slices.Contains(CancellableTables(p), table) {
			return ErrConflict
		}
		n, err = s.q.PipelineRequestTableCancellation(ctx, s.pool, sqlc.PipelineRequestTableCancellationParams{AccountID: account, ID: id, Revision: revision, TableName: table, RequestID: uuid.NewString(), MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
	} else {
		n, err = s.q.PipelineApproveTableCancellation(ctx, s.pool, sqlc.PipelineApproveTableCancellationParams{AccountID: account, ID: id, Revision: revision, CancellationID: approval, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
	}
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// CanRetryTableRetirement authorizes only a new API refresh attempt for an
// already archived table. It preserves the original retirement approval.
func CanRetryTableRetirement(p Pipeline) bool {
	if p.SchemaChange == nil || p.SchemaChange.Cancellation == nil {
		return false
	}
	c := p.SchemaChange.Cancellation
	return p.DesiredState == "running" && p.State == "blocked" && p.Reason == "schema_api_refresh_failed" && p.ObservationFresh &&
		p.SchemaApproval == p.SchemaChange.ID && c.State == "approved" && c.Stage == "archived" && c.RefreshRetry < api.DataPipelineSchemaRetriesMax && c.ID == tableCancellationID(p, *c) &&
		(p.ReaderChange == nil || p.ReaderChange.State != "approved")
}

func (s *PostgresStore) retryTableRetirement(ctx context.Context, p Pipeline, approval string) error {
	n, err := s.q.PipelineRetryTableRetirement(ctx, s.pool, sqlc.PipelineRetryTableRetirementParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, CancellationID: approval, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds()), MaxRetries: int32(api.DataPipelineSchemaRetriesMax)})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// A subset of an approved addition needs no new exposure approval. A fresh
// selection identity prevents a later re-add from adopting an old proof/copy.
func cancellationResult(p Pipeline) SchemaChange {
	c := *p.SchemaChange.Cancellation
	c.State, c.Stage = "complete", "complete"
	after := slices.DeleteFunc(slices.Clone(p.SchemaChange.After), func(r relation) bool { return r.Name == c.Table })
	seed := *p.SchemaChange
	seed.SelectionID = c.ID
	p.SchemaChange = &seed
	before := seed.Before
	if c.Kind == "live" {
		before = after
	}
	result := tablePlan(p, before, after)
	result.Retry, result.Cancellation = seed.Retry, &c
	result.Copies = slices.DeleteFunc(slices.Clone(seed.Copies), func(copy TableCopyProgress) bool { return copy.Table == c.Table })
	result.State = "verifying"
	if len(result.AddedTables) == 0 {
		result.State, result.Supported = "complete", true
	}
	return result
}
