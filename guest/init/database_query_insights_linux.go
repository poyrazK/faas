//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbqueryinsights"
)

func queryInsightsModulePresent(major int) bool {
	version := strconv.Itoa(major)
	library := filepath.Join("/usr/lib/postgresql", version, "lib/pg_stat_statements.so")
	control := filepath.Join("/usr/share/postgresql", version, "extension/pg_stat_statements.control")
	for _, path := range []string{library, control} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func (g *databaseGuest) queryInsightsSettings() []string {
	if !g.queryInsights {
		return nil
	}
	if !g.queryInsightsSupported {
		g.log.Warn("database query statistics module unavailable", "postgres_major", g.major)
		return nil
	}
	return []string{"-c", "shared_preload_libraries=pg_stat_statements", "-c", "compute_query_id=on",
		"-c", "pg_stat_statements.track=top", "-c", "pg_stat_statements.max=" + strconv.Itoa(dbqueryinsights.MaxEntries)}
}

func (g *databaseGuest) collectQueryInsights(database string, response dbguest.Response) dbguest.Response {
	if response.State != dbguest.StateReady || !g.queryInsights || !g.queryInsightsSupported || !g.queryInsightsRunning.CompareAndSwap(false, true) {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	defer g.queryInsightsRunning.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if !g.queryInsightsInstalled {
		if _, err := g.queryIn(ctx, "postgres", dbqueryinsights.InstallSQL); err != nil {
			response.Error = dbguest.ErrCodeNotReady
			return response
		}
		g.queryInsightsInstalled = true
	}
	data, err := g.queryIn(ctx, "postgres", dbqueryinsights.SampleSQL(database))
	var sample dbqueryinsights.Sample
	if err != nil || json.Unmarshal([]byte(data), &sample) != nil || !sample.Valid() {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	response.QuerySample = &sample
	return response
}
