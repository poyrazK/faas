package state

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type ExecutionEnvironmentDiagnosticStore interface {
	AppendExecutionEnvironmentEvent(context.Context, ExecutionEnvironment, api.ExecutionEnvironmentEvent) error
}

func validateEnvironmentEvent(e api.ExecutionEnvironmentEvent) error {
	if !slices.Contains([]string{"build", "export", "verify", "scan", "publish"}, e.Stage) || e.Code != "" && !slices.Contains([]string{"build_output", "progress", "log_gap", "logs_unavailable", "dependency_resolution", "integrity_mismatch", "size_limit", "scan_failed", "scan_unavailable", "scan_rejected", "build_timeout", "builder_unavailable", "build_failed", "image_failed"}, e.Code) || !utf8.ValidString(e.Message) || len(e.Message) > api.ExecutionEnvironmentDiagnosticMaxLineBytes || strings.IndexFunc(e.Message, unicode.IsControl) >= 0 {
		return ErrExecutionInvalid
	}
	return nil
}

func (m *MemStore) AppendExecutionEnvironmentEvent(_ context.Context, claim ExecutionEnvironment, e api.ExecutionEnvironmentEvent) error {
	if err := validateEnvironmentEvent(e); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.executionEnvironments[claim.ID]
	if !ok || r.AccountID != claim.AccountID || r.Lease != claim.Lease || r.Status != claim.Status || (runenv.PublicStatus(r.Status) != "building" && runenv.PublicStatus(r.Status) != "imaging") || !r.LeaseExpiresAt.After(time.Now()) {
		return ErrExecutionEnvironmentLease
	}
	r.DiagnosticSequence++
	e.Sequence = r.DiagnosticSequence
	e.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	e.Attempt = r.BuildAttempts
	if runenv.PublicStatus(r.Status) == "imaging" {
		e.Attempt = r.ImageAttempts
	}
	r.Diagnostics = append(append([]api.ExecutionEnvironmentEvent(nil), r.Diagnostics...), e)
	if len(r.Diagnostics) > api.ExecutionEnvironmentDiagnosticMaxEvents {
		r.Diagnostics = r.Diagnostics[len(r.Diagnostics)-api.ExecutionEnvironmentDiagnosticMaxEvents:]
	}
	m.executionEnvironments[r.ID] = r
	return nil
}

func (s *PgStore) AppendExecutionEnvironmentEvent(ctx context.Context, claim ExecutionEnvironment, e api.ExecutionEnvironmentEvent) error {
	if err := validateEnvironmentEvent(e); err != nil {
		return err
	}
	if !validEnvironmentID(claim.ID) || !validEnvironmentID(claim.AccountID) {
		return ErrExecutionInvalid
	}
	n, err := sqlc.New().ExecutionEnvironmentAppendDiagnostic(ctx, s.pool, sqlc.ExecutionEnvironmentAppendDiagnosticParams{ID: mustPgUUID(claim.ID), AccountID: mustPgUUID(claim.AccountID), Lease: claim.Lease, Status: claim.Status, Stage: e.Stage, Code: e.Code, Message: e.Message, MaxEvents: api.ExecutionEnvironmentDiagnosticMaxEvents})
	if err != nil {
		return errors.Join(ErrExecutionEnvironmentLease, err)
	}
	return environmentPublication(n, nil)
}
