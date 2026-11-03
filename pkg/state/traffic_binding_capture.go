// adr: 20261029190349 — what a binding write touched, from the capture
// triggers, checked against the fleet-wide diff.
package state

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TrafficBindingCaptureGapUnreadable is the gap kind of a write whose capture
// could not be read; every other kind is a trafficBindingChange kind.
const TrafficBindingCaptureGapUnreadable = "capture_unreadable"

// TrafficBindingCaptureObserver receives one audited binding write: its writer
// and the kind of every changed hostname the capture triggers did not record
// (empty when the capture covered the change).
type TrafficBindingCaptureObserver func(writer string, gaps []string)

var trafficBindingCaptureObserver atomic.Pointer[TrafficBindingCaptureObserver]

// SetTrafficBindingCaptureObserver wires the daemon's counter; nil disables it.
func SetTrafficBindingCaptureObserver(observe TrafficBindingCaptureObserver) {
	if observe == nil {
		trafficBindingCaptureObserver.Store(nil)
		return
	}
	trafficBindingCaptureObserver.Store(&observe)
}

// readTrafficBindingCapture returns the claim and reservation hostnames this
// transaction's statements touched, before and after: the identities the
// capture triggers recorded, expanded through the rows they name. added marks
// a newly inserted exact claim or reservation.
func readTrafficBindingCapture(ctx context.Context, tx sqlc.DBTX, appsSuffix string) ([]trafficBindingChange, error) {
	touched, err := sqlc.New().ReadTrafficBindingCapture(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("state: read binding capture: %w", err)
	}
	var captured []trafficBindingChange
	expand := sqlc.ExpandTrafficBindingCaptureParams{AppsSuffix: appsSuffix}
	for _, line := range strings.Split(touched, "\n") {
		fields := strings.Split(line, "\t")
		kind, added := strings.TrimSuffix(fields[0], "+"), strings.HasSuffix(fields[0], "+")
		switch {
		case len(fields) == 2 && (kind == TrafficBindingScopeKindDomain || kind == TrafficBindingScopeKindTenant):
			captured = append(captured, trafficCapturedHost(kind, fields[1], added))
		case len(fields) == 2 && kind == "slug" && appsSuffix != "":
			captured = append(captured, trafficCapturedHost(TrafficBindingScopeKindPrimary, fields[1]+appsSuffix, false))
		case len(fields) == 3 && kind == TrafficBindingScopeKindAlias && appsSuffix != "":
			captured = append(captured, trafficCapturedHost(kind, "tag-"+fields[2]+"-"+strings.ReplaceAll(fields[1], "-", "")+appsSuffix, added))
		case len(fields) == 2 && kind == "app" && added:
			expand.AddedAppIds = append(expand.AddedAppIds, fields[1])
		case len(fields) == 2 && kind == "app":
			expand.AppIds = append(expand.AppIds, fields[1])
		case len(fields) == 2 && kind == "surface":
			expand.SurfaceIds = append(expand.SurfaceIds, fields[1])
		case len(fields) == 2 && kind == "platform":
			expand.PlatformIds = append(expand.PlatformIds, fields[1])
		case len(fields) == 2 && kind == "environment":
			expand.EnvironmentIds = append(expand.EnvironmentIds, fields[1])
		}
	}
	if len(expand.AppIds)+len(expand.AddedAppIds)+len(expand.SurfaceIds)+len(expand.PlatformIds)+len(expand.EnvironmentIds) == 0 {
		return captured, nil
	}
	rows, err := sqlc.New().ExpandTrafficBindingCapture(ctx, tx, expand)
	if err != nil {
		return nil, fmt.Errorf("state: expand binding capture: %w", err)
	}
	for _, row := range rows {
		captured = append(captured, trafficCapturedHost(row.Kind, row.Host, row.Added))
	}
	return captured, nil
}

func trafficCapturedHost(kind, host string, added bool) trafficBindingChange {
	change := trafficBindingChange{host: normalizeTrafficLockName(host), kind: kind, added: added}
	if suffix, wild := WildcardDomainSuffix(change.host); wild {
		change.host, change.subtree = suffix, true
	}
	return change
}

// trafficBindingCaptureGaps lists the kinds of diff changes whose hostname
// the capture did not record.
func trafficBindingCaptureGaps(changes, captured []trafficBindingChange) []string {
	type name struct {
		host    string
		subtree bool
	}
	recorded := make(map[name]bool, len(captured))
	for _, change := range captured {
		recorded[name{change.host, change.subtree}] = true
	}
	var gaps []string
	for _, change := range changes {
		if !recorded[name{change.host, change.subtree}] {
			gaps = append(gaps, change.kind)
		}
	}
	return gaps
}

// auditTrafficBindingCapture compares the capture with the fleet-wide diff of
// a binding write. It never refuses the write: it reads inside a savepoint,
// and a capture it cannot read counts as a gap.
func auditTrafficBindingCapture(ctx context.Context, tx pgx.Tx, writer, appsSuffix string, changes []trafficBindingChange) {
	gaps := []string{TrafficBindingCaptureGapUnreadable}
	if savepoint, err := tx.Begin(ctx); err == nil {
		captured, err := readTrafficBindingCapture(ctx, savepoint, appsSuffix)
		_ = savepoint.Rollback(ctx)
		if err == nil {
			gaps = trafficBindingCaptureGaps(changes, captured)
		}
	}
	if observe := trafficBindingCaptureObserver.Load(); observe != nil {
		(*observe)(writer, gaps)
	}
}
