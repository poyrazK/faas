package firecracker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/maskedbranch"
)

// CopyMaskedBranch uses two worker-only logins. The lifecycle keeps the
// fresh VM's customer credential paths closed until this method has committed
// the masked data and revoked both worker logins.
func (p *Provider) CopyMaskedBranch(ctx context.Context, req managedpostgres.MaskedBranchCopyRequest) (result error) {
	source, sourceErr := uuid.Parse(req.SourceResourceID)
	target, targetErr := uuid.Parse(req.TargetResourceID)
	if sourceErr != nil || targetErr != nil || source == target || source == uuid.Nil || target == uuid.Nil ||
		source.String() != req.SourceResourceID || target.String() != req.TargetResourceID {
		return managedpostgres.ErrInvalid
	}
	identity := "masked-branch-" + req.TargetResourceID
	reader := managedpostgres.CredentialRequest{ProviderResourceID: req.SourceResourceID, IdentityKey: identity, Access: managedpostgres.CredentialReadOnly}
	writer := managedpostgres.CredentialRequest{ProviderResourceID: req.TargetResourceID, IdentityKey: identity, Access: managedpostgres.CredentialMigration}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		writerErr := p.RevokeCredentials(cleanup, writer)
		readerErr := p.RevokeCredentials(cleanup, reader)
		if writerErr != nil || readerErr != nil {
			result = managedpostgres.ErrUnavailable
		}
	}()
	destination, err := p.openMaskedBranchConnection(ctx, writer)
	if err != nil {
		return err
	}
	defer closeMaskedBranchConnection(ctx, destination)
	original, err := p.openMaskedBranchConnection(ctx, reader)
	if err != nil {
		return err
	}
	defer closeMaskedBranchConnection(ctx, original)
	mac := hmac.New(sha256.New, p.cfg.CredentialKey)
	_, _ = mac.Write([]byte("gregale-masked-branch-v1\x00" + req.TargetResourceID))
	var key [32]byte
	copy(key[:], mac.Sum(nil))
	return maskedbranch.Copy(ctx, original, destination, req.TargetResourceID, key, req.Tables)
}

func (p *Provider) openMaskedBranchConnection(ctx context.Context, req managedpostgres.CredentialRequest) (*pgx.Conn, error) {
	material, err := p.IssueCredentials(ctx, req)
	if err != nil {
		return nil, managedpostgres.ErrUnavailable
	}
	conn, err := p.connect(ctx, material)
	if err != nil {
		return nil, managedpostgres.ErrUnavailable
	}
	return conn, nil
}

func closeMaskedBranchConnection(ctx context.Context, conn *pgx.Conn) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_ = conn.Close(cleanup)
}
