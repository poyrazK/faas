package state

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func lockToolGrantCredentialReady(ctx context.Context, q *toolpublicationdb.Queries, db toolpublicationdb.DBTX, g api.ToolGrantRecord, ready bool) error {
	if g.Credential == nil {
		return nil
	}
	if g.Credential.ConnectionID != "" {
		row, err := q.ShareToolOAuthConnection(ctx, db, toolpublicationdb.ShareToolOAuthConnectionParams{AccountID: mustPgUUID(g.AccountID), ID: mustPgUUID(g.Credential.ConnectionID)})
		if err != nil {
			return mapErr(err)
		}
		c, err := toolOAuthRow(row)
		if err != nil {
			return err
		}
		if err = checkToolOAuthGrant(g, c, ready); err != nil {
			return err
		}
		return lockToolOAuthAuthority(ctx, q, db, c.Record)
	}
	ciphertext, err := q.LockToolGrantCredential(ctx, db, toolpublicationdb.LockToolGrantCredentialParams{AccountID: mustPgUUID(g.AccountID), AppID: mustPgUUID(g.AppID), Scope: g.Credential.Scope, Key: strings.TrimPrefix(g.Intent.CredentialSecretRef, api.SecretRefPrefix)})
	if err != nil {
		return mapErr(err)
	}
	if toolpublication.Digest(ciphertext) != g.Credential.SourceSHA256 {
		return ErrNotFound
	}
	return nil
}
