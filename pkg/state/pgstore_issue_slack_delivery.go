package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ClaimIssueSlackDelivery(ctx context.Context) (IssueSlackClaim, bool, error) {
	out := IssueSlackClaim{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	app, err := q.IssueSlackCandidateApp(ctx, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	r, err := q.IssueSlackCandidateDelivery(ctx, tx, sqlc.IssueSlackCandidateDeliveryParams{AppID: app, EntitledPlans: issueSlackEntitledPlans()})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out = IssueSlackClaim{RoutingRuleKey: r.RoutingRuleKey, RoutingRevision: r.RoutingRevision, ID: issueID(r.ID), AppID: issueID(r.AppID), AccountID: issueID(r.AccountID), DestinationID: issueID(r.DestinationID), DestinationRevision: r.DestinationRevision, NotificationID: issueID(r.NotificationID), IncidentID: issueID(r.IncidentID), IssueID: issueID(r.IssueID), LinkID: issueID(r.LinkID), AppSlug: r.AppSlug, TeamID: issueID(r.TeamID), ChannelID: r.ChannelID, Kind: r.Kind, Reason: r.Reason, Attempt: int(r.Attempt) + 1, TokenSealed: r.TokenSealed, Status: "in_flight"}
	out.Application = r.ActivityID.Valid || r.ReminderID.Valid
	if out.Application {
		if err = json.Unmarshal(r.Details, &out.Details); err != nil {
			return IssueSlackClaim{}, false, err
		}
	}
	status, reason := "", ""
	switch {
	case !r.Eligible:
		status, reason = "canceled", "destination_unavailable"
	case !r.ExpiresAt.Time.After(time.Now()):
		status, reason = "dead", "delivery_expired"
	case r.Attempt >= api.IssueSlackMaxAttempts:
		status, reason = "dead", "attempts_exhausted"
	}
	if status != "" {
		out.Status = status
		out.TokenSealed = nil
		if err = q.IssueSlackTerminal(ctx, tx, sqlc.IssueSlackTerminalParams{ID: r.ID, Status: status, LastError: reason}); err != nil {
			return out, false, err
		}
		return out, true, tx.Commit(ctx)
	}
	out.ClaimID = uuid.NewString()
	until := issueTime(time.Now().Add(api.IssueSlackLease))
	if err = q.IssueSlackClaim(ctx, tx, sqlc.IssueSlackClaimParams{ID: r.ID, ClaimID: issueUUID(out.ClaimID), ClaimUntil: until}); err != nil {
		return out, false, err
	}
	if err = q.IssueSlackDestinationLease(ctx, tx, sqlc.IssueSlackDestinationLeaseParams{ID: r.DestinationID, ClaimID: issueUUID(out.ClaimID), ClaimUntil: until}); err != nil {
		return out, false, err
	}
	args := sqlc.IssueSlackThreadEnsureParams{DestinationID: r.DestinationID, DestinationRevision: r.DestinationRevision, IncidentID: r.IncidentID, ChannelID: r.ChannelID}
	if err = q.IssueSlackThreadEnsure(ctx, tx, args); err != nil {
		return out, false, err
	}
	out.ThreadTS, err = q.IssueSlackThreadGet(ctx, tx, sqlc.IssueSlackThreadGetParams(args))
	if err != nil {
		return out, false, err
	}
	return out, true, tx.Commit(ctx)
}
func (s *PgStore) IssueSlackDeliveryReady(ctx context.Context, c IssueSlackClaim) (bool, error) {
	return sqlc.New().IssueSlackReady(ctx, s.pool, sqlc.IssueSlackReadyParams{ID: issueUUID(c.ID), ClaimID: issueUUID(c.ClaimID), EntitledPlans: issueSlackEntitledPlans()})
}
func (s *PgStore) FinishIssueSlackDelivery(ctx context.Context, c IssueSlackClaim, r IssueSlackResult) error {
	if !validIssueSlackResult(r) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(c.AppID)); err != nil {
		return mapErr(err)
	}
	held, err := q.IssueSlackHeld(ctx, tx, sqlc.IssueSlackHeldParams{ID: issueUUID(c.ID), ClaimID: issueUUID(c.ClaimID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIssueSlackClaimLost
	}
	if err != nil {
		return err
	}
	if held.DestinationRevision != c.DestinationRevision || issueID(held.DestinationID) != c.DestinationID || issueID(held.IncidentID) != c.IncidentID || held.ChannelID != c.ChannelID {
		return ErrIssueSlackClaimLost
	}
	if r.Status == "succeeded" || r.ResetThread {
		root, err := q.IssueSlackThreadGet(ctx, tx, sqlc.IssueSlackThreadGetParams{DestinationID: held.DestinationID, DestinationRevision: held.DestinationRevision, IncidentID: held.IncidentID, ChannelID: held.ChannelID})
		if err != nil {
			return err
		}
		if root == "" || r.ResetThread {
			ts := r.MessageTS
			if r.ResetThread {
				ts = ""
			}
			if err = q.IssueSlackThreadSet(ctx, tx, sqlc.IssueSlackThreadSetParams{DestinationID: held.DestinationID, DestinationRevision: held.DestinationRevision, IncidentID: held.IncidentID, ChannelID: held.ChannelID, RootTs: ts}); err != nil {
				return err
			}
		}
	}
	if r.Status == "succeeded" {
		if err = q.IssueSlackActionSent(ctx, tx, sqlc.IssueSlackActionSentParams{DeliveryID: held.ID, MessageTs: r.MessageTS}); err != nil {
			return err
		}
	}
	next := r.RetryAt
	if next.IsZero() {
		next = time.Now()
	}
	if err = q.IssueSlackFinish(ctx, tx, sqlc.IssueSlackFinishParams{ID: held.ID, Status: r.Status, LastError: r.Error, ResponseCode: int32(r.ResponseCode), NextAttemptAt: issueTime(next)}); err != nil {
		return err
	}
	cooldown := time.Now().Add(api.IssueSlackSendInterval)
	if r.Error == "rate_limited" && next.After(cooldown) {
		cooldown = next
	}
	if err = q.IssueSlackRelease(ctx, tx, sqlc.IssueSlackReleaseParams{ID: held.DestinationID, ClaimID: issueUUID(c.ClaimID), NextSendAt: issueTime(cooldown)}); err != nil {
		return err
	}
	if r.Error == "invalid_auth" {
		installed, e := readSlackInstallation(ctx, q, tx, c.AppID, c.AccountID)
		if e != nil {
			return e
		}
		if installed.Status == "connected" && bytes.Equal(installed.TokenSealed, c.TokenSealed) {
			if e = invalidateSlackOAuth(ctx, q, tx, c.AppID, false); e != nil {
				return e
			}
			if e = q.IssueSlackInstallationDisable(ctx, tx, sqlc.IssueSlackInstallationDisableParams{AppID: issueUUID(c.AppID), Status: "revoked"}); e != nil {
				return e
			}
		}
	}
	return tx.Commit(ctx)
}
func validIssueSlackResult(r IssueSlackResult) bool {
	if r.ResponseCode < 0 || r.ResponseCode > 599 || (r.ResetThread && (r.Error != "thread_unavailable" || r.Status != "failed")) {
		return false
	}
	switch r.Status {
	case "succeeded":
		return r.Error == "" && ValidIssueSlackTimestamp(r.MessageTS) && !r.ResetThread
	case "failed", "dead", "canceled":
	default:
		return false
	}
	switch r.Error {
	case "destination_unavailable", "delivery_expired", "attempts_exhausted", "token_unavailable", "transport_error", "rate_limited", "slack_unavailable", "slack_rejected", "invalid_auth", "channel_not_found", "not_in_channel", "missing_scope", "thread_unavailable", "invalid_response":
		return true
	}
	return false
}
