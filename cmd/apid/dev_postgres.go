package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

const devPostgresEnvironmentKey = "DATABASE_URL"

const devPostgresStorageLimitBytes int64 = 1 << 30

// ensureDevPostgres creates or reuses the database whose name matches the
// stable developer app slug, then binds it to DATABASE_URL. The name is an
// internal idempotency key; callers only receive safe resource metadata.
func (s *server) ensureDevPostgres(ctx context.Context, acct state.Account, app state.App, request *api.DevPostgresRequest) (*api.DevPostgresResponse, error) {
	if request == nil {
		return nil, nil
	}
	if s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return nil, managedpostgres.ErrUnavailable
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.DatabasesMax == 0 || !limits.DevelopmentAllowed {
		return nil, managedpostgres.ErrQuotaExceeded
	}
	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return nil, err
	}
	wasExisting := false
	var existing *managedpostgres.Database
	for _, database := range databases {
		if database.Name == app.Slug {
			wasExisting = true
			copy := database
			existing = &copy
			break
		}
	}
	region := request.Region
	if region == "" {
		if existing != nil {
			region = existing.Spec.Region
		} else {
			region = s.managedPostgres.DefaultRegion()
		}
	}
	spec := managedpostgres.Spec{
		Region:               region,
		PostgresMajor:        16,
		Class:                managedpostgres.ClassDevelopment,
		Availability:         managedpostgres.AvailabilitySingleZone,
		ScaleToZero:          true,
		StorageLimitBytes:    devPostgresStorageLimitBytes,
		RestoreWindowSeconds: 0,
	}
	if existing != nil {
		if existing.Spec.Class != managedpostgres.ClassDevelopment || !existing.Spec.ScaleToZero || existing.Spec.Availability != managedpostgres.AvailabilitySingleZone {
			return nil, managedpostgres.ErrConflict
		}
		if request.Region == "" {
			spec = existing.Spec
		}
	}
	if spec.StorageLimitBytes > limits.StorageLimitBytes {
		return nil, managedpostgres.ErrQuotaExceeded
	}
	database, err := s.managedPostgres.Create(ctx, managedpostgres.CreateRequest{
		AccountID: acct.ID,
		Name:      app.Slug,
		Spec:      spec,
	})
	if err != nil {
		return nil, err
	}

	binding, bindingCreated, err := s.managedPostgresBindings.CreateWithResult(ctx, managedpostgres.CreateBindingRequest{
		AccountID:      acct.ID,
		DatabaseID:     database.ID,
		AppID:          app.ID,
		Scope:          api.DefaultEnvScope,
		EnvironmentKey: devPostgresEnvironmentKey,
		Access:         managedpostgres.CredentialReadWrite,
	})
	if err != nil {
		if bindingCreated && binding.ID != "" {
			if _, deleteErr := s.managedPostgresBindings.Delete(context.WithoutCancel(ctx), acct.ID, binding.ID); deleteErr != nil && s.log != nil {
				s.log.Warn("dev postgres binding compensation failed", "binding_id", binding.ID, "err", deleteErr)
			}
		}
		if !wasExisting {
			if _, deleteErr := s.managedPostgres.Delete(context.WithoutCancel(ctx), acct.ID, database.ID); deleteErr != nil && s.log != nil {
				s.log.Warn("dev postgres database compensation failed", "database_id", database.ID, "err", deleteErr)
			}
		}
		return nil, err
	}
	return &api.DevPostgresResponse{
		DatabaseID:     database.ID,
		Name:           database.Name,
		State:          string(database.State),
		BindingID:      binding.ID,
		BindingState:   string(binding.State),
		EnvironmentKey: binding.EnvironmentKey,
	}, nil
}

// cleanupDevPostgres removes only the automatically named database when its
// DATABASE_URL binding belongs to this developer app. A database with the
// same name but no matching binding is never touched, which keeps explicit
// customer-managed attachments safe.
func (s *server) cleanupDevPostgres(ctx context.Context, app state.App) error {
	if app.PreviewOfSlug == "" || app.PreviewPrNumber != 0 || s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return nil
	}
	databases, err := s.managedPostgres.List(ctx, app.AccountID)
	if err != nil {
		return err
	}
	for _, database := range databases {
		if database.Name != app.Slug {
			continue
		}
		bindings, listErr := s.managedPostgresBindings.List(ctx, app.AccountID, database.ID)
		if listErr != nil {
			return listErr
		}
		owned := false
		for _, binding := range bindings {
			if binding.AppID != app.ID || binding.Scope != api.DefaultEnvScope || binding.EnvironmentKey != devPostgresEnvironmentKey {
				continue
			}
			owned = true
			if _, deleteErr := s.managedPostgresBindings.Delete(ctx, app.AccountID, binding.ID); deleteErr != nil && !errors.Is(deleteErr, managedpostgres.ErrNotFound) {
				return fmt.Errorf("delete dev postgres binding: %w", deleteErr)
			}
		}
		if !owned {
			return nil
		}
		remaining, listErr := s.managedPostgresBindings.List(ctx, app.AccountID, database.ID)
		if listErr != nil {
			return listErr
		}
		if len(remaining) != 0 {
			return managedpostgres.ErrConflict
		}
		if _, err := s.managedPostgres.Delete(ctx, app.AccountID, database.ID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return fmt.Errorf("delete dev postgres database: %w", err)
		}
		return nil
	}
	return nil
}
