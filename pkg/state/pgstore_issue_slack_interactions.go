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
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
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
		out.Status = "link_required"
		out.LinkToken = ch.Token
		if ch.ConsumedAt.Valid {
			out.Status = "linked"
			out.LinkToken = ""
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	identity, err := q.IssueSlackIdentityGet(ctx, tx, sqlc.IssueSlackIdentityGetParams{AppID: c.AppID, SlackUserID: in.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
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
	raw := make([]byte, api.IssueSlackLinkNonceBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	// Use database time for the expiry as well as response mutation.
	now, err := q.IssueSlackInteractionNow(ctx, tx)
	if err != nil {
		return "", err
	}
	err = q.IssueSlackChallengePut(ctx, tx, sqlc.IssueSlackChallengePutParams{Token: token, AppID: appID, DeliveryID: issueUUID(in.DeliveryID), RequestKey: in.RequestKey, SlackUserID: in.UserID, PolicyRevision: in.PolicyRevision, ExpiresAt: issueTime(now.Time.Add(api.IssueSlackLinkTTL))})
	return token, err
}
