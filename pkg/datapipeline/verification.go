package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

// VerificationProgress contains counters and selected table names. Boundaries and customer keys
// stay in the private destination receipt, never in catalog or HTTP metadata.
type VerificationProgress struct {
	Tables                            []string `json:",omitempty"`
	Restarts                          int
	IntentID                          string
	ID, State, Phase, Table, Reason   string
	Rows, Bytes, Ranges, DirtyRanges  int64
	BackfilledRows                    int64
	TablesCompleted, TablesTotal      int
	StartedAt, UpdatedAt, CompletedAt time.Time
}

type verificationCheckpoint struct {
	Tables     []string `json:",omitempty"`
	Storage    []string
	Progress   VerificationProgress
	Generation int64
	Spec       string
	Table      int
	Cursor     []string
}

func verificationID(p Pipeline, relations []relation) (string, string) {
	encoded, _ := json.Marshal(relations)
	sum := sha256.Sum256(encoded)
	spec := hex.EncodeToString(sum[:])
	schema := ""
	attempt := p.Revision
	if p.SchemaChange != nil && p.SchemaApproval == p.SchemaChange.ID && p.SchemaChange.State != "complete" && p.SchemaChange.State != "withdrawn" {
		schema = p.SchemaChange.ID
		attempt = int64(p.SchemaChange.Retry)
	}
	encoded, _ = json.Marshal(struct {
		Pipeline, Spec, Schema string
		Generation, Attempt    int64
	}{p.ID, spec, schema, p.Generation, attempt})
	sum = sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), spec
}

func (s *Service) CancelVerification(ctx context.Context, account, id string, revision int64, run string) error {
	if err := s.admit(ctx, account); err != nil {
		return err
	}
	if !validID(account) || !validID(id) || revision < 1 || len(run) != 64 {
		return ErrInvalid
	}
	if _, err := hex.DecodeString(run); err != nil {
		return ErrInvalid
	}
	store, ok := s.Store.(interface {
		CancelVerification(context.Context, string, string, int64, string) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.CancelVerification(ctx, account, id, revision, run)
}
func (s *PostgresStore) CancelVerification(ctx context.Context, account, id string, revision int64, run string) error {
	count, err := s.q.PipelineCancelVerification(ctx, s.pool, sqlc.PipelineCancelVerificationParams{AccountID: account, ID: id, Revision: revision, RunID: run})
	if err != nil {
		return storeError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
