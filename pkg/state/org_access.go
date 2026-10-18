package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db"
)

// ErrOrgAccessRevoked means a previously authorized organization request no
// longer has the membership or credential that authorized it.
var ErrOrgAccessRevoked = errors.New("organization access revoked")

// OrgAccessSnapshot is read atomically; a role and its revision must never
// come from different membership versions.
type OrgAccessSnapshot struct {
	Role     OrgRole
	Revision int64
}

type OrgAccessStore interface {
	OrgAccessSnapshot(context.Context, string, string) (OrgAccessSnapshot, error)
	CheckOrgAccess(context.Context) error
}

// OrgAccessFence pins a request to a membership version. CredentialOrgID
// preserves the credential's actual binding; AppID is present for deploy tokens.
type OrgAccessFence struct {
	OrgID, AccountID string
	Revision         int64
	CredentialID     string
	CredentialOrgID  string
	AppID            string
}

type orgAccessContextKey struct{}
type orgAccessControl struct {
	fence  OrgAccessFence
	cancel context.CancelCauseFunc
}

// WithOrgAccessFence carries the original authorization into Store writes.
// The derived context is also cancelled when a write detects revocation.
func WithOrgAccessFence(ctx context.Context, fence OrgAccessFence) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	ctx = context.WithValue(ctx, orgAccessContextKey{}, orgAccessControl{fence: fence, cancel: cancel})
	ctx = db.WithIntentFence(ctx, func(ctx context.Context, tx pgx.Tx) error { return lockOrgAccess(ctx, tx, fence) })
	return ctx, cancel
}

// WithoutOrgAccessFence permits only post-commit audit delivery and response
// hydration. A successful self-transfer/revoke has invalidated its own grant.
func WithoutOrgAccessFence(ctx context.Context) context.Context {
	return db.WithoutIntentFence(context.WithValue(ctx, orgAccessContextKey{}, struct{}{}))
}

func orgAccessFrom(ctx context.Context) (OrgAccessFence, bool) {
	if ctx == nil {
		return OrgAccessFence{}, false
	}
	control, ok := ctx.Value(orgAccessContextKey{}).(orgAccessControl)
	return control.fence, ok
}

func rejectOrgAccess(ctx context.Context) error {
	if control, ok := ctx.Value(orgAccessContextKey{}).(orgAccessControl); ok {
		control.cancel(ErrOrgAccessRevoked)
	}
	return ErrOrgAccessRevoked
}
