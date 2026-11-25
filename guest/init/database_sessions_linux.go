//go:build linux

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func (g *databaseGuest) collectSessions(database string, response dbguest.Response) dbguest.Response {
	if response.State != dbguest.StateReady || !g.diagnosticsRunning.CompareAndSwap(false, true) {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	defer g.diagnosticsRunning.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := g.queryIn(ctx, database, dbguest.SessionSQL)
	var sample dbguest.SessionSample
	if err != nil || json.Unmarshal([]byte(data), &sample) != nil || sample.SampledAt.IsZero() || sample.Sessions == nil {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	if len(sample.Sessions) > dbguest.MaxSessionRows {
		sample.Sessions = sample.Sessions[:dbguest.MaxSessionRows]
		sample.HasMore = true
	}
	response.Sessions = &sample
	return response
}
