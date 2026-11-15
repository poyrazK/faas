package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func publicationAuthorityAction(action string, in ToolPublicationVerificationChange) bool {
	switch action {
	case "review", "approve", "begin", "dispatch", "renew", "renew_dispatch", "readiness_dispatch", "readiness_record", "readiness_assert":
		return true
	case "finish":
		return in.Outcome != nil && in.Outcome.OK
	}
	return false
}

func publicationReadinessCurrent(p storedToolPublication, v *api.ToolPublicationVerification, s MCPReadinessSubject) error {
	if !toolpublication.ReadinessMatches(p.Intent.Readiness, s.Policy) {
		return ErrConflict
	}
	if p.Intent.Readiness != nil && (v == nil || !toolpublication.ValidID(v.ReadinessArtifactSHA256) || s.ArtifactSHA256 != v.ReadinessArtifactSHA256 || api.MCPConnectionScope(s.Scope) != api.DefaultEnvScope) {
		return ErrConflict
	}
	return nil
}

func publicationReadinessExpectation(p storedToolPublication) api.MCPReadinessExpectation {
	e := toolpublication.ReadinessExpectation(*p.Verification)
	if p.Intent.Readiness != nil {
		e.PolicyRevision = p.Intent.Readiness.PolicyRevision
	}
	return e
}

func (m *MemStore) publicationReadinessLocked(p storedToolPublication, v *api.ToolPublicationVerification) (*MCPReadinessSubject, error) {
	if p.Intent.Readiness == nil {
		if m.readinessPolicyLocked(v.AppID, api.DefaultEnvScope).Enabled {
			return nil, ErrConflict
		}
		return nil, nil
	}
	s, err := m.readinessSubjectLocked(p.AccountID, v.DeploymentID)
	if err != nil {
		return nil, err
	}
	if err = publicationReadinessCurrent(p, v, s); err != nil {
		return nil, err
	}
	return &s, nil
}

func publicationReadinessTx(ctx context.Context, tx pgx.Tx, p storedToolPublication, v *api.ToolPublicationVerification) (*MCPReadinessSubject, error) {
	if p.Intent.Readiness == nil {
		policy, err := readReadinessPolicy(ctx, tx, v.AppID, true, api.DefaultEnvScope)
		if err != nil {
			return nil, err
		}
		if policy.Enabled {
			return nil, ErrConflict
		}
		return nil, nil
	}
	s, err := readinessSubjectTx(ctx, tx, p.AccountID, v.DeploymentID)
	if err != nil {
		return nil, err
	}
	if err = publicationReadinessCurrent(p, v, s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (m *MemStore) publicationReadinessEvidenceLocked(id string) []api.MCPReadinessEvidence {
	out := []api.MCPReadinessEvidence{}
	for _, e := range m.readinessLocked().evidence {
		if e.DeploymentID == id {
			out = append(out, e)
		}
	}
	return out
}

func publicationReadinessEvidenceTx(ctx context.Context, tx pgx.Tx, id string) ([]api.MCPReadinessEvidence, error) {
	rows, err := toolpublicationdb.New().LockPublicationReadinessEvidence(ctx, tx, mustPgUUID(id))
	if err != nil {
		return nil, mapErr(err)
	}
	out := []api.MCPReadinessEvidence{}
	for _, raw := range rows {
		var e api.MCPReadinessEvidence
		if err = json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func publicationReadinessPassed(p storedToolPublication, s *MCPReadinessSubject, evidence []api.MCPReadinessEvidence, outcome *api.ToolPublicationVerificationOutcome) bool {
	if p.Intent.Readiness == nil {
		return true
	}
	if p.Verification == nil || outcome == nil {
		return false
	}
	v := *p.Verification
	v.Outcome = outcome
	p.Verification = &v
	return s != nil && p.Verification != nil && p.Verification.Outcome != nil && toolpublication.ReadinessResultsMatch(p.Intent.Readiness, p.Verification.Outcome.Readiness) && toolpublication.ReadinessEvidenceMatches(s.Policy, s.ArtifactSHA256, evidence, publicationReadinessExpectation(p), time.Now())
}
