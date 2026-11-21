package state

import (
	"context"
	"encoding/json"
	"strings"
	"time"

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
	if publicationAuthorityAction(action, in) {
		if !validVerificationTarget(v) {
			return out, ErrInvalidArgument
		}
		_, err = q.LockToolPublicationCandidate(ctx, tx, toolpublicationdb.LockToolPublicationCandidateParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(v.AppID), DeploymentID: mustPgUUID(v.DeploymentID), KeyID: mustPgUUID(v.KeyID), SourceSha256: pgtype.Text{String: v.DeploymentSourceSHA256, Valid: true}, AppSlug: p.Intent.App, ExpectedServing: p.Intent.ExpectedServingDeploymentID})
		if err != nil {
			return out, mapErr(err)
		}
	}
	var readiness *MCPReadinessSubject
	if publicationAuthorityAction(action, in) {
		readiness, err = publicationReadinessTx(ctx, tx, p, v)
		if err != nil {
			return out, err
		}
	}
	ref := ""
	connectionID := ""
	if p.TestCredential != nil {
		ref = p.TestCredential.Ref
		connectionID = p.TestCredential.ConnectionID
	}
	if action == "approve" && in.Decision != nil {
		ref = in.Decision.CredentialSecretRef
		connectionID = in.Decision.CredentialConnectionID
	}
	var secret *AppSecret
	var connection *ToolOAuthConnection
	if action == "approve" || action == "dispatch" || action == "renew" || action == "renew_dispatch" {
		if connectionID != "" {
			if !toolgrant.ValidID(connectionID) {
				return out, ErrInvalidArgument
			}
			params := toolpublicationdb.ShareToolOAuthConnectionParams{AccountID: mustPgUUID(account), ID: mustPgUUID(connectionID)}
			var row toolpublicationdb.ToolOauthConnection
			if action == "renew" {
				row, err = q.LockToolOAuthConnection(ctx, tx, toolpublicationdb.LockToolOAuthConnectionParams(params))
			} else {
				row, err = q.ShareToolOAuthConnection(ctx, tx, params)
			}
			if err != nil {
				return out, mapErr(err)
			}
			c, err := toolOAuthRow(row)
			if err != nil {
				return out, err
			}
			if err = lockToolOAuthAuthority(ctx, q, tx, c.Record); err != nil {
				return out, err
			}
			connection = &c
		} else {
			if v == nil || !toolgrant.ValidCredentialRef(ref) {
				return out, ErrInvalidArgument
			}
			ciphertext, err := q.LockToolGrantCredential(ctx, tx, toolpublicationdb.LockToolGrantCredentialParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(v.AppID), Scope: api.DefaultEnvScope, Key: strings.TrimPrefix(ref, api.SecretRefPrefix)})
			if err != nil {
				return out, mapErr(err)
			}
			secret = &AppSecret{AccountID: account, AppID: v.AppID, Scope: api.DefaultEnvScope, Key: strings.TrimPrefix(ref, api.SecretRefPrefix), Ciphertext: ciphertext}
		}
	}
	out, err = changeToolVerification(&p, key, action, in, secret, connection)
	if err != nil {
		return out, err
	}
	out.ReadinessSubject = readiness
	if action == "readiness_record" {
		if readiness == nil || in.ReadinessSubject == nil || in.ReadinessEvidence == nil || in.ReadinessSubject.AccountID != account || in.ReadinessSubject.DeploymentID != v.DeploymentID || in.ReadinessEvidence.Role != in.Role || !validateReadinessEvidence(*in.ReadinessSubject, *in.ReadinessEvidence) {
			return out, ErrInvalidArgument
		}
		saved, err := recordMCPReadinessTx(ctx, tx, *in.ReadinessSubject, *in.ReadinessEvidence)
		if err != nil {
			return out, err
		}
		out.ReadinessEvidence = &saved
		return out, tx.Commit(ctx)
	}
	if action == "readiness_assert" || action == "finish" && in.Outcome != nil && in.Outcome.OK {
		if p.Intent.Readiness != nil {
			evidence, e := publicationReadinessEvidenceTx(ctx, tx, v.DeploymentID)
			if e != nil {
				return out, e
			}
			if !publicationReadinessPassed(p, readiness, evidence, in.Outcome) {
				return out, ErrConflict
			}
		}
	}
	if action == "renew" && connection != nil {
		claim, claimErr := claimToolOAuth(*connection, time.Now().UTC())
		if claim.Connection.Record.ID != "" {
			if err = updateToolOAuth(ctx, q, tx, claim.Connection); err != nil {
				return out, err
			}
			if err = tx.Commit(ctx); err != nil {
				return out, err
			}
		}
		out.Connection, out.RenewalFresh = &claim.Connection, claim.Fresh
		return out, claimErr
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
