package state

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueHandoffAction(action string) bool {
	switch action {
	case "created", "regressed", "reopened", "impact_threshold_reached":
		return true
	default:
		return false
	}
}

// The handoff and summary transition share a transaction and activity identity,
// but have distinct outbox IDs. Delivery uses only this persisted snapshot.
func issueHandoff(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, activity sqlc.IssueActivity, details map[string]string, now time.Time, eventID string) error {
	if !issueHandoffAction(activity.Action) {
		return nil
	}
	subscribed, err := q.IssueHasHandoffRecipients(ctx, tx, sqlc.IssueHasHandoffRecipientsParams{AccountID: row.AccountID, AppID: row.AppID})
	if err != nil || !subscribed {
		return err
	}
	owner, err := q.IssueHandoffContext(ctx, tx, sqlc.IssueHandoffContextParams{AccountID: row.AccountID, AppID: row.AppID})
	if err != nil {
		return err
	}
	plan := api.Plan(owner.Plan)
	p := api.IssueHandoff{
		SchemaVersion: 1, ID: uuid.NewString(), Type: "issue.handoff", GeneratedAt: now,
		Issue:      issueRow(row),
		Transition: api.IssueActivity{ID: issueID(activity.ID), Action: activity.Action, ActorAccountID: issueID(activity.ActorAccountID), CreatedAt: activity.CreatedAt.Time, Details: details},
		Evidence:   api.IssueHandoffEvidence{IssuePath: "/v1/apps/" + url.PathEscape(owner.Slug) + "/issues/" + issueID(row.ID)},
		Gaps:       []string{},
	}
	retainedFrom := now.AddDate(0, 0, -plan.IssueLimits().RetentionDays)
	impactFrom := now.Add(-api.IssueImpactAlertWindow)
	if retainedFrom.After(impactFrom) {
		impactFrom = retainedFrom
	}
	impact, err := q.IssueImpact(ctx, tx, sqlc.IssueImpactParams{IssueID: row.ID, Since: issueTime(impactFrom), Until: issueTime(now)})
	if err != nil {
		return err
	}
	p.Impact = api.IssueImpact{WindowStart: impactFrom, WindowEnd: now, IdentifiedCustomers: impact.IdentifiedCustomers, ObservedEvents: impact.ObservedEvents, UnattributedEvents: impact.UnattributedEvents, Coverage: "observed retained events; uninstrumented or rejected failures are not counted"}
	sample, err := q.IssueHandoffSample(ctx, tx, sqlc.IssueHandoffSampleParams{
		IssueID: row.ID, AppID: row.AppID, RetainedFrom: issueTime(retainedFrom), Now: issueTime(now),
		EventID: issueUUID(eventID), DeploymentID: issueUUID(details["deployment_id"]),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		p.Gaps = append(p.Gaps, "occurrence_unavailable_or_expired")
	} else if err != nil {
		return err
	} else {
		var event api.IssueEvent
		if err := json.Unmarshal(sample.Payload, &event); err != nil {
			return err
		}
		p.Sample = &event
		p.Release = &api.IssueRelease{DeploymentID: issueID(sample.DeploymentID), CommitSHA: sample.CommitSha, ImageDigest: sample.ImageDigest, EventCount: sample.EventCount, FirstSeenAt: sample.FirstSeenAt.Time, LastSeenAt: sample.LastSeenAt.Time}
		if sample.CommitSha == "" {
			p.Gaps = append(p.Gaps, "commit_sha_unavailable")
		}
		if event.StackTrace == "" && len(event.Frames) == 0 {
			p.Gaps = append(p.Gaps, "stack_unavailable")
		}
		if err := issueHandoffRequest(ctx, q, tx, &p, row, sample, event, plan, owner.Slug, now); err != nil {
			return err
		}
	}
	raw, err := issues.MarshalHandoff(p)
	if err != nil {
		return err
	}
	return q.IssueAddHandoff(ctx, tx, sqlc.IssueAddHandoffParams{ID: issueUUID(p.ID), AccountID: row.AccountID, AppID: row.AppID, ActivityID: activity.ID, Payload: raw})
}

func issueHandoffRequest(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, p *api.IssueHandoff, row sqlc.AppIssue, sample sqlc.IssueHandoffSampleRow, event api.IssueEvent, plan api.Plan, slug string, now time.Time) error {
	if !plan.DebugTelemetryEnabled() {
		p.Gaps = append(p.Gaps, "request_evidence_disabled")
		return nil
	}
	if event.RequestID == "" && event.TraceID == "" {
		p.Gaps = append(p.Gaps, "request_correlation_missing")
		return nil
	}
	requests, err := q.IssueHandoffRequests(ctx, tx, sqlc.IssueHandoffRequestsParams{
		AccountID: row.AccountID, AppID: row.AppID, DeploymentID: sample.DeploymentID,
		RequestID: event.RequestID, TraceID: event.TraceID,
		RetainedFrom: issueTime(now.AddDate(0, 0, -plan.DebugTelemetryRetentionDays())), Now: issueTime(now),
		Since: issueTime(event.OccurredAt.Add(-api.IssueMaxClockSkew)), Until: issueTime(event.OccurredAt.Add(api.IssueMaxClockSkew)),
	})
	if err != nil {
		return err
	}
	if len(requests) == 0 {
		p.Gaps = append(p.Gaps, "request_unavailable_or_expired")
		return nil
	}
	if len(requests) > 1 {
		p.Gaps = append(p.Gaps, "request_correlation_ambiguous")
		return nil
	}
	r := requests[0]
	if (event.TraceID != "" && event.TraceID != r.TraceID.String) ||
		(event.RequestID != "" && event.RequestID != issueID(r.ID) && event.RequestID != r.TraceID.String) {
		p.Gaps = append(p.Gaps, "request_correlation_conflict")
		return nil
	}
	spans, truncated, gap := issues.HandoffSpans(r.SpansSummary)
	if gap != "" {
		p.Gaps = append(p.Gaps, gap)
	}
	p.Request = &api.IssueHandoffRequest{ID: issueID(r.ID), TraceID: r.TraceID.String, Route: r.Route, Method: r.Method, Status: int(r.Status), LatencyMS: int64(r.LatencyMs), ColdBoot: r.ColdBoot, ReceivedAt: r.ReceivedAt.Time, Spans: spans, SpansTruncated: truncated}
	p.Evidence.RequestPath = "/v1/apps/" + url.PathEscape(slug) + "/debug/requests/" + issueID(r.ID) + "/evidence"
	if r.TraceID.String != "" {
		p.Evidence.TracePath = "/v1/account/traces/" + url.PathEscape(r.TraceID.String)
	}
	return nil
}
