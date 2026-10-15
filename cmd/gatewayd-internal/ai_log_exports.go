package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/state"
)

// AI gateway export lines (ADR-1340) reach an app's drains as their own
// source: the instance ID below, with the outbox row ID as the sequence, so
// each drain's durable queue resumes and dedupes them like runtime lines.
const (
	aiLogExportSource       = "ai-gateway"
	aiLogExportStream       = "ai_gateway"
	aiLogExportPollInterval = 5 * time.Second
)

// aiLogExportStore reads the export outbox outboundd writes at settlement.
type aiLogExportStore interface {
	ListAIGatewayLogExports(ctx context.Context, appID string, afterID int64, createdAfter time.Time,
		visibility time.Duration, limit int) ([]state.AIGatewayLogExport, error)
}

// aiLogExportWorker copies an app's AI gateway lines into one drain's
// sender until ctx ends. A drain new to this gateway (a fresh queue, or an
// ownership handoff) starts one reconcile interval back, so a handoff can
// repeat or skip that interval's lines, as it can for runtime lines.
func (m *appLogDrainManager) aiLogExportWorker(ctx context.Context, spec state.AppLogDrain, sender *logdrain.Sender, store aiLogExportStore) {
	cursor := sender.LastSequences()[aiLogExportSource]
	var since time.Time
	if cursor == 0 {
		since = time.Now().Add(-appLogDrainReconcileInterval)
	}
	ticker := time.NewTicker(aiLogExportPollInterval)
	defer ticker.Stop()
	for {
		cursor = m.copyAILogExports(ctx, spec, sender, store, cursor, since)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// copyAILogExports enqueues every visible line after cursor and returns the
// new cursor. A full or failed queue stops the pass without moving past the
// line, so the next pass retries it.
func (m *appLogDrainManager) copyAILogExports(ctx context.Context, spec state.AppLogDrain, sender *logdrain.Sender,
	store aiLogExportStore, cursor int64, since time.Time) int64 {
	for {
		rows, err := store.ListAIGatewayLogExports(ctx, spec.AppID, cursor, since,
			api.OutboundAILogExportVisibilitySeconds*time.Second, api.OutboundAILogExportBatchMax)
		if err != nil {
			if ctx.Err() == nil {
				m.log.WarnContext(ctx, "list AI gateway log exports", slog.String("drain_id", spec.ID),
					slog.String("app_id", spec.AppID), slog.String("err", err.Error()))
			}
			return cursor
		}
		for _, row := range rows {
			if row.AccountID == spec.AccountID && !sender.Enqueue(logdrain.Record{
				AppID: spec.AppID, AccountID: spec.AccountID, TenantID: spec.AccountID, InstanceID: aiLogExportSource,
				Sequence: uint64(row.ID), Stream: aiLogExportStream, Line: row.Line, WrittenAt: row.CreatedAt,
				Attributes: row.Attributes,
			}) {
				return cursor
			}
			cursor = row.ID
		}
		if len(rows) < api.OutboundAILogExportBatchMax {
			return cursor
		}
	}
}
