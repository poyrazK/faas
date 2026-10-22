package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const DeployMCPAdmission DeploymentAuditKind = "deploy.mcp_admission"

var ErrMCPAdmissionPersistence = errors.New("MCP admission evidence persistence failed")

type mcpAdmissionCaptureKey struct{}
type mcpAdmissionCapture struct {
	Record *api.MCPAdmissionEvidence
	Guard  mcpAccessPolicyGuard
}

func captureMCPAdmission(ctx context.Context, deploymentID string, percent int) (context.Context, *mcpAdmissionCapture) {
	guard, ok := ctx.Value(mcpAccessPolicyContextKey{}).(mcpAccessPolicyGuard)
	if !ok {
		return ctx, nil
	}
	parsed, err := uuid.Parse(deploymentID)
	if err != nil {
		return ctx, nil
	}
	deploymentID = parsed.String()
	capture := &mcpAdmissionCapture{Guard: guard}
	record := &api.MCPAdmissionEvidence{Version: 1, ID: uuid.NewString(), DeploymentID: deploymentID, TrafficPercent: percent, ExpectedFingerprint: guard.Expectation.Fingerprint, ExpectedRevision: guard.Expectation.AccountRevision, Gateways: []api.MCPAdmissionGateway{}}
	// Capture becomes visible only after ownership was verified inside the guard.
	return context.WithValue(ctx, mcpAdmissionCaptureKey{}, &mcpAdmissionPending{capture, record}), capture
}

type mcpAdmissionPending struct {
	Capture *mcpAdmissionCapture
	Record  *api.MCPAdmissionEvidence
}

func observeMCPAdmission(ctx context.Context, fingerprint string, evidence *sqlc.ReadMCPGatewayPromotionEvidenceRow) error {
	pending, ok := ctx.Value(mcpAdmissionCaptureKey{}).(*mcpAdmissionPending)
	if !ok {
		return nil
	}
	record := pending.Record
	record.ObservedFingerprint = fingerprint
	record.CapturedAt = time.Now().UTC()
	if evidence != nil {
		record.CapturedAt = evidence.CapturedAt.Time
		revision := evidence.Revision
		record.ObservedRevision = &revision
		if err := json.Unmarshal(evidence.Gateways, &record.Gateways); err != nil {
			return err
		}
	}
	pending.Capture.Record = record
	return nil
}
func mcpAdmissionReason(err error) string {
	switch {
	case errors.Is(err, ErrMCPAdmissionPersistence):
		return api.CodeMCPAdmissionEvidenceUnavailable
	case errors.Is(err, ErrMCPAccessPolicyChanged):
		return api.CodeMCPAccessPolicyChanged
	case errors.Is(err, ErrMCPAccountRevisionChanged):
		return api.CodeMCPAccountRevisionChanged
	case errors.Is(err, ErrMCPGatewayEvidenceUnavailable):
		return api.CodeMCPGatewayEvidenceUnavailable
	case errors.Is(err, ErrMCPGatewayEvidenceStale):
		return api.CodeMCPGatewayEvidenceStale
	case errors.Is(err, ErrMCPGatewayReplayPending):
		return api.CodeMCPGatewayReplayPending
	case errors.Is(err, ErrTrafficServingChanged):
		return api.CodeTrafficServingChanged
	default:
		return "traffic_update_failed"
	}
}
func appendMCPAdmission(ctx context.Context, db sqlc.DBTX, capture *mcpAdmissionCapture, outcome, reason string) error {
	if capture == nil || capture.Record == nil {
		return nil
	}
	capture.Record.Outcome = outcome
	capture.Record.Reason = reason
	data, err := json.Marshal(map[string]any{"mcp_admission": capture.Record})
	if err != nil {
		return err
	}
	inserted, err := sqlc.New().AppendMCPAdmissionEvidence(ctx, db, sqlc.AppendMCPAdmissionEvidenceParams{DeploymentID: mustPgUUID(capture.Record.DeploymentID), AppID: mustPgUUID(capture.Guard.AppID), AccountID: mustPgUUID(capture.Guard.AccountID), Data: data})
	if err != nil {
		return err
	}
	if inserted == 0 {
		exists, err := sqlc.New().MCPAdmissionEvidenceExists(ctx, db, sqlc.MCPAdmissionEvidenceExistsParams{AccountID: mustPgUUID(capture.Guard.AccountID), EvidenceID: capture.Record.ID})
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}
func (s *PgStore) finishMCPAdmission(ctx context.Context, tx pgx.Tx, capture *mcpAdmissionCapture, resultErr *error) {
	// Roll back before borrowing another connection: never retain partial traffic.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	_ = tx.Rollback(cleanupCtx)
	cleanupCancel()
	if *resultErr == nil || capture == nil || capture.Record == nil {
		return
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := appendMCPAdmission(auditCtx, s.pool, capture, "rejected", mcpAdmissionReason(*resultErr)); err != nil {
		*resultErr = errors.Join(*resultErr, fmt.Errorf("%w: %w", ErrMCPAdmissionPersistence, err))
	}
}
func (m *MemStore) finishMCPAdmissionLocked(capture *mcpAdmissionCapture, resultErr error) {
	if capture == nil || capture.Record == nil {
		return
	}
	outcome, reason := "accepted", ""
	if resultErr != nil {
		outcome = "rejected"
		reason = mcpAdmissionReason(resultErr)
	}
	capture.Record.Outcome = outcome
	capture.Record.Reason = reason
	data, _ := json.Marshal(map[string]any{"mcp_admission": capture.Record})
	account := uuid.MustParse(capture.Guard.AccountID)
	_, _ = m.appendDeploymentAuditLocked(DeploymentAudit{DeploymentID: uuid.MustParse(capture.Record.DeploymentID), AccountID: &account, Kind: DeployMCPAdmission, Actor: "apid:mcp_traffic_admission", Data: data})
}
