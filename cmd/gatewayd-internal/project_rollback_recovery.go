package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func observeProjectRollbackRoute(ctx context.Context, pool *pgxpool.Pool, inv invalidator, raw, node string, log *slog.Logger) {
	route, err := db.ParseDeploymentRouteChangedPayload(raw)
	if err != nil {
		log.Warn("gatewayd: bad project rollback route notification", "err", err)
		return
	}
	backend, ok := inv.(*gateway.PGBackend)
	if !ok || backend.HasProjectRollbackRoute(route.AppID, route.Generation) ||
		!refreshDeploymentRoute(ctx, inv, route, log) || !backend.ProjectRollbackTargetInstalled(route.AppID, route.DeploymentID) {
		return
	}
	store := state.NewPgStore(pool)
	ackCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	expires, err := store.AcknowledgeProjectRollbackHTTPRoute(ackCtx, route, node)
	cancel()
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			log.Warn("gatewayd: acknowledge project rollback route", "generation", route.Generation, "err", err)
		}
		return
	}
	backend.WatchProjectRollbackRoute(route.AppID, route.DeploymentID, route.Generation, expires,
		func(ctx context.Context, started, succeeded time.Time) (bool, error) {
			recorded, err := store.RecordProjectRollbackHTTPResponse(ctx, route, node, started, succeeded)
			if err != nil {
				log.Warn("gatewayd: record project rollback response", "generation", route.Generation, "err", err)
			}
			return recorded, err
		})
}
