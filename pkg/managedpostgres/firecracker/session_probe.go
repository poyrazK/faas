package firecracker

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SessionCredentialProber = (*Provider)(nil)

// ProbeSessionCredentials proves the session binding (ADR-20261110120000):
// it reaches the direct endpoint, keeps session state across statements and
// receives notifications, its password is recoverable by re-issue, and
// revocation ends its login.
func (p *Provider) ProbeSessionCredentials(ctx context.Context, id string) (evidence managedpostgres.SessionCredentialEvidence, err error) {
	key := probeKey("qualification-session-")
	channel := fixtureName("gregale_session_")
	request := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key, IdempotencyKey: key, Access: managedpostgres.CredentialSession}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
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
	conn, err := p.connect(ctx, material)
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
	if !p.loginRejected(ctx, material) {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.Revoked = true
	return evidence, nil
}
