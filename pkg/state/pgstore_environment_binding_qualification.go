package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationServiceReceiptStore = (*PgStore)(nil)

func candidateBindingContextTx(ctx context.Context, tx pgx.Tx, accountID, deploymentID string) ([]byte, error) {
	q := sqlc.New()
	row, err := q.EnvironmentBindingCandidate(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return nil, mapErr(err)
	}
	d, err := bindingVerificationDeploymentJSON(row.Deployment)
	if err != nil {
		return nil, err
	}
	body, err := q.ReadBindingReleasePolicy(ctx, tx, sqlc.ReadBindingReleasePolicyParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(d.AppID), Scope: d.Scope})
	if err != nil {
		return nil, mapErr(err)
	}
	policy, err := decodeBindingReleasePolicy(body, d.AppID, d.Scope)
	if err != nil || policy.Mode != "enforce" {
		return nil, err
	}
	revision, err := q.ReadServiceBindingRevision(ctx, tx, sqlc.ReadServiceBindingRevisionParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(d.AppID)})
	if err != nil {
		return nil, mapErr(err)
	}
	c, err := candidateBindingContext(d, policy, revision)
	if err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

func (s *PgStore) RecordEnvironmentQualificationServiceExchange(ctx context.Context, request EnvironmentQualificationServiceRequest, original EnvironmentQualificationServiceRoute) error {
	if !qualificationServiceRequestValid(request) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := s.resolveEnvironmentQualificationServiceTx(ctx, tx, request)
	if err != nil {
		return err
	}
	if !qualificationServiceExchangeMatches(current, original) {
		return ErrConflict
	}
	q := sqlc.New()
	row, err := q.EnvironmentWorkloadQualificationForUpdate(ctx, tx, mustPgUUID(original.Caller.RequestID))
	if err != nil {
		return mapErr(err)
	}
	caller := qualificationRequestFromSQL(row)
	if caller.BindingContext == nil {
		return nil
	}
	if err := candidateBindingRequestCurrentTx(ctx, tx, caller); err != nil {
		return err
	}
	raw, err := json.Marshal(original)
	if err != nil {
		return err
	}
	if err := q.RecordEnvironmentQualificationServiceExchange(ctx, tx, sqlc.RecordEnvironmentQualificationServiceExchangeParams{RequestID: row.ID, Attempt: caller.Attempt, Binding: request.Binding, Route: raw}); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

func candidateBindingRequestCurrentTx(ctx context.Context, tx sqlc.DBTX, request EnvironmentWorkloadQualificationRequest) error {
	q := sqlc.New()
	row, err := q.EnvironmentBindingCandidate(ctx, tx, mustPgUUID(request.DeploymentID))
	if err != nil {
		return mapErr(err)
	}
	d, err := bindingVerificationDeploymentJSON(row.Deployment)
	if err != nil {
		return err
	}
	if !row.CatalogSupported {
		return ErrBindingReleaseRequired
	}
	body, err := q.ReadBindingReleasePolicy(ctx, tx, sqlc.ReadBindingReleasePolicyParams{AppID: mustPgUUID(d.AppID), AccountID: row.AccountID, Scope: d.Scope})
	if err != nil {
		return mapErr(err)
	}
	policy, err := decodeBindingReleasePolicy(body, d.AppID, d.Scope)
	if err != nil {
		return err
	}
	revision, err := q.ReadServiceBindingRevision(ctx, tx, sqlc.ReadServiceBindingRevisionParams{AccountID: row.AccountID, AppID: mustPgUUID(d.AppID)})
	if err != nil {
		return mapErr(err)
	}
	if !candidateBindingContextCurrent(request, d, policy, revision) {
		return ErrBindingReleaseRequired
	}
	return nil
}

func checkEnvironmentBindingGraphTx(ctx context.Context, tx sqlc.DBTX, projectID, scope string, graph EnvironmentWorkloadGraph, members []ProjectReleaseMember) (bool, error) {
	q := sqlc.New()
	required, err := q.EnvironmentBindingReleaseRequired(ctx, tx, sqlc.EnvironmentBindingReleaseRequiredParams{ProjectID: mustPgUUID(projectID), Scope: scope})
	if err != nil || !required {
		return required, mapErr(err)
	}
	if !candidateBindingMembersMatch(graph, members) {
		return true, ErrBindingReleaseRequired
	}
	rows, err := q.EnvironmentWorkloadQualificationsByGraph(ctx, tx, mustPgUUID(graph.ID))
	if err != nil {
		return true, mapErr(err)
	}
	requests := map[string]EnvironmentWorkloadQualificationRequest{}
	for _, row := range rows {
		r := qualificationRequestFromSQL(row)
		requests[r.AppID] = r
	}
	if len(requests) != len(members) {
		return true, ErrBindingReleaseRequired
	}
	stored, err := q.EnvironmentQualificationServiceExchanges(ctx, tx, mustPgUUID(graph.ID))
	if err != nil {
		return true, mapErr(err)
	}
	receipts := map[string]EnvironmentQualificationServiceReceipt{}
	for _, row := range stored {
		var route EnvironmentQualificationServiceRoute
		if err := json.Unmarshal(row.Route, &route); err != nil {
			return true, err
		}
		receipts[qualificationServiceReceiptKey(pgUUIDString(row.RequestID), row.Attempt, row.Binding)] = EnvironmentQualificationServiceReceipt{Binding: row.Binding, Route: route, RecordedAt: row.RecordedAt.Time}
	}
	for _, member := range members {
		r := requests[member.AppID]
		if r.DeploymentID != member.DeploymentID {
			return true, ErrBindingReleaseRequired
		}
		if err := candidateBindingRequestCurrentTx(ctx, tx, r); err != nil {
			return true, err
		}
		smoke, err := q.EnvironmentQualificationSmokeReceipt(ctx, tx, sqlc.EnvironmentQualificationSmokeReceiptParams{RequestID: mustPgUUID(r.ID), Attempt: r.Attempt})
		if err != nil {
			return true, ErrBindingReleaseRequired
		}
		if _, err := candidateBindingEvidenceDeadline(r, qualificationSmokeReceiptFromSQL(smoke), receipts, requests, time.Now()); err != nil {
			return true, err
		}
	}
	return true, nil
}
