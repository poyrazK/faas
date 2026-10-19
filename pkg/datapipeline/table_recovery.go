package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

// TableRecovery authorizes replacement of one failed table's imported rows.
// The approval binds source definition, generation, native identities and attempt.
type TableRecovery struct {
	Kind                                      string `json:",omitempty"` // Empty retains existing mismatch approval hashes.
	ID, Table, State, Stage                   string
	TableOID, SubscriptionOID, PublicationOID uint32
	Attempt                                   int
}

func CanRecoverTable(p Pipeline) bool {
	return len(RecoverableTables(p)) > 0
}

func RecoverableTables(p Pipeline) []string {
	if activeTableCancellation(p) {
		return nil
	}
	v := p.VerificationProgress
	if !tableWorkflow(p) || p.SchemaApproval != p.SchemaChange.ID || !p.SchemaChange.Supported || p.State != "blocked" || p.DesiredState != "running" || !p.ObservationFresh || p.SchemaChange.Retry >= api.DataPipelineSchemaRetriesMax {
		return nil
	}
	if r := p.SchemaChange.Recovery; r != nil && r.State != "complete" && r.State != "blocked" && r.State != "detected" && r.Stage != "verifying" {
		return nil
	}
	if liveTableRepair(p.SchemaChange) {
		if (p.Reason == "live_table_mismatch" || p.Reason == "replica_mismatch") && v != nil && v.State == "mismatch" && v.Table == p.SchemaChange.Recovery.Table {
			return []string{v.Table}
		}
		return nil
	}
	if p.Reason == "replica_mismatch" && v != nil && v.State == "mismatch" && containsTable(p.SchemaChange.AddedTables, v.Table) {
		return []string{v.Table}
	}
	if p.Reason != "table_copy_stalled" && p.Reason != "table_addition_tracking_lost" {
		return nil
	}
	result := []string{}
	for _, c := range p.SchemaChange.Copies {
		if containsTable(p.SchemaChange.AddedTables, c.Table) && c.NativeState != "r" && (c.Stalled || c.Phase == "missing") {
			if slices.ContainsFunc(p.SchemaChange.Copies, func(other TableCopyProgress) bool { return other.Table != c.Table && other.NativeState == "" }) {
				continue
			}
			result = append(result, c.Table)
		}
	}
	return result
}

func tableRecoveryID(p Pipeline, r TableRecovery) string {
	r.ID, r.State, r.Stage = "", "", ""
	encoded, _ := json.Marshal(struct {
		Pipeline, Change string
		Generation       int64
		Recovery         TableRecovery
	}{p.ID, p.SchemaApproval, p.Generation, r})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *Service) TableRecovery(ctx context.Context, account, id string, revision int64, approval string, tables ...string) error {
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
		if err := s.tableCapacity(ctx, account, id); err != nil {
			return err
		}
	}
	store, ok := s.Store.(interface {
		TableRecovery(context.Context, string, string, int64, string, ...string) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.TableRecovery(ctx, account, id, revision, approval, tables...)
}

func (s *PostgresStore) TableRecovery(ctx context.Context, account, id string, revision int64, approval string, tables ...string) error {
	if !validID(account) || !validID(id) || revision < 1 {
		return ErrInvalid
	}
	var n int64
	var err error
	if approval == "" {
		p, getErr := s.Get(ctx, account, id)
		if errors.Is(getErr, ErrNotFound) {
			return ErrConflict
		}
		if getErr != nil {
			return getErr
		}
		choices := RecoverableTables(p)
		if len(tables) > 1 {
			return ErrInvalid
		}
		if len(choices) == 0 || p.Revision != revision {
			return ErrConflict
		}
		table := choices[0]
		if len(tables) == 1 && tables[0] != "" {
			table = tables[0]
		}
		if !containsTable(choices, table) {
			return ErrConflict
		}
		kind := "copy"
		if liveTableRepair(p.SchemaChange) {
			kind = "live"
		} else if p.Reason == "replica_mismatch" {
			kind = ""
		}
		n, err = s.q.PipelineRequestTableRecovery(ctx, s.pool, sqlc.PipelineRequestTableRecoveryParams{AccountID: account, ID: id, Revision: revision, TableName: table, Kind: kind, MaxRetries: api.DataPipelineSchemaRetriesMax, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
	} else {
		n, err = s.q.PipelineApproveTableRecovery(ctx, s.pool, sqlc.PipelineApproveTableRecoveryParams{AccountID: account, ID: id, Revision: revision, RecoveryID: approval, MaxRetries: api.DataPipelineSchemaRetriesMax, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
	}
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
