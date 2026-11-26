//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

func (g *databaseGuest) checkMigration(src dbguest.ImportSource) dbguest.Response {
	resp := g.response()
	if resp.State != dbguest.StateReady {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	if err := g.serveImportBridge(); err != nil {
		resp.Migration = &dbmigration.Check{Outcome: dbmigration.Unverified, TargetMajor: g.major, Reasons: []string{"source_relay_unavailable"}}
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := g.inspectMigration(ctx, src, g.sourceQuery)
	resp.Migration = &result
	return resp
}

func (g *databaseGuest) inspectMigration(ctx context.Context, src dbguest.ImportSource, query func(context.Context, dbguest.ImportSource, string) (string, error)) dbmigration.Check {
	result := dbmigration.Check{Outcome: dbmigration.Unverified, TargetMajor: g.major}
	out, err := query(ctx, src, dbmigration.InspectSourceSQL)
	if err != nil {
		result.Reasons = []string{"source_inspection_unavailable"}
		return result
	}
	var source dbmigration.Source
	if err := json.Unmarshal([]byte(out), &source); err != nil || source.Major < 1 || source.DatabaseBytes <= 0 {
		result.Reasons = []string{"source_inspection_unavailable"}
		return result
	}
	total, free, err := importVolume()
	if err != nil {
		result.Reasons = []string{"target_inspection_unavailable"}
		return result
	}
	err = g.checkImportTargetEmpty(ctx, src.TargetDatabase, false)
	if err != nil && !errors.Is(err, errImportTargetNotEmpty) {
		result.Reasons = []string{"target_inspection_unavailable"}
		return result
	}
	workers, workerErr := g.queryIn(ctx, src.TargetDatabase, `SELECT
 current_setting('max_worker_processes')::bigint >= 3
 AND current_setting('max_sync_workers_per_subscription')::bigint >= 2
 AND current_setting('max_logical_replication_workers')::bigint -
    (SELECT count(*) FROM pg_catalog.pg_stat_subscription WHERE pid IS NOT NULL) >= 3`)
	if workerErr != nil || (workers != "t" && workers != "f") {
		result.Reasons = []string{"target_inspection_unavailable"}
		return result
	}
	result = dbmigration.Assess(source, dbmigration.Target{
		Major: g.major, Empty: err == nil, HasWorkerCapacity: workers == "t",
		FreeBytes: int64(min(free, 1<<62)), StorageReserveBytes: int64(min(importSpaceFloor(total), 1<<62)), //nolint:gosec // bounded
	})
	return result
}
