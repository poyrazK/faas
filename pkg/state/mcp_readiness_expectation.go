package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type readinessExpectationKey struct{}
type readinessExpectationGuard struct {
	account, app string
	expected     api.MCPReadinessExpectation
}

func WithMCPReadinessExpectation(ctx context.Context, account, app string, expected api.MCPReadinessExpectation) context.Context {
	ids := map[string]string{}
	for k, v := range expected.EvidenceIDs {
		ids[k] = v
	}
	expected.EvidenceIDs = ids
	return context.WithValue(ctx, readinessExpectationKey{}, readinessExpectationGuard{account, app, expected})
}

func (m *MemStore) checkMCPReadinessExpectationLocked(ctx context.Context, d Deployment) error {
	g, ok := ctx.Value(readinessExpectationKey{}).(readinessExpectationGuard)
	if !ok {
		return nil
	}
	if d.AppID != g.app {
		return ErrMCPReadinessRequired
	}
	s, err := m.readinessSubjectLocked(g.account, d.ID)
	if err != nil {
		return err
	}
	if !toolpublication.ReadinessEvidenceMatches(s.Policy, s.ArtifactSHA256, m.publicationReadinessEvidenceLocked(d.ID), g.expected, time.Now()) {
		return ErrMCPReadinessRequired
	}
	return nil
}

func (s *PgStore) checkMCPReadinessExpectation(ctx context.Context, tx pgx.Tx, app, id string) error {
	g, ok := ctx.Value(readinessExpectationKey{}).(readinessExpectationGuard)
	if !ok {
		return nil
	}
	if app != g.app {
		return ErrMCPReadinessRequired
	}
	subject, err := readinessSubjectTx(ctx, tx, g.account, id)
	if err != nil {
		return err
	}
	evidence, err := publicationReadinessEvidenceTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if !toolpublication.ReadinessEvidenceMatches(subject.Policy, subject.ArtifactSHA256, evidence, g.expected, time.Now()) {
		return ErrMCPReadinessRequired
	}
	return nil
}
