package neon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SessionCredentialProber = (*Provider)(nil)

// ProbeSessionCredentials proves the session binding (ADR-20261110120000):
// it reaches the direct endpoint, keeps session state across statements and
// receives notifications, its password is recoverable by re-issue, and
// revocation ends its login.
func (p *Provider) ProbeSessionCredentials(ctx context.Context, id string) (evidence managedpostgres.SessionCredentialEvidence, err error) {
	key := "qualification-session-" + uuid.NewString()
	channel := "gregale_session_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	request := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key, IdempotencyKey: key, Access: managedpostgres.CredentialSession}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if revokeErr := p.RevokeCredentials(cleanup, request); revokeErr != nil && err == nil {
			err = managedpostgres.ErrUnavailable
		}
	}()
	material, err := p.IssueCredentials(ctx, request)
	if err != nil {
		return evidence, err
	}
	evidence.DirectEndpoint = managedpostgres.SessionMaterialHasDirect(material)
	retried, err := p.IssueCredentials(ctx, request)
	if err != nil || retried.Username != material.Username || retried.Password != material.Password || retried.Database != material.Database {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.PasswordRecovered = true
	conn, err := connectProbe(ctx, material)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	evidence.SessionStateHeld, evidence.NotificationDelivered, err = managedpostgres.VerifySessionCredentialProbe(ctx, conn, channel)
	if err != nil {
		return evidence, err
	}
	if err := p.RevokeCredentials(ctx, request); err != nil {
		return evidence, err
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); err == nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	if !sessionLoginRejected(ctx, material) {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.Revoked = true
	return evidence, nil
}

// sessionLoginRejected reports whether the server refuses the revoked login
// itself, not merely whether a connection failed.
func sessionLoginRejected(ctx context.Context, material managedpostgres.CredentialMaterial) bool {
	dsn, err := probeDSN(material)
	if err != nil {
		return false
	}
	conn, err := pgx.Connect(ctx, dsn)
	if conn != nil {
		_ = conn.Close(ctx)
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "28000" || pgErr.Code == "28P01")
}
