package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationOutboundStore = (*PgStore)(nil)

func (s *PgStore) PendingEnvironmentQualificationOutboundProbes(ctx context.Context, limit int) ([]api.OutboundQualificationProbe, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().PendingEnvironmentQualificationOutboundProbes(ctx, s.pool, int32(min(limit, 1000)))
	if err != nil {
		return nil, mapErr(err)
	}
	probes := make([]api.OutboundQualificationProbe, 0, len(rows))
	for _, row := range rows {
		probes = append(probes, api.OutboundQualificationProbe{RequestID: row.RequestID, Attempt: row.Attempt, AccountID: row.AccountID,
			AppID: row.AppID, IntegrationID: row.IntegrationID, Revision: row.Revision,
			Policy: api.OutboundBindingProbePolicy{Method: row.Method, Path: row.Path, ExpectedStatus: int(row.ExpectedStatus)}})
	}
	return probes, nil
}

func (s *PgStore) RecordEnvironmentQualificationOutboundReceipt(ctx context.Context, probe api.OutboundQualificationProbe, status int) error {
	if !validOutboundQualificationReceipt(probe, status) {
		return ErrInvalidArgument
	}
	// The insert guard rechecks the lease, probe policy and binding revision;
	// a change maps to ErrBindingPromotionChanged and records nothing.
	return mapErr(sqlc.New().RecordEnvironmentQualificationOutboundReceipt(ctx, s.pool, sqlc.RecordEnvironmentQualificationOutboundReceiptParams{
		RequestID: mustPgUUID(probe.RequestID), Attempt: probe.Attempt, IntegrationID: mustPgUUID(probe.IntegrationID),
		Revision: probe.Revision, Method: probe.Policy.Method, Path: probe.Policy.Path, Status: int32(status)}))
}

// candidateOutboundDeadlineTx evaluates environment_binding_outbound_deadline,
// the SQL the deferred activation guard uses. Zero means no active outbound binding.
func candidateOutboundDeadlineTx(ctx context.Context, tx sqlc.DBTX, request EnvironmentWorkloadQualificationRequest, _ string, age time.Duration) (time.Time, error) {
	deadline, err := sqlc.New().EnvironmentQualificationOutboundDeadline(ctx, tx, sqlc.EnvironmentQualificationOutboundDeadlineParams{
		MaxAgeSeconds: int64(age / time.Second), RequestID: mustPgUUID(request.ID)})
	if err != nil {
		return time.Time{}, mapErr(err)
	}
	return candidateDeadlineFromSQL(deadline)
}
