package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueUUID(id string) pgtype.UUID {
	v, err := uuid.Parse(id)
	return pgtype.UUID{Bytes: v, Valid: err == nil}
}
func issueID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
func issueTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}
func issueTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
func issueNullableTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return issueTime(*t)
}
func issueRow(r sqlc.AppIssue) api.Issue {
	return api.Issue{ID: issueID(r.ID), AppID: issueID(r.AppID), Environment: r.Environment, Fingerprint: r.Fingerprint, GroupingVersion: int(r.GroupingVersion), Title: r.Title, State: r.State, AssigneeAccountID: issueID(r.AssigneeAccountID), FirstSeenAt: r.FirstSeenAt.Time, LastSeenAt: r.LastSeenAt.Time, EventCount: r.EventCount, RegressionCount: r.RegressionCount, ResolvedAt: issueTimePtr(r.ResolvedAt), FixedDeploymentID: issueID(r.FixedDeploymentID), FixedDeploymentCreatedAt: issueTimePtr(r.FixedDeploymentCreatedAt), IgnoredUntil: issueTimePtr(r.IgnoredUntil)}
}
func issueTokenRow(r sqlc.IssueIngestToken) api.IssueIngestToken {
	return api.IssueIngestToken{ID: issueID(r.ID), AppID: issueID(r.AppID), DeploymentID: issueID(r.DeploymentID), Environment: r.Environment, Name: r.Name, ExpiresAt: r.ExpiresAt.Time, RevokedAt: issueTimePtr(r.RevokedAt)}
}

func (s *PgStore) CreateIssueToken(ctx context.Context, account, app string, in api.CreateIssueIngestTokenRequest, hash []byte, lim api.IssueLimits) (api.IssueIngestToken, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueIngestToken{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueIngestToken{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueIngestToken{}, ErrNotFound
	}
	if _, err = q.IssueDeploymentScope(ctx, tx, sqlc.IssueDeploymentScopeParams{DeploymentID: issueUUID(in.DeploymentID), AppID: issueUUID(app), AccountID: issueUUID(account), Environment: in.Environment}); err != nil {
		return api.IssueIngestToken{}, mapErr(err)
	}
	n, err := q.IssueCountTokens(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueIngestToken{}, err
	}
	if n >= int64(lim.TokensPerApp) {
		return api.IssueIngestToken{}, &IssueLimitError{Resource: "ingest_tokens", Limit: int64(lim.TokensPerApp), Observed: n + 1}
	}
	r, err := q.IssueInsertToken(ctx, tx, sqlc.IssueInsertTokenParams{AccountID: issueUUID(account), AppID: issueUUID(app), DeploymentID: issueUUID(in.DeploymentID), Environment: in.Environment, Name: in.Name, TokenHash: hash, ExpiresAt: issueTime(in.ExpiresAt)})
	if err != nil {
		return api.IssueIngestToken{}, err
	}
	return issueTokenRow(r), tx.Commit(ctx)
}
func (s *PgStore) FindIssueToken(ctx context.Context, hash []byte, now time.Time) (IssueCredential, error) {
	r, err := sqlc.New().IssueFindToken(ctx, s.pool, sqlc.IssueFindTokenParams{TokenHash: hash, Now: issueTime(now)})
	if err != nil {
		return IssueCredential{}, mapErr(err)
	}
	return IssueCredential{issueID(r.ID), issueID(r.AccountID), issueID(r.AppID), issueID(r.DeploymentID), r.Environment}, nil
}
func (s *PgStore) ListIssueTokens(ctx context.Context, app string) ([]api.IssueIngestToken, error) {
	rows, err := sqlc.New().IssueListTokens(ctx, s.pool, issueUUID(app))
	out := make([]api.IssueIngestToken, 0, len(rows))
	for _, r := range rows {
		out = append(out, issueTokenRow(r))
	}
	return out, err
}
func (s *PgStore) RevokeIssueToken(ctx context.Context, app, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return mapErr(err)
	}
	n, err := q.IssueRevokeToken(ctx, tx, sqlc.IssueRevokeTokenParams{AppID: issueUUID(app), ID: issueUUID(id)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// RecordIssue serializes short writes per app. The unique event key, group
// creation, counters, attribution, and transition outbox commit together.
func (s *PgStore) RecordIssue(ctx context.Context, in RecordIssueParams) (api.IssueEventResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	c := in.Credential
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(c.AppID))
	if err != nil {
		return api.IssueEventResponse{}, mapErr(err)
	}
	if issueID(owner.AccountID) != c.AccountID {
		return api.IssueEventResponse{}, ErrNotFound
	}
	if c.ID != "" {
		valid, checkErr := q.IssueTokenStillValid(ctx, tx, sqlc.IssueTokenStillValidParams{ID: issueUUID(c.ID), AppID: owner.ID, DeploymentID: issueUUID(c.DeploymentID), Now: issueTime(time.Now().UTC())})
		if checkErr != nil {
			return api.IssueEventResponse{}, checkErr
		}
		if !valid {
			return api.IssueEventResponse{}, ErrNotFound
		}
	}
	dep, err := q.IssueDeploymentScope(ctx, tx, sqlc.IssueDeploymentScopeParams{DeploymentID: issueUUID(c.DeploymentID), AppID: issueUUID(c.AppID), AccountID: owner.AccountID, Environment: c.Environment})
	if err != nil {
		return api.IssueEventResponse{}, mapErr(err)
	}
	old, err := q.IssueFindEvent(ctx, tx, sqlc.IssueFindEventParams{AppID: owner.ID, DeploymentID: dep.ID, EventID: issueUUID(in.Event.EventID)})
	if err == nil {
		if old.PayloadHash != in.PayloadHash {
			return api.IssueEventResponse{}, ErrIssueEventConflict
		}
		return api.IssueEventResponse{IssueID: issueID(old.IssueID), EventID: in.Event.EventID, Duplicate: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.IssueEventResponse{}, err
	}
	if err = q.IssuePurgeEvents(ctx, tx, sqlc.IssuePurgeEventsParams{AppID: owner.ID, Before: issueTime(in.Now.AddDate(0, 0, -in.Limits.RetentionDays))}); err != nil {
		return api.IssueEventResponse{}, err
	}
	n, err := q.IssueCountEvents(ctx, tx, owner.ID)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	if n >= int64(in.Limits.EventsPerApp) {
		return api.IssueEventResponse{}, &IssueLimitError{Resource: "events", Limit: int64(in.Limits.EventsPerApp), Observed: n + 1}
	}
	n, err = q.IssueCountRecentEvents(ctx, tx, sqlc.IssueCountRecentEventsParams{AppID: owner.ID, Since: issueTime(in.Now.Add(-time.Minute))})
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	if n >= int64(in.Limits.EventsPerMinute) {
		return api.IssueEventResponse{}, &IssueLimitError{Resource: "events_per_minute", Limit: int64(in.Limits.EventsPerMinute), Observed: n + 1, Rate: true}
	}
	row, err := q.IssueFindGroup(ctx, tx, sqlc.IssueFindGroupParams{AppID: owner.ID, Environment: c.Environment, GroupingVersion: int32(in.GroupingVersion), Fingerprint: in.Fingerprint})
	created := errors.Is(err, pgx.ErrNoRows)
	if created {
		n, err = q.IssueCount(ctx, tx, owner.ID)
		if err != nil {
			return api.IssueEventResponse{}, err
		}
		if n >= int64(in.Limits.IssuesPerApp) {
			return api.IssueEventResponse{}, &IssueLimitError{Resource: "issues", Limit: int64(in.Limits.IssuesPerApp), Observed: n + 1}
		}
		row, err = q.IssueCreate(ctx, tx, sqlc.IssueCreateParams{AccountID: owner.AccountID, AppID: owner.ID, Environment: c.Environment, Fingerprint: in.Fingerprint, GroupingVersion: int32(in.GroupingVersion), Title: in.Title, OccurredAt: issueTime(in.Event.OccurredAt)})
	}
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	consumer, tenant, err := issueAttribution(ctx, q, tx, in)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	raw, err := json.Marshal(in.Event)
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	if err = q.IssueInsertEvent(ctx, tx, sqlc.IssueInsertEventParams{AppID: owner.ID, DeploymentID: dep.ID, EventID: issueUUID(in.Event.EventID), IssueID: row.ID, PayloadHash: in.PayloadHash, Payload: raw, OccurredAt: issueTime(in.Event.OccurredAt), ReceivedAt: issueTime(in.Now), ConsumerID: consumer, TenantID: tenant}); err != nil {
		return api.IssueEventResponse{}, err
	}
	regressed := issues.Recurs(issueRow(row), c.DeploymentID, in.Event.OccurredAt, row.FixedDeploymentCreatedAt.Valid && dep.CreatedAt.Time.After(row.FixedDeploymentCreatedAt.Time))
	row, err = q.IssueObserve(ctx, tx, sqlc.IssueObserveParams{ID: row.ID, OccurredAt: issueTime(in.Event.OccurredAt), Regressed: regressed})
	if err != nil {
		return api.IssueEventResponse{}, err
	}
	if err = q.IssueObserveRelease(ctx, tx, sqlc.IssueObserveReleaseParams{IssueID: row.ID, DeploymentID: dep.ID, CommitSha: dep.CommitSha.String, ImageDigest: dep.ImageDigest, OccurredAt: issueTime(in.Event.OccurredAt)}); err != nil {
		return api.IssueEventResponse{}, err
	}
	action := ""
	if created {
		action = "created"
	}
	if regressed {
		action = "regressed"
	}
	if action != "" {
		if err = issueActivity(ctx, q, tx, row, action, "", map[string]string{"deployment_id": c.DeploymentID}, in.Now); err != nil {
			return api.IssueEventResponse{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return api.IssueEventResponse{}, err
	}
	return api.IssueEventResponse{IssueID: issueID(row.ID), EventID: in.Event.EventID, Regressed: regressed}, nil
}

func issueAttribution(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, in RecordIssueParams) (pgtype.UUID, pgtype.UUID, error) {
	var consumer, tenant pgtype.UUID
	c := in.Credential
	if in.Event.InvocationID != "" {
		r, err := q.IssueInvocationScope(ctx, db, sqlc.IssueInvocationScopeParams{ID: issueUUID(in.Event.InvocationID), AppID: issueUUID(c.AppID), AccountID: issueUUID(c.AccountID)})
		if err != nil {
			return consumer, tenant, mapErr(err)
		}
		tenant = r.PlatformTenantID
	}
	if in.Event.RequestID != "" {
		rows, err := q.IssueRequestAttribution(ctx, db, sqlc.IssueRequestAttributionParams{AccountID: issueUUID(c.AccountID), AppID: issueUUID(c.AppID), DeploymentID: issueUUID(c.DeploymentID), RequestID: in.Event.RequestID})
		if err != nil {
			return consumer, tenant, err
		}
		if len(rows) > 1 {
			return consumer, tenant, nil
		}
		if len(rows) == 1 {
			consumer = issueUUID(rows[0].ConsumerKey)
			if tenant.Valid && rows[0].PlatformTenantID.Valid && tenant != rows[0].PlatformTenantID {
				return consumer, tenant, ErrInvalidArgument
			}
			if rows[0].PlatformTenantID.Valid {
				tenant = rows[0].PlatformTenantID
			}
			return consumer, tenant, nil
		}
	}
	if in.Event.TraceID != "" {
		rows, err := q.IssueAttribution(ctx, db, sqlc.IssueAttributionParams{AccountID: issueUUID(c.AccountID), AppID: issueUUID(c.AppID), DeploymentID: issueUUID(c.DeploymentID), TraceID: pgtype.Text{String: in.Event.TraceID, Valid: true}, Since: issueTime(in.Event.OccurredAt.Add(-api.IssueMaxClockSkew)), Until: issueTime(in.Event.OccurredAt.Add(api.IssueMaxClockSkew))})
		if err != nil {
			return consumer, tenant, err
		}
		if len(rows) == 1 {
			consumer = rows[0].ConsumerID
			if tenant.Valid && rows[0].PlatformTenantID.Valid && tenant != rows[0].PlatformTenantID {
				return consumer, tenant, ErrInvalidArgument
			}
			if rows[0].PlatformTenantID.Valid {
				tenant = rows[0].PlatformTenantID
			}
		}
	}
	return consumer, tenant, nil
}

func issueActivity(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, action, actor string, details map[string]string, now time.Time) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	activity, err := q.IssueAddActivity(ctx, tx, sqlc.IssueAddActivityParams{IssueID: row.ID, Action: action, ActorAccountID: issueUUID(actor), CreatedAt: issueTime(now), Details: raw})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"id": issueID(activity.ID), "type": "issue." + action, "issue": issueRow(row), "details": details})
	if err != nil {
		return err
	}
	return q.IssueAddTransition(ctx, tx, sqlc.IssueAddTransitionParams{ActivityID: activity.ID, AccountID: row.AccountID, AppID: row.AppID, Payload: payload})
}

func (s *PgStore) ListIssues(ctx context.Context, app string, filter IssueListFilter, cur IssueCursor) (api.ListIssuesResponse, error) {
	rows, err := sqlc.New().IssueList(ctx, s.pool, sqlc.IssueListParams{
		AppID: issueUUID(app), State: filter.State, Environment: filter.Environment,
		AssigneeAccountID: issueUUID(filter.AssigneeAccountID), Unassigned: filter.Unassigned,
		CursorTime: issueTime(cur.Time), CursorID: issueUUID(cur.ID), PageLimit: api.IssuePageSize + 1,
	})
	if err != nil {
		return api.ListIssuesResponse{}, err
	}
	out := api.ListIssuesResponse{Items: make([]api.Issue, 0, len(rows))}
	for i, r := range rows {
		if i == api.IssuePageSize {
			break
		}
		out.Items = append(out.Items, issueRow(r))
	}
	if len(rows) > api.IssuePageSize {
		last := out.Items[len(out.Items)-1]
		out.NextCursor = EncodeIssueCursor(IssueCursor{last.LastSeenAt, last.ID})
	}
	if len(out.Items) == 0 {
		return out, nil
	}
	issueIDs := make([]pgtype.UUID, 0, len(out.Items))
	for _, item := range out.Items {
		issueIDs = append(issueIDs, issueUUID(item.ID))
	}
	windowEnd := time.Now().UTC()
	windowStart := windowEnd.Add(-api.IssueImpactSummaryWindow)
	impactRows, err := sqlc.New().IssueImpactSummaries(ctx, s.pool, sqlc.IssueImpactSummariesParams{
		IssueIds: issueIDs, Since: issueTime(windowStart), Until: issueTime(windowEnd),
	})
	if err != nil {
		return api.ListIssuesResponse{}, err
	}
	impacts := make(map[string]api.IssueImpactSummary, len(impactRows))
	for _, row := range impactRows {
		impacts[issueID(row.IssueID)] = api.IssueImpactSummary{
			IdentifiedCustomers: row.IdentifiedCustomers,
			ObservedEvents:      row.ObservedEvents,
			UnattributedEvents:  row.UnattributedEvents,
		}
	}
	for i := range out.Items {
		impact := impacts[out.Items[i].ID]
		out.Items[i].Impact24h = &impact
	}
	return out, nil
}

func (s *PgStore) GetIssueDetail(ctx context.Context, app, id string, since, until time.Time, cur IssueDetailCursors) (api.IssueDetail, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return api.IssueDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	r, err := q.IssueGet(ctx, tx, sqlc.IssueGetParams{AppID: issueUUID(app), ID: issueUUID(id)})
	if err != nil {
		return api.IssueDetail{}, mapErr(err)
	}
	out := api.IssueDetail{Issue: issueRow(r), Events: []api.IssueOccurrence{}, Releases: []api.IssueRelease{}, Activity: []api.IssueActivity{}}
	events, err := q.IssueListEvents(ctx, tx, sqlc.IssueListEventsParams{IssueID: r.ID, Since: issueTime(since), Until: issueTime(until), CursorTime: issueTime(cur.Events.Time), CursorID: issueUUID(cur.Events.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, e := range events {
		if i == api.IssuePageSize {
			break
		}
		var event api.IssueEvent
		if err = json.Unmarshal(e.Payload, &event); err != nil {
			return out, err
		}
		attr := "unattributed"
		if e.VerifiedConsumerID.Valid || e.VerifiedPlatformTenantID.Valid {
			attr = "verified"
		}
		out.Events = append(out.Events, api.IssueOccurrence{ID: issueID(e.ID), IssueEvent: event, DebugRequestID: issueDebugRequest(ctx, q, tx, issueID(r.AccountID), issueID(r.AppID), issueID(e.DeploymentID), event), DeploymentID: issueID(e.DeploymentID), ReceivedAt: e.ReceivedAt.Time, VerifiedConsumerID: issueID(e.VerifiedConsumerID), VerifiedPlatformTenantID: issueID(e.VerifiedPlatformTenantID), Attribution: attr})
	}
	if len(events) > api.IssuePageSize {
		last := out.Events[len(out.Events)-1]
		out.NextEventCursor = EncodeIssueCursor(IssueCursor{last.OccurredAt, last.ID})
	}
	releases, err := q.IssueListReleases(ctx, tx, sqlc.IssueListReleasesParams{IssueID: r.ID, CursorTime: issueTime(cur.Releases.Time), CursorID: issueUUID(cur.Releases.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, e := range releases {
		if i == api.IssuePageSize {
			last := out.Releases[len(out.Releases)-1]
			out.NextReleaseCursor = EncodeIssueCursor(IssueCursor{last.FirstSeenAt, last.DeploymentID})
			break
		}
		out.Releases = append(out.Releases, api.IssueRelease{DeploymentID: issueID(e.DeploymentID), CommitSHA: e.CommitSha, ImageDigest: e.ImageDigest, EventCount: e.EventCount, FirstSeenAt: e.FirstSeenAt.Time, LastSeenAt: e.LastSeenAt.Time})
	}
	activity, err := q.IssueListActivity(ctx, tx, sqlc.IssueListActivityParams{IssueID: r.ID, CursorTime: issueTime(cur.Activity.Time), CursorID: issueUUID(cur.Activity.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, a := range activity {
		if i == api.IssuePageSize {
			last := out.Activity[len(out.Activity)-1]
			out.NextActivityCursor = EncodeIssueCursor(IssueCursor{last.CreatedAt, last.ID})
			break
		}
		var details map[string]string
		if err = json.Unmarshal(a.Details, &details); err != nil {
			return out, err
		}
		out.Activity = append(out.Activity, api.IssueActivity{ID: issueID(a.ID), Action: a.Action, ActorAccountID: issueID(a.ActorAccountID), CreatedAt: a.CreatedAt.Time, Details: details})
	}
	impact, err := q.IssueImpact(ctx, tx, sqlc.IssueImpactParams{IssueID: r.ID, Since: issueTime(since), Until: issueTime(until)})
	if err != nil {
		return out, err
	}
	out.Impact = api.IssueImpact{WindowStart: since, WindowEnd: until, IdentifiedCustomers: impact.IdentifiedCustomers, ObservedEvents: impact.ObservedEvents, UnattributedEvents: impact.UnattributedEvents, Coverage: "observed retained events; uninstrumented or rejected failures are not counted"}
	return out, tx.Commit(ctx)
}

func (s *PgStore) ActOnIssue(ctx context.Context, app, id, actor string, in api.IssueActionRequest, now time.Time) (api.Issue, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.Issue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Same lock order as ingestion prevents counter/action races and deadlocks.
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return api.Issue{}, mapErr(err)
	}
	row, err := q.IssueGetLocked(ctx, tx, sqlc.IssueGetLockedParams{AppID: issueUUID(app), ID: issueUUID(id)})
	if err != nil {
		return api.Issue{}, mapErr(err)
	}
	p := sqlc.IssueUpdateActionParams{ID: row.ID, State: row.State, Assignee: row.AssigneeAccountID, ResolvedAt: row.ResolvedAt, FixedDeploymentID: row.FixedDeploymentID, FixedDeploymentCreatedAt: row.FixedDeploymentCreatedAt, IgnoredUntil: row.IgnoredUntil}
	details := map[string]string{}
	action := ""
	switch in.Action {
	case "assign":
		if in.AssigneeAccountID != "" {
			ok, err := q.IssueAssigneeAllowed(ctx, tx, sqlc.IssueAssigneeAllowedParams{AppID: row.AppID, Assignee: issueUUID(in.AssigneeAccountID)})
			if err != nil {
				return api.Issue{}, err
			}
			if !ok {
				return api.Issue{}, ErrNotFound
			}
		}
		if issueID(row.AssigneeAccountID) == in.AssigneeAccountID {
			return issueRow(row), nil
		}
		p.Assignee = issueUUID(in.AssigneeAccountID)
		details["assignee_account_id"] = in.AssigneeAccountID
		action = "assigned"
	case "resolve":
		if row.State == "resolved" && issueID(row.FixedDeploymentID) == in.FixedDeploymentID {
			return issueRow(row), nil
		}
		dep, scopeErr := q.IssueDeploymentScope(ctx, tx, sqlc.IssueDeploymentScopeParams{AppID: row.AppID, AccountID: row.AccountID, DeploymentID: issueUUID(in.FixedDeploymentID), Environment: row.Environment})
		if scopeErr != nil {
			return api.Issue{}, mapErr(scopeErr)
		}
		p.State = "resolved"
		p.ResolvedAt = issueTime(now)
		p.FixedDeploymentID = issueUUID(in.FixedDeploymentID)
		p.FixedDeploymentCreatedAt = dep.CreatedAt
		p.IgnoredUntil = pgtype.Timestamptz{}
		details["fixed_deployment_id"] = in.FixedDeploymentID
		action = "resolved"
		if err = q.IssueAddResolution(ctx, tx, sqlc.IssueAddResolutionParams{IssueID: row.ID, DeploymentID: p.FixedDeploymentID, ResolvedAt: p.ResolvedAt, ActorAccountID: issueUUID(actor)}); err != nil {
			return api.Issue{}, err
		}
	case "expire-ignore":
		if row.State != "ignored" || !row.IgnoredUntil.Valid || row.IgnoredUntil.Time.After(now) {
			return issueRow(row), nil
		}
		p.State = "open"
		p.IgnoredUntil = pgtype.Timestamptz{}
		action = "reopened"
	case "reopen":
		if row.State == "open" {
			return issueRow(row), nil
		}
		p.State = "open"
		p.ResolvedAt = pgtype.Timestamptz{}
		p.FixedDeploymentID = pgtype.UUID{}
		p.FixedDeploymentCreatedAt = pgtype.Timestamptz{}
		p.IgnoredUntil = pgtype.Timestamptz{}
		action = "reopened"
	case "ignore":
		if row.State == "ignored" && in.IgnoredUntil != nil && row.IgnoredUntil.Valid && row.IgnoredUntil.Time.Equal(*in.IgnoredUntil) {
			return issueRow(row), nil
		}
		p.State = "ignored"
		p.IgnoredUntil = issueNullableTime(in.IgnoredUntil)
		action = "ignored"
	default:
		return api.Issue{}, ErrInvalidArgument
	}
	row, err = q.IssueUpdateAction(ctx, tx, p)
	if err != nil {
		return api.Issue{}, err
	}
	if err = issueActivity(ctx, q, tx, row, action, actor, details, now); err != nil {
		return api.Issue{}, err
	}
	return issueRow(row), tx.Commit(ctx)
}

// MaintainIssues is a bounded sweep, independent of whether an app receives
// new events. Issue metadata and release totals outlive occurrence retention.
func (s *PgStore) MaintainIssues(ctx context.Context, now time.Time) error {
	q := sqlc.New()
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		if _, err := q.IssuePurgePlanEvents(ctx, s.pool, sqlc.IssuePurgePlanEventsParams{Plan: string(plan), Before: issueTime(now.AddDate(0, 0, -plan.IssueLimits().RetentionDays)), BatchLimit: api.IssueMaintenanceBatch}); err != nil {
			return err
		}
	}
	if _, err := q.IssuePurgeExpiredTokens(ctx, s.pool, issueTime(now)); err != nil {
		return err
	}
	if err := s.enrichIssueAttribution(ctx, now); err != nil {
		return err
	}
	rows, err := q.IssueExpiredIgnores(ctx, s.pool, sqlc.IssueExpiredIgnoresParams{Now: issueTime(now), BatchLimit: api.IssueMaintenanceBatch})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := s.ActOnIssue(ctx, issueID(r.AppID), issueID(r.ID), "", api.IssueActionRequest{Action: "expire-ignore"}, now); err != nil {
			return err
		}
	}
	return nil
}

func issueDebugRequest(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, dep string, e api.IssueEvent) string {
	if e.RequestID == "" && e.TraceID == "" {
		return ""
	}
	rows, err := q.IssueDebugRequest(ctx, db, sqlc.IssueDebugRequestParams{AccountID: issueUUID(account), AppID: issueUUID(app), DeploymentID: issueUUID(dep), RequestID: issueUUID(e.RequestID), TraceID: e.TraceID, Since: issueTime(e.OccurredAt.Add(-api.IssueMaxClockSkew)), Until: issueTime(e.OccurredAt.Add(api.IssueMaxClockSkew))})
	if err == nil && len(rows) == 1 {
		return issueID(rows[0])
	}
	return ""
}

func (s *PgStore) enrichIssueAttribution(ctx context.Context, now time.Time) error {
	q := sqlc.New()
	rows, err := q.IssueUnattributedEvents(ctx, s.pool, sqlc.IssueUnattributedEventsParams{Before: issueTime(now.Add(-api.IssueMaintenanceInterval)), BatchLimit: api.IssueMaintenanceBatch})
	if err != nil {
		return err
	}
	for _, row := range rows {
		var e api.IssueEvent
		if err := json.Unmarshal(row.Payload, &e); err != nil {
			return err
		}
		in := RecordIssueParams{Credential: IssueCredential{AccountID: issueID(row.AccountID), AppID: issueID(row.AppID), DeploymentID: issueID(row.DeploymentID), Environment: row.Environment}, Event: e}
		consumer, tenant, err := issueAttribution(ctx, q, s.pool, in)
		// Deleted invocations or conflicting evidence never acquire a customer.
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalidArgument) {
			return err
		}
		if err != nil {
			consumer = pgtype.UUID{}
			tenant = pgtype.UUID{}
		}
		if err := q.IssueEnrichAttribution(ctx, s.pool, sqlc.IssueEnrichAttributionParams{AppID: row.AppID, DeploymentID: row.DeploymentID, EventID: row.EventID, ConsumerID: consumer, TenantID: tenant, Now: issueTime(now)}); err != nil {
			return err
		}
	}
	return nil
}
