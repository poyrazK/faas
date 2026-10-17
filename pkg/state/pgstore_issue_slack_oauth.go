package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func slackOAuthChanged() error {
	return api.NewProblem(409, "issue_slack_installation_changed", "Slack installation changed", "reload Slack settings and restart authorization")
}
func slackInstallationRow(r sqlc.IssueSlackInstallation) IssueSlackInstallation {
	return IssueSlackInstallation{AppID: issueID(r.AppID), AccountID: issueID(r.AccountID), Revision: r.Revision, Status: r.Status, WorkspaceID: r.WorkspaceID, WorkspaceName: r.WorkspaceName, SlackAppID: r.SlackAppID, BotUserID: r.BotUserID, TokenSealed: r.TokenSealed, InstalledAt: r.InstalledAt.Time}
}
func slackOAuthFlowRow(r sqlc.IssueSlackOauthFlow) IssueSlackOAuthFlow {
	return IssueSlackOAuthFlow{AppID: issueID(r.AppID), AccountID: issueID(r.AccountID), Hash: r.StateHash, Phase: r.Phase, ExpectedRevision: r.ExpectedRevision, Sealed: r.Sealed, ExpiresAt: r.ExpiresAt.Time}
}
func readSlackInstallation(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, owner string) (IssueSlackInstallation, error) {
	r, err := q.IssueSlackInstallation(ctx, db, sqlc.IssueSlackInstallationParams{AppID: issueUUID(app), AccountID: issueUUID(owner)})
	if errors.Is(err, pgx.ErrNoRows) {
		return IssueSlackInstallation{}, nil
	}
	return slackInstallationRow(r), err
}
func (s *PgStore) IssueSlackInstallationCredential(ctx context.Context, app, owner string) (IssueSlackInstallation, error) {
	if _, err := issueApplicationSlackPolicy(ctx, sqlc.New(), s.pool, app, owner); err != nil {
		return IssueSlackInstallation{}, err
	}
	return readSlackInstallation(ctx, sqlc.New(), s.pool, app, owner)
}
func (s *PgStore) GetIssueSlackInstallation(ctx context.Context, app, owner string) (IssueSlackInstallation, error) {
	r, err := s.IssueSlackInstallationCredential(ctx, app, owner)
	r.TokenSealed = nil
	return r, err
}
func (s *PgStore) slackOAuthTransaction(ctx context.Context, app, owner string, fn func(*sqlc.Queries, pgx.Tx, sqlc.IssueLockAppRow) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return mapErr(err)
	}
	if issueID(a.AccountID) != owner {
		return ErrNotFound
	}
	if _, err = issueApplicationSlackPolicy(ctx, q, tx, app, owner); err != nil {
		return err
	}
	if err = fn(q, tx, a); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) StartIssueSlackOAuth(ctx context.Context, f IssueSlackOAuthFlow) error {
	if !slackLinkToken.MatchString(f.Hash) || f.ExpectedRevision < 0 || len(f.Sealed) == 0 || len(f.Sealed) > api.IssueSlackOAuthSealedMaxBytes || !f.ExpiresAt.After(time.Now()) || f.ExpiresAt.After(time.Now().Add(api.IssueSlackOAuthFlowTTL)) {
		return ErrInvalidArgument
	}
	return s.slackOAuthTransaction(ctx, f.AppID, f.AccountID, func(q *sqlc.Queries, tx pgx.Tx, a sqlc.IssueLockAppRow) error {
		if err := slackOAuthOwnerReady(ctx, q, tx, f.AppID, f.AccountID); err != nil {
			return err
		}
		old, err := readSlackInstallation(ctx, q, tx, f.AppID, f.AccountID)
		if err != nil {
			return err
		}
		if old.Revision != f.ExpectedRevision {
			return slackOAuthChanged()
		}
		return q.IssueSlackOAuthFlowPut(ctx, tx, sqlc.IssueSlackOAuthFlowPutParams{AppID: a.ID, AccountID: a.AccountID, StateHash: f.Hash, ExpectedRevision: f.ExpectedRevision, Sealed: f.Sealed, ExpiresAt: issueTime(f.ExpiresAt)})
	})
}
func (s *PgStore) ClaimIssueSlackOAuth(ctx context.Context, hash, owner string) (IssueSlackOAuthFlow, error) {
	r, err := sqlc.New().IssueSlackOAuthFlowClaim(ctx, s.pool, sqlc.IssueSlackOAuthFlowClaimParams{StateHash: hash, AccountID: issueUUID(owner)})
	return slackOAuthFlowRow(r), mapErr(err)
}
func (s *PgStore) SaveIssueSlackOAuth(ctx context.Context, f IssueSlackOAuthFlow, sealed []byte) error {
	if len(sealed) == 0 || len(sealed) > api.IssueSlackOAuthSealedMaxBytes {
		return ErrInvalidArgument
	}
	_, err := sqlc.New().IssueSlackOAuthFlowSave(ctx, s.pool, sqlc.IssueSlackOAuthFlowSaveParams{StateHash: f.Hash, AccountID: issueUUID(f.AccountID), Sealed: sealed})
	return mapErr(err)
}
func (s *PgStore) GetIssueSlackOAuth(ctx context.Context, hash, owner string) (IssueSlackOAuthFlow, error) {
	r, err := sqlc.New().IssueSlackOAuthFlow(ctx, s.pool, sqlc.IssueSlackOAuthFlowParams{StateHash: hash, AccountID: issueUUID(owner)})
	return slackOAuthFlowRow(r), mapErr(err)
}
func invalidateSlackOAuth(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app string, all bool) error {
	if err := q.IssueSlackOAuthDestinationsDisable(ctx, tx, sqlc.IssueSlackOAuthDestinationsDisableParams{AppID: issueUUID(app), AllDestinations: all}); err != nil {
		return err
	}
	if err := q.IssueSlackOAuthDeliveriesCancel(ctx, tx, issueUUID(app)); err != nil {
		return err
	}
	if err := q.IssueSlackOAuthInteractionsDisable(ctx, tx, issueUUID(app)); err != nil {
		return err
	}
	if err := q.IssueSlackIdentitiesRevoke(ctx, tx, issueUUID(app)); err != nil {
		return err
	}
	return q.IssueSlackOAuthFlowDelete(ctx, tx, issueUUID(app))
}
func (s *PgStore) FinishIssueSlackOAuth(ctx context.Context, f IssueSlackOAuthFlow, in IssueSlackInstallation, signing []byte) error {
	if !slackWorkspace.MatchString(in.WorkspaceID) || !slackApp.MatchString(in.SlackAppID) || !ValidIssueSlackUser(in.BotUserID) || len(in.WorkspaceName) == 0 || len(in.WorkspaceName) > api.IssueSlackOAuthNameMaxBytes || len(in.TokenSealed) == 0 || len(in.TokenSealed) > api.IssueSlackTokenMaxBytes*2 || len(signing) == 0 || len(signing) > api.IssueSlackTokenMaxBytes*2 {
		return ErrInvalidArgument
	}
	return s.slackOAuthTransaction(ctx, f.AppID, f.AccountID, func(q *sqlc.Queries, tx pgx.Tx, a sqlc.IssueLockAppRow) error {
		current, err := q.IssueSlackOAuthFlow(ctx, tx, sqlc.IssueSlackOAuthFlowParams{StateHash: f.Hash, AccountID: a.AccountID})
		if err != nil {
			return mapErr(err)
		}
		if issueID(current.AppID) != f.AppID || current.Phase != "choosing" || current.ExpectedRevision != f.ExpectedRevision {
			return slackOAuthChanged()
		}
		if err := slackOAuthOwnerReady(ctx, q, tx, f.AppID, f.AccountID); err != nil {
			return err
		}
		old, err := readSlackInstallation(ctx, q, tx, f.AppID, f.AccountID)
		if err != nil {
			return err
		}
		if old.Revision != f.ExpectedRevision {
			return slackOAuthChanged()
		}
		if err = invalidateSlackOAuth(ctx, q, tx, f.AppID, true); err != nil {
			return err
		}
		_, err = q.IssueSlackInstallationPut(ctx, tx, sqlc.IssueSlackInstallationPutParams{AppID: a.ID, AccountID: a.AccountID, Revision: old.Revision + 1, WorkspaceID: in.WorkspaceID, WorkspaceName: in.WorkspaceName, SlackAppID: in.SlackAppID, BotUserID: in.BotUserID, TokenSealed: in.TokenSealed})
		if err != nil {
			return err
		}
		policy, err := readSlackInteractionPolicy(ctx, q, tx, f.AppID)
		if err != nil {
			return err
		}
		return q.IssueSlackInteractionPolicyPut(ctx, tx, sqlc.IssueSlackInteractionPolicyPutParams{AppID: a.ID, Enabled: true, Revision: policy.Revision + 1, WorkspaceID: in.WorkspaceID, SlackAppID: in.SlackAppID, SecretSealed: signing})
	})
}
func (s *PgStore) DisconnectIssueSlackOAuth(ctx context.Context, app, owner string, revision int64) error {
	return s.slackOAuthTransaction(ctx, app, owner, func(q *sqlc.Queries, tx pgx.Tx, a sqlc.IssueLockAppRow) error {
		old, err := readSlackInstallation(ctx, q, tx, app, owner)
		if err != nil {
			return err
		}
		if old.Revision == 0 || old.Revision != revision {
			return slackOAuthChanged()
		}
		if err = invalidateSlackOAuth(ctx, q, tx, app, false); err != nil {
			return err
		}
		return q.IssueSlackInstallationDisable(ctx, tx, sqlc.IssueSlackInstallationDisableParams{AppID: a.ID, Status: "disconnected"})
	})
}
func (s *PgStore) SetIssueSlackOAuthDestination(ctx context.Context, app, owner string, in PutIssueSlackDestination, revision int64) error {
	if !ValidIssueSlackChannel(in.ChannelID) || in.Revision < 0 {
		return ErrInvalidArgument
	}
	return s.slackOAuthTransaction(ctx, app, owner, func(q *sqlc.Queries, tx pgx.Tx, a sqlc.IssueLockAppRow) error {
		if err := slackOAuthOwnerReady(ctx, q, tx, app, owner); err != nil {
			return err
		}
		install, err := readSlackInstallation(ctx, q, tx, app, owner)
		if err != nil {
			return err
		}
		if install.Status != "connected" || install.Revision != revision {
			return slackOAuthChanged()
		}
		in.TokenSealed = install.TokenSealed
		d, err := putIssueSlackDestination(ctx, q, tx, a, in)
		if err != nil {
			return err
		}
		return q.IssueSlackOAuthDestinationBind(ctx, tx, sqlc.IssueSlackOAuthDestinationBindParams{ID: issueUUID(d.ID), OauthRevision: pgtype.Int8{Int64: revision, Valid: true}})
	})
}
func (s *PgStore) RevokeIssueSlackOAuth(ctx context.Context, slackApp, workspace, bot string, when time.Time) error {
	rows, err := sqlc.New().IssueSlackOAuthAffectedApps(ctx, s.pool, sqlc.IssueSlackOAuthAffectedAppsParams{SlackAppID: slackApp, WorkspaceID: workspace, BotUserID: bot, EventTime: issueTime(when)})
	if err != nil {
		return err
	}
	for _, r := range rows {
		err = s.slackOAuthTransaction(ctx, issueID(r.AppID), issueID(r.AccountID), func(q *sqlc.Queries, tx pgx.Tx, a sqlc.IssueLockAppRow) error {
			old, err := readSlackInstallation(ctx, q, tx, issueID(r.AppID), issueID(r.AccountID))
			if err != nil {
				return err
			}
			if old.Status != "connected" || old.SlackAppID != slackApp || old.WorkspaceID != workspace || old.InstalledAt.After(when) || (bot != "" && old.BotUserID != bot) {
				return nil
			}
			if err = invalidateSlackOAuth(ctx, q, tx, issueID(r.AppID), false); err != nil {
				return err
			}
			return q.IssueSlackInstallationDisable(ctx, tx, sqlc.IssueSlackInstallationDisableParams{AppID: a.ID, Status: "revoked"})
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *PgStore) IssueSlackOAuthActionApp(ctx context.Context, delivery, slackApp, workspace string) (string, error) {
	id, err := sqlc.New().IssueSlackOAuthActionApp(ctx, s.pool, sqlc.IssueSlackOAuthActionAppParams{ID: issueUUID(delivery), SlackAppID: slackApp, WorkspaceID: workspace})
	return issueID(id), mapErr(err)
}

func slackOAuthOwnerReady(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, owner string) error {
	ready, err := q.IssueSlackOAuthOwnerReady(ctx, db, sqlc.IssueSlackOAuthOwnerReadyParams{ID: issueUUID(app), AccountID: issueUUID(owner), EntitledPlans: issueSlackEntitledPlans()})
	if err != nil {
		return err
	}
	if !ready {
		return ErrNotFound
	}
	return nil
}
