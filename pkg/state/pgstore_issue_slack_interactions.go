package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ApplyIssueSlackInteraction(ctx context.Context, app string, in IssueSlackInteractionRequest) (IssueSlackInteractionResult, error) {
	out := IssueSlackInteractionResult{}
	if !ValidIssueSlackUser(in.UserID) || !ValidIssueSlackChannel(in.ChannelID) || !ValidIssueSlackTimestamp(in.MessageTS) || !slackLinkToken.MatchString(in.RequestKey) {
		return out, ErrInvalidArgument
	}
	switch in.Action {
	case "acknowledge", "snooze_1h", "snooze_24h":
	default:
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	identityArgs := sqlc.IssueSlackIdentityGetParams{AppID: issueUUID(app), SlackUserID: in.UserID}
	initialIdentity, identityErr := q.IssueSlackIdentityGet(ctx, tx, identityArgs)
	hadIdentity := identityErr == nil
	if identityErr != nil && !errors.Is(identityErr, pgx.ErrNoRows) {
		return out, identityErr
	}
	if hadIdentity {
		appPolicy, err := q.IssuePolicyApp(ctx, tx, issueUUID(app))
		if err != nil {
			return out, mapErr(err)
		}
		if _, err = lockIssueResponseActor(ctx, tx, app, issueID(appPolicy.AccountID), issueID(initialIdentity.AccountID)); err != nil {
			return out, err
		}
	} else if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return out, mapErr(err)
	}
	c, err := q.IssueSlackActionContext(ctx, tx, sqlc.IssueSlackActionContextParams{AppID: issueUUID(app), DeliveryID: issueUUID(in.DeliveryID), ChannelID: in.ChannelID, MessageTs: in.MessageTS, PolicyRevision: in.PolicyRevision, EntitledPlans: issueSlackEntitledPlans()})
	if err != nil {
		return out, mapErr(err)
	}
	out.AppSlug = c.Slug
	out.IssueID = issueID(c.IssueID)
	if c.ConsumedAt.Valid {
		out.Status = "already_handled"
		return out, tx.Commit(ctx)
	}
	if c.ResponseID != c.CurrentResponseID || c.ResponseRevision != c.CurrentResponseRevision {
		return out, applicationTriageConflict()
	}
	// Remember unsigned-browser linking separately from response mutation. A retry of
	// the original click cannot execute an action after the user confirms linking.
	ch, err := q.IssueSlackChallengeGet(ctx, tx, sqlc.IssueSlackChallengeGetParams{AppID: c.AppID, RequestKey: in.RequestKey})
	if err == nil {
		if ch.ConsumedAt.Valid {
			out.Status = "linked"
			return out, tx.Commit(ctx)
		}
		// Only the token's hash is stored, so a retried click gets a new
		// link and the one it replaces stops working.
		out.LinkToken, err = rotateSlackLinkChallenge(ctx, q, tx, c.AppID, in.RequestKey)
		if err != nil {
			return out, err
		}
		out.Status = "link_required"
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	identity, err := q.IssueSlackIdentityGet(ctx, tx, identityArgs)
	if errors.Is(err, pgx.ErrNoRows) {
		if hadIdentity {
			return out, ErrNotFound
		}
		out.LinkToken, err = createSlackLinkChallenge(ctx, q, tx, c.AppID, in)
		if err != nil {
			return out, err
		}
		out.Status = "link_required"
		return out, tx.Commit(ctx)
	}
	if err != nil {
		return out, err
	}
	if !hadIdentity || identity.AccountID != initialIdentity.AccountID {
		return out, ErrNotFound
	}
	actor := issueID(identity.AccountID)
	scope, err := applicationTriageScope(ctx, q, tx, app, issueID(c.AccountID), actor, issueID(c.IssueID))
	if err != nil {
		return out, mapErr(err)
	}
	req := IssueApplicationTriageAction{Action: "acknowledge"}
	if in.Action != "acknowledge" {
		duration := api.IssueSlackSnoozeShort
		if in.Action == "snooze_24h" {
			duration = api.IssueSlackSnoozeLong
		}
		until := scope.CheckedAt.Time.Add(duration)
		req.Action = "snooze"
		req.SnoozedUntil = &until
	}
	if err = applyApplicationTriageAction(ctx, q, tx, app, issueID(c.AccountID), actor, issueID(c.IssueID), c.ResponseRevision, req); err != nil {
		return out, err
	}
	if err = q.IssueSlackActionConsume(ctx, tx, issueUUID(in.DeliveryID)); err != nil {
		return out, err
	}
	out.Status = "handled"
	return out, tx.Commit(ctx)
}
func createSlackLinkChallenge(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, appID pgtype.UUID, in IssueSlackInteractionRequest) (string, error) {
	if err := q.IssueSlackChallengesExpire(ctx, tx, appID); err != nil {
		return "", err
	}
	n, err := q.IssueSlackChallengeCount(ctx, tx, appID)
	if err != nil {
		return "", err
	}
	if n >= api.IssueSlackLinkChallengesMax {
		return "", api.ErrCapacity("Slack account linking is busy; try again shortly")
	}
	token, err := newSlackLinkToken()
	if err != nil {
		return "", err
	}
	// Use database time for the expiry as well as response mutation.
	now, err := q.IssueSlackInteractionNow(ctx, tx)
	if err != nil {
		return "", err
	}
	err = q.IssueSlackChallengePut(ctx, tx, sqlc.IssueSlackChallengePutParams{Token: slackLinkTokenHash(token), AppID: appID, DeliveryID: issueUUID(in.DeliveryID), RequestKey: in.RequestKey, SlackUserID: in.UserID, PolicyRevision: in.PolicyRevision, ExpiresAt: issueTime(now.Time.Add(api.IssueSlackLinkTTL))})
	return token, err
}

// rotateSlackLinkChallenge gives the unconsumed challenge for requestKey a
// new token, keeping its expiry, and returns the token.
func rotateSlackLinkChallenge(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, appID pgtype.UUID, requestKey string) (string, error) {
	token, err := newSlackLinkToken()
	if err != nil {
		return "", err
	}
	err = q.IssueSlackChallengeRotate(ctx, tx, sqlc.IssueSlackChallengeRotateParams{AppID: appID, RequestKey: requestKey, Token: slackLinkTokenHash(token)})
	return token, err
}

func newSlackLinkToken() (string, error) {
	raw := make([]byte, api.IssueSlackLinkNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
