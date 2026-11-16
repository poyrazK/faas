package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// statelessAdvisoryAuditKind is the audit row kind apid's advisory receiver
// writes too; `gregale audit --kind-prefix=stateless.advisory` lists both.
const statelessAdvisoryAuditKind = "stateless.advisory"

type statelessAdvisoryEmitter interface {
	Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
}

type statelessAdvisoryApps interface {
	AppByID(ctx context.Context, id string) (state.App, error)
}

// statelessAdvisoryAuditor is the Manager's AdvisoryForwarder on a
// database-backed vmmd (ADR-047 amendment 2026-11-12). apid runs on the
// control plane, so a compute node has no /run/faas/apid.sock and the
// original gRPC hop could never deliver from it; vmmd already writes audit
// rows through its store (sidecar init failures), so it writes this one too,
// in the shape apid's receiver uses, and sends the same live-events summary.
type statelessAdvisoryAuditor struct {
	audit statelessAdvisoryEmitter
	apps  statelessAdvisoryApps
	// notify sends the stateless_advisory pg_notify summary the SSE
	// stream relays; nil skips it.
	notify func(ctx context.Context, payload string) error
}

func (a *statelessAdvisoryAuditor) Forward(ctx context.Context, instance, appID string, events []fcvm.AdvisoryEvent) error {
	if a == nil || a.audit == nil || appID == "" || instance == "" || len(events) == 0 {
		return nil
	}
	// The row belongs to the app's account. An app deleted since its wake
	// has nobody to tell; any other lookup failure still records the row,
	// unattributed, as apid's receiver does.
	var subject *string
	if a.apps != nil {
		app, err := a.apps.AppByID(ctx, appID)
		switch {
		case errors.Is(err, state.ErrNotFound):
			return nil
		case err == nil && app.AccountID != "":
			account := app.AccountID
			subject = &account
		}
	}
	rows := make([]map[string]any, 0, len(events))
	paths := make([]string, 0, len(events))
	for _, e := range events {
		rows = append(rows, map[string]any{
			"path":       e.Path,
			"mask":       strings.Join(e.Masks, ","),
			"pid":        e.PID,
			"ts_unix_ms": e.TsUnix,
		})
		paths = append(paths, e.Path)
	}
	a.audit.Emit(ctx, statelessAdvisoryAuditKind, subject, map[string]any{
		"instance": instance,
		"app_id":   appID,
		"count":    len(events),
		"events":   rows,
		"severity": api.StatelessAdvisoryBatchSeverity(paths),
	})
	if a.notify != nil {
		summary, _ := json.Marshal(map[string]any{
			"app_id": appID, "instance": instance, "n": len(events), "sample_path": events[0].Path,
		})
		// Best effort: a missed frame leaves the audit row in place.
		_ = a.notify(ctx, string(summary))
	}
	return nil
}
