package outbound

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

type PostgresWorkflowAuthorizer struct{ pool *pgxpool.Pool }

func NewPostgresWorkflowAuthorizer(pool *pgxpool.Pool) (*PostgresWorkflowAuthorizer, error) {
	if pool == nil {
		return nil, errors.New("workflow outbound database is required")
	}
	return &PostgresWorkflowAuthorizer{pool: pool}, nil
}
func (a *PostgresWorkflowAuthorizer) AuthorizeWorkflow(ctx context.Context, raw, integrationID, method, path string, body []byte) (WorkflowIdentity, error) {
	q := sqlc.New()
	key, err := q.WorkflowOutboundSigningKey(ctx, a.pool)
	if err != nil {
		return WorkflowIdentity{}, err
	}
	block, _ := pem.Decode([]byte(key.PublicKeyPem))
	if block == nil {
		return WorkflowIdentity{}, errors.New("workflow outbound signing key is invalid")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return WorkflowIdentity{}, errors.New("workflow outbound signing key is invalid")
	}
	public, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return WorkflowIdentity{}, errors.New("workflow outbound signing key is invalid")
	}
	identity, err := verifyWorkflowIdentity(raw, integrationID, method, path, body, key.KeyID, public, time.Now())
	if err != nil {
		return WorkflowIdentity{}, err
	}
	allowed, err := q.AuthorizeWorkflowOutbound(ctx, a.pool, sqlc.AuthorizeWorkflowOutboundParams{RunID: workflowUUID(identity.RunID), AppID: workflowUUID(identity.AppID), AccountID: workflowUUID(identity.AccountID), StepName: identity.StepName, Attempt: int32(identity.Attempt), AttemptToken: workflowUUID(identity.AttemptToken), IntegrationID: workflowUUID(integrationID), Method: method, Path: path})
	if err != nil {
		return WorkflowIdentity{}, err
	}
	if !allowed {
		return WorkflowIdentity{}, ErrWorkflowNotAuthorized
	}
	return identity, nil
}

func workflowUUID(raw string) pgtype.UUID {
	id, err := uuid.Parse(raw)
	return pgtype.UUID{Bytes: id, Valid: err == nil}
}
