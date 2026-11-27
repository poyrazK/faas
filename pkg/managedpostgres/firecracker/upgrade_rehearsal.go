package firecracker

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *Provider) UpgradeRehearsal(ctx context.Context, resourceID, key string, major int, start bool) (api.ManagedPostgresUpgradeRehearsal, error) {
	client, ok := p.scheduler.(interface {
		RequestDatabaseUpgradeRehearsal(context.Context, *scheddpb.RequestDatabaseUpgradeRehearsalRequest, ...grpc.CallOption) (*scheddpb.DatabaseUpgradeRehearsal, error)
		GetDatabaseUpgradeRehearsal(context.Context, *scheddpb.GetDatabaseUpgradeRehearsalRequest, ...grpc.CallOption) (*scheddpb.DatabaseUpgradeRehearsal, error)
	})
	if !ok {
		return api.ManagedPostgresUpgradeRehearsal{}, managedpostgres.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var r *scheddpb.DatabaseUpgradeRehearsal
	var err error
	if start {
		if major < dbguest.MinMajor || major > dbguest.MaxMajor {
			return api.ManagedPostgresUpgradeRehearsal{}, managedpostgres.ErrInvalid
		}
		r, err = client.RequestDatabaseUpgradeRehearsal(ctx, &scheddpb.RequestDatabaseUpgradeRehearsalRequest{
			DatabaseId: resourceUUID(resourceID), RequestId: key, PostgresMajor: int32(major), DatabaseName: p.cfg.Database,
		})
	} else {
		r, err = client.GetDatabaseUpgradeRehearsal(ctx, &scheddpb.GetDatabaseUpgradeRehearsalRequest{DatabaseId: resourceUUID(resourceID), RehearsalId: key})
	}
	if status.Code(err) == codes.Unimplemented {
		return api.ManagedPostgresUpgradeRehearsal{}, managedpostgres.ErrUnsupported
	}
	if err != nil {
		return api.ManagedPostgresUpgradeRehearsal{}, providerError(err)
	}
	d := r.GetDrill()
	if d.GetDrillId() == "" || d.GetRequestId() == "" || d.GetStartedAt().CheckValid() != nil || r.GetTargetMajor() <= r.GetSourceMajor() {
		return api.ManagedPostgresUpgradeRehearsal{}, managedpostgres.ErrUnavailable
	}
	outcome := d.GetOutcome()
	if outcome == "ok" {
		outcome = "succeeded"
	}
	out := api.ManagedPostgresUpgradeRehearsal{
		RehearsalID: d.GetDrillId(), RequestID: d.GetRequestId(), Outcome: outcome,
		SourceMajor: int(r.GetSourceMajor()), TargetMajor: int(r.GetTargetMajor()), Phase: r.GetPhase(), FailedPhase: r.GetFailedPhase(),
		StartedAt: d.GetStartedAt().AsTime(), FinishedAt: restoreDrillTime(d.GetFinishedAt()),
		RecoveryPoint: restoreDrillTime(d.GetTargetAt()), RecoveredTo: restoreDrillTime(d.GetRecoveredTo()), ErrorCode: d.GetErrorCode(),
	}
	if out.FinishedAt != nil {
		seconds := int64(max(out.FinishedAt.Sub(out.StartedAt), 0) / time.Second)
		out.DurationSeconds = &seconds
	}
	return out, nil
}
