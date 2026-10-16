package state

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	ErrMCPAccountRevisionChanged     = errors.New("MCP account policy revision changed")
	ErrMCPGatewayEvidenceUnavailable = errors.New("MCP gateway evidence unavailable")
	ErrMCPGatewayEvidenceStale       = errors.New("MCP gateway evidence stale")
	ErrMCPGatewayReplayPending       = errors.New("MCP gateway replay pending")
)

// Evidence is one database snapshot taken after the account policy commit fence.
// Gateway membership and reports can change after this admission decision.
func checkMCPGatewayPromotionEvidence(expected int64, evidence sqlc.ReadMCPGatewayPromotionEvidenceRow) error {
	if evidence.Revision != expected {
		return ErrMCPAccountRevisionChanged
	}
	if evidence.Serving == 0 {
		return ErrMCPGatewayEvidenceUnavailable
	}
	if evidence.Stale > 0 {
		return ErrMCPGatewayEvidenceStale
	}
	if evidence.Pending > 0 {
		return ErrMCPGatewayReplayPending
	}
	return nil
}
