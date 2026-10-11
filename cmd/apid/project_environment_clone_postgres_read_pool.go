package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
)

// A single worker owner supplies the pool, shared across operations, accounts,
// source capture and target verification. Per-attempt configuration cannot
// replace it with another directory/accounting owner. Deployment stays disabled
// until the full coordinator also supplies CPU/placement and billing admission.
func (s *server) reserveProjectEnvironmentClonePostgresRead(ctx context.Context, cfg copycontents.Config) (copycontents.Config, func(), error) {
	if s.cloneWorkerAdmission != nil {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return copycontents.Config{}, nil, err
		}
	}
	if s.clonePostgresContentsReadPool == nil {
		return copycontents.Config{}, nil, managedpostgres.ErrUnavailable
	}
	if cfg.ReadPool != nil && cfg.ReadPool != s.clonePostgresContentsReadPool {
		return copycontents.Config{}, nil, managedpostgres.ErrConflict
	}
	cfg.ReadPool = s.clonePostgresContentsReadPool
	return cfg.ReserveReadForWorker(ctx)
}

func (s *server) authorizeProjectEnvironmentClonePostgresRead(ctx context.Context, cfg copycontents.Config) error {
	if s.cloneWorkerAdmission != nil {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
	}
	if s.clonePostgresContentsReadPool == nil || cfg.ReadPool != s.clonePostgresContentsReadPool {
		return managedpostgres.ErrConflict
	}
	return cfg.CheckReadAdmissionForWorker(ctx)
}
