//go:build linux

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func (g *databaseGuest) collectDiagnostics(req dbguest.Request, response dbguest.Response) dbguest.Response {
	if response.State != dbguest.StateReady || !g.diagnosticsRunning.CompareAndSwap(false, true) {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	defer g.diagnosticsRunning.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if options := req.TableHealthOptions; options != nil {
		return g.collectTableHealth(ctx, req.DiagnosticDatabase, *options, response)
	}
	data, err := g.queryIn(ctx, req.DiagnosticDatabase, dbguest.ActivitySQL)
	var sample dbguest.ActivitySample
	if err != nil || json.Unmarshal([]byte(data), &sample) != nil || sample.SampledAt.IsZero() {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	response.Activity = &sample
	return response
}

func (g *databaseGuest) collectTableHealth(ctx context.Context, database string, options dbguest.TableHealthOptions, response dbguest.Response) dbguest.Response {
	sql, err := dbguest.TableHealthQuery(options)
	if err != nil {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	data, err := g.queryIn(ctx, database, sql)
	var sample dbguest.TableHealthSample
	if err != nil || json.Unmarshal([]byte(data), &sample) != nil || sample.SampledAt.IsZero() ||
		sample.Tables == nil || len(sample.Tables) > options.Limit {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	response.TableHealth = &sample
	return response
}
