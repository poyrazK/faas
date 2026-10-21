package datapipeline

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

// RemovableTables exposes only completed, freshly observed selections. The
// ordinary disconnect action owns stopping the final table/pipeline.
func RemovableTables(p Pipeline) []string {
	if p.State != "live" || p.DesiredState != "running" || !p.Initialized || !p.ObservationFresh || len(p.Tables) < 2 || p.VerifyRequested || activeTableCancellation(p) {
		return nil
	}
	if p.SchemaChange != nil && p.SchemaChange.State != "complete" && p.SchemaChange.State != "withdrawn" {
		return nil
	}
	if p.VerificationProgress != nil && slices.Contains([]string{"running", "waiting"}, p.VerificationProgress.State) {
		return nil
	}
	return slices.Clone(p.Tables)
}

func (s *Service) TableRemoval(ctx context.Context, account, id string, revision int64, table, approval string) error {
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
		TableRemoval(context.Context, string, string, int64, string, string) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.TableRemoval(ctx, account, id, revision, table, approval)
}

func (s *PostgresStore) TableRemoval(ctx context.Context, account, id string, revision int64, table, approval string) error {
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
	var n int64
	if approval == "" && table != "" {
		if !slices.Contains(RemovableTables(p), table) {
			return ErrConflict
		}
		plan, _ := json.Marshal(SchemaChange{State: "removal_requested", Supported: true, RequestedTables: p.Tables, Cancellation: &TableCancellation{Kind: "live", Table: table, State: "requested", RequestID: uuid.NewString(), Retention: "retain_private"}})
		n, err = s.q.PipelineRequestTableRemoval(ctx, s.pool, sqlc.PipelineRequestTableRemovalParams{AccountID: account, ID: id, Revision: revision, TableName: table, Plan: plan, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
	} else {
		if p.SchemaChange == nil || p.SchemaChange.Cancellation == nil || p.SchemaChange.Cancellation.Kind != "live" {
			return ErrConflict
		}
		if approval == "" {
			n, err = s.q.PipelineDiscardTableCancellation(ctx, s.pool, sqlc.PipelineDiscardTableCancellationParams{AccountID: account, ID: id, Revision: revision})
		} else {
			n, err = s.q.PipelineApproveTableRemoval(ctx, s.pool, sqlc.PipelineApproveTableRemovalParams{AccountID: account, ID: id, Revision: revision, CancellationID: approval, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
		}
	}
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
