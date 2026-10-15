package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OperatorIncidentContext(ctx context.Context, f OperatorIncidentContextFilter) (api.ObsIncidentContextResponse, error) {
	if err := validateOperatorContextFilter(f); err != nil {
		return api.ObsIncidentContextResponse{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ObsIncidentContextResponse{}, fmt.Errorf("incident context snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := pgOperatorContext(ctx, tx, f)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func pgOperatorContext(ctx context.Context, tx pgx.Tx, f OperatorIncidentContextFilter) (api.ObsIncidentContextResponse, error) {
	q := sqlc.New()
	root, err := pgOperatorContextRoot(ctx, tx, q, f)
	if err != nil {
		return api.ObsIncidentContextResponse{}, err
	}
	scope, err := pgOperatorContextScope(ctx, tx, q, root)
	if err != nil {
		return api.ObsIncidentContextResponse{}, err
	}
	out, err := newOperatorContext(root, scope, f)
	if err != nil || scope.State != "known" {
		return out, err
	}
	params := sqlc.OperatorContextResourcesParams{AppID: scope.AppID, NodeID: scope.NodeID, AccountID: scope.AccountID, JobID: scope.JobID, BeforeKey: f.Before, PageLimit: int32(f.Limit + 1), Since: pgtype.Timestamptz{Time: out.Since, Valid: true}, Until: pgtype.Timestamptz{Time: out.Until, Valid: true}}
	if root.kind == "deployment" {
		params.DeploymentID = root.id
	}
	if root.kind == "job_run" {
		params.RunID = root.id
	}
	rows, err := q.OperatorContextResources(ctx, tx, params)
	if err != nil {
		return out, fmt.Errorf("incident context relationships: %w", err)
	}
	out.Counts = &api.ObsIncidentContextCounts{Apps: rows.Apps, Tenants: rows.Tenants, Deployments: rows.Deployments, Nodes: rows.Nodes, Jobs: rows.Jobs, JobRuns: rows.JobRuns, FailedResources: rows.FailedResources}
	page := []api.ObsIncidentRelatedResource{}
	if err := json.Unmarshal(rows.Resources, &page); err != nil {
		return out, fmt.Errorf("incident relationships projection: %w", err)
	}
	pageOperatorContext(&out, page)
	targets, err := json.Marshal(contextTargets(root, out))
	if err != nil {
		return out, fmt.Errorf("incident context targets: %w", err)
	}
	history, err := q.OperatorContextTimeline(ctx, tx, sqlc.OperatorContextTimelineParams{Targets: targets, Since: params.Since, Until: params.Until, PageLimit: int32(f.HistoryLimit + 1)})
	if err != nil {
		return out, fmt.Errorf("incident context timeline: %w", err)
	}
	out.TimelineTruncated = len(history) > f.HistoryLimit
	if out.TimelineTruncated {
		history = history[:f.HistoryLimit]
	}
	for _, r := range history {
		out.Timeline = append(out.Timeline, api.ObsIncidentRelatedEntry{ResourceKind: r.ResourceKind, ResourceID: r.ResourceID, Match: r.Match, Entry: api.ObsIncidentEvidenceEntry{ID: r.ID, Source: r.Source, Kind: r.Kind, Status: r.Status, At: r.At.Time.UTC(), TraceID: r.TraceID}})
	}
	return out, nil
}

func pgOperatorContextRoot(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, f OperatorIncidentContextFilter) (operatorContextRoot, error) {
	if f.Kind == "platform_alert" {
		e, err := q.OperatorAlertEpisode(ctx, tx, f.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return operatorContextRoot{}, ErrNotFound
		}
		if err != nil {
			return operatorContextRoot{}, err
		}
		c, err := q.OperatorAlertCollection(ctx, tx)
		if err != nil {
			return operatorContextRoot{}, err
		}
		episode := pgAlertEpisode(e)
		return operatorContextRoot{kind: f.Kind, id: episode.ID, status: episode.Status(pgAlertCollection(c), f.Now), at: episode.ObservedAt(), appID: episode.AppID, nodeID: episode.NodeID, accountID: episode.AccountID}, nil
	}
	r, err := q.OperatorContextRoot(ctx, tx, sqlc.OperatorContextRootParams{Kind: f.Kind, ResourceID: contextPGUUID(f.ID), StaleBefore: pgtype.Timestamptz{Time: f.Now.Add(-DefaultHeartbeatStaleness), Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return operatorContextRoot{}, ErrNotFound
	}
	if err != nil {
		return operatorContextRoot{}, err
	}
	return operatorContextRoot{kind: r.Kind, id: r.ID, status: r.Status, at: r.At.Time, appID: r.AppID, nodeID: r.NodeID, accountID: r.AccountID, jobID: r.JobID}, nil
}

func pgOperatorContextScope(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, r operatorContextRoot) (api.ObsIncidentContextScope, error) {
	var owner string
	var appOK, nodeOK, accountOK bool
	var err error
	if r.appID != "" {
		owner, err = q.OperatorContextAppOwner(ctx, tx, contextPGUUID(r.appID))
		appOK = err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return api.ObsIncidentContextScope{}, err
		}
	} else if r.nodeID != "" {
		nodeOK, err = q.OperatorContextNodeExists(ctx, tx, contextPGUUID(r.nodeID))
		if err != nil {
			return api.ObsIncidentContextScope{}, err
		}
		if r.accountID != "" {
			accountOK, err = q.OperatorContextAccountExists(ctx, tx, contextPGUUID(r.accountID))
			if err != nil {
				return api.ObsIncidentContextScope{}, err
			}
		}
	}
	return operatorContextScope(r, owner, appOK, nodeOK, accountOK), nil
}

func contextPGUUID(id string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true} }
