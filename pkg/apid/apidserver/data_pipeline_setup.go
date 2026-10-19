package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func (s *server) configurePipelineSetup(service *datapipeline.Service) {
	s.configurePipelineDataAPI(service)
	if s.managedPostgres == nil {
		return
	}
	service.PlanMirror = func(ctx context.Context, account, region string) (datapipeline.MirrorPlan, error) {
		if region == "" {
			region = s.managedPostgres.DefaultRegion()
		}
		spec, err := s.pipelineMirrorSpec(ctx, account, datapipeline.MirrorPlan{Region: region})
		if err != nil {
			return datapipeline.MirrorPlan{}, pipelineMirrorError(err)
		}
		if _, err = s.managedPostgres.DiscoverCapabilities(ctx, account, region); err != nil {
			return datapipeline.MirrorPlan{}, pipelineMirrorError(err)
		}
		return datapipeline.MirrorPlan{Region: spec.Region, StorageLimitBytes: spec.StorageLimitBytes, RestoreWindowSeconds: spec.RestoreWindowSeconds}, nil
	}
	service.EnsureMirror = func(ctx context.Context, job datapipeline.Setup) error {
		spec, err := s.pipelineMirrorSpec(ctx, job.AccountID, job.MirrorPlan)
		if err != nil {
			return pipelineMirrorError(err)
		}
		_, err = s.managedPostgres.CreateWithID(ctx, managedpostgres.CreateRequest{AccountID: job.AccountID, Name: job.MirrorName, Spec: spec}, job.DatabaseID)
		return pipelineMirrorError(err)
	}
	service.MirrorState = func(ctx context.Context, account, id string) (string, error) {
		database, err := s.managedPostgres.Get(ctx, account, id)
		if err != nil {
			return "", pipelineMirrorError(err)
		}
		if database.Spec.PostgresMajor != 16 {
			return "", datapipeline.ErrInvalid
		}
		if database.State == managedpostgres.StateFailed || database.State == managedpostgres.StateDeleting || database.State == managedpostgres.StateDeleted {
			return string(database.State), nil
		}
		if database.DesiredGeneration != database.ObservedGeneration {
			return "provisioning", nil
		}
		return string(database.State), nil
	}
}
func (s *server) pipelineMirrorSpec(ctx context.Context, account string, plan datapipeline.MirrorPlan) (managedpostgres.Spec, error) {
	acct, err := s.store.AccountByID(ctx, account)
	if err != nil {
		return managedpostgres.Spec{}, err
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || !acct.MayDeploy() {
		return managedpostgres.Spec{}, managedpostgres.ErrNotInPlan
	}
	return managedPostgresSpecFromRequest(api.CreateManagedPostgresDatabaseRequest{Region: plan.Region, PostgresMajor: 16, StorageLimitBytes: plan.StorageLimitBytes, RestoreWindowSeconds: plan.RestoreWindowSeconds}, limits)
}
func pipelineMirrorError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		return datapipeline.ErrQuota
	case errors.Is(err, managedpostgres.ErrConflict):
		return datapipeline.ErrConflict
	case errors.Is(err, managedpostgres.ErrInvalid), errors.Is(err, managedpostgres.ErrNotInPlan), errors.Is(err, managedpostgres.ErrUnsupported):
		return datapipeline.ErrInvalid
	default:
		return datapipeline.ErrUnavailable
	}
}
