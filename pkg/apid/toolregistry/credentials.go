package toolregistry

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolconnection"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func credentialSource(ctx context.Context, store state.Store, g api.ToolGrantRecord) (*state.AppSecret, error) {
	if g.Credential == nil || !toolgrant.ValidCredentialRef(g.Intent.CredentialSecretRef) {
		return nil, state.ErrNotFound
	}
	secret, err := store.GetAppSecretInScope(ctx, g.AccountID, g.AppID, g.Credential.Scope, strings.TrimPrefix(g.Intent.CredentialSecretRef, api.SecretRefPrefix))
	if err != nil || secret == nil || secret.AccountID != g.AccountID || secret.AppID != g.AppID || secret.Scope != g.Credential.Scope || secret.Key != strings.TrimPrefix(g.Intent.CredentialSecretRef, api.SecretRefPrefix) || secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" || secret.ManagedCredentialRef != "" {
		return nil, state.ErrNotFound
	}
	if g.Credential.SourceSHA256 != "" && toolpublication.Digest(secret.Ciphertext) != g.Credential.SourceSHA256 {
		return nil, state.ErrNotFound
	}
	return secret, nil
}

func bindGrantCredential(ctx context.Context, store state.Store, out *api.ToolGrantRecord, p api.ToolPublicationRecord) error {
	if out.Intent.CredentialSecretRef == "" && out.Intent.CredentialConnectionID == "" {
		return nil
	}
	if p.Intent.AccessMode != "external-oauth" || !out.Intent.AllowInvocation {
		return state.ErrInvalidArgument
	}
	out.Credential = &api.ToolGrantCredential{Scope: api.DefaultEnvScope, RequiredScopes: append([]string{}, p.Intent.ToolScopes[out.Intent.Name]...)}
	if out.Intent.CredentialConnectionID != "" {
		connections, ok := store.(state.ToolOAuthConnectionStore)
		if !ok {
			return state.ErrNotFound
		}
		c, err := connections.GetToolOAuthConnection(ctx, out.AccountID, out.Intent.CredentialConnectionID)
		if err != nil {
			return err
		}
		if c.Record.AppID != out.AppID || c.Record.ProjectID != out.ProjectID || c.Record.Intent.Scope != api.DefaultEnvScope || !toolconnection.Covers(c.Record.Intent.Scopes, out.Credential.RequiredScopes) || c.Record.Intent.Guided && c.Record.Generation == 0 {
			return state.ErrNotFound
		}
		if err = connectionAuthority(ctx, store, c); err != nil {
			return err
		}
		out.Credential.ConnectionID = c.Record.ID
		out.Credential.SourceSHA256 = c.Record.BindingSHA256
		out.Credential.SHA256 = toolgrant.CredentialDigest(*out)
		return nil
	}
	secret, err := credentialSource(ctx, store, *out)
	if err != nil {
		return err
	}
	out.Credential.SourceSHA256 = toolpublication.Digest(secret.Ciphertext)
	out.Credential.SHA256 = toolgrant.CredentialDigest(*out)
	return nil
}

func grantAccess(g api.ToolGrantRecord) api.ToolGrantAccess {
	out := api.ToolGrantAccess{GrantID: g.ID, ExpiresAt: g.Intent.ExpiresAt, AllowInvocation: g.Intent.AllowInvocation}
	if g.Credential != nil {
		out.CredentialBindingSHA256 = g.Credential.SHA256
	}
	return out
}
