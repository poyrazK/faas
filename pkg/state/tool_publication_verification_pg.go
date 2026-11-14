package state

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

func (s *PgStore) ChangeToolPublicationVerification(ctx context.Context, account, id, key, action string, in ToolPublicationVerificationChange) (ToolPublicationVerificationResult, error) {
	var out ToolPublicationVerificationResult
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	data, err := q.LockToolPublication(ctx, tx, toolpublicationdb.LockToolPublicationParams{AccountID: mustPgUUID(account), ID: id})
	if err != nil {
		return out, mapErr(err)
	}
	p, err := decodeStoredToolPublication(data)
	if err != nil {
		return out, err
	}
	v := p.Verification
	if v == nil {
		v = in.Review
	}
	if action == "approve" || action == "begin" || action == "dispatch" || action == "review" && p.Verification == nil {
		if !validVerificationTarget(v) {
			return out, ErrInvalidArgument
		}
		_, err = q.LockToolPublicationCandidate(ctx, tx, toolpublicationdb.LockToolPublicationCandidateParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(v.AppID), DeploymentID: mustPgUUID(v.DeploymentID), KeyID: mustPgUUID(v.KeyID), SourceSha256: pgtype.Text{String: v.DeploymentSourceSHA256, Valid: true}, AppSlug: p.Intent.App, ExpectedServing: p.Intent.ExpectedServingDeploymentID})
		if err != nil {
			return out, mapErr(err)
		}
	}
	ref := ""
	if p.TestCredential != nil {
		ref = p.TestCredential.Ref
	}
	if action == "approve" && in.Decision != nil {
		ref = in.Decision.CredentialSecretRef
	}
	var secret *AppSecret
	if action == "approve" || action == "dispatch" {
		if v == nil || !toolgrant.ValidCredentialRef(ref) {
			return out, ErrInvalidArgument
		}
		ciphertext, err := q.LockToolGrantCredential(ctx, tx, toolpublicationdb.LockToolGrantCredentialParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(v.AppID), Scope: api.DefaultEnvScope, Key: strings.TrimPrefix(ref, api.SecretRefPrefix)})
		if err != nil {
			return out, mapErr(err)
		}
		secret = &AppSecret{AccountID: account, AppID: v.AppID, Scope: api.DefaultEnvScope, Key: strings.TrimPrefix(ref, api.SecretRefPrefix), Ciphertext: ciphertext}
	}
	out, err = changeToolVerification(&p, key, action, in, secret)
	if err != nil {
		return out, err
	}
	data, err = json.Marshal(p)
	if err != nil {
		return out, err
	}
	err = q.FinishToolPublication(ctx, tx, toolpublicationdb.FinishToolPublicationParams{AccountID: mustPgUUID(account), ID: id, Status: p.Status, Payload: data})
	if err != nil {
		return out, mapErr(err)
	}
	return out, tx.Commit(ctx)
}
