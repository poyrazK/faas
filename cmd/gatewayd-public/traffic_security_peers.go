// adr: 570
package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// trafficSecurityRoster lists the serving compute gateways with the wiring
// each one last reported (gateway_traffic_runtime_observations).
type trafficSecurityRoster interface {
	ListServingGatewayTrafficRuntime(context.Context) ([]state.ServingGatewayTrafficRuntime, error)
}

// trafficSecurityPeers decides whether the public edge may require the
// traffic security snapshot on compute gateway answers. Units release
// independently, so this edge runs beside N-1 compute gateways during a
// rollout (docs/release-compatibility.md). A gateway that predates the
// snapshot never stamps it and never reports security_revocation; while any
// serving gateway is such a peer, or its report went stale (a node rolled
// back stops reporting), unstamped answers pass as they did before ADR-570.
// On 2026-10-26 an rc.295 edge in front of rc.294 gateways refused every
// successful response as traffic_revocation_unavailable.
type trafficSecurityPeers struct {
	roster   trafficSecurityRoster
	log      *slog.Logger
	required atomic.Bool
}

// newTrafficSecurityPeers reads the roster once before serving. Until a read
// succeeds the snapshot stays required, as it was without this gate.
func newTrafficSecurityPeers(ctx context.Context, roster trafficSecurityRoster, log *slog.Logger) *trafficSecurityPeers {
	p := &trafficSecurityPeers{roster: roster, log: log}
	p.required.Store(true)
	p.refresh(ctx)
	return p
}

// Required reports whether every serving compute gateway stamps the snapshot.
func (p *trafficSecurityPeers) Required() bool { return p.required.Load() }

func (p *trafficSecurityPeers) run(ctx context.Context) {
	ticker := time.NewTicker(api.TrafficRuntimeObservationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refresh(ctx)
		}
	}
}

// refresh keeps the last decision when the roster cannot be read.
func (p *trafficSecurityPeers) refresh(ctx context.Context) {
	readCtx, cancel := context.WithTimeout(ctx, api.TrafficRuntimeObservationTimeout)
	defer cancel()
	rows, err := p.roster.ListServingGatewayTrafficRuntime(readCtx)
	if err != nil {
		p.log.Warn("gatewayd-public: compute gateway traffic security roster unavailable; keeping the last decision",
			"required", p.required.Load(), "err", err)
		return
	}
	required, legacy := allStampTrafficSecurity(rows)
	if p.required.Swap(required) == required {
		return
	}
	if required {
		p.log.Info("gatewayd-public: every compute gateway stamps the traffic security snapshot; requiring it", "gateways", len(rows))
		return
	}
	p.log.Warn("gatewayd-public: a compute gateway does not stamp the traffic security snapshot; accepting unstamped answers",
		"gateways", len(rows), "without_snapshot", legacy)
}

// allStampTrafficSecurity reports whether every serving gateway freshly
// reported security_revocation, and names the ones that did not. An empty
// roster (a single-host edge in front of its own gateway) requires it.
func allStampTrafficSecurity(rows []state.ServingGatewayTrafficRuntime) (bool, []string) {
	var legacy []string
	for _, row := range rows {
		fresh := row.Generation > 0 && !row.ReportedAt.IsZero() && !row.DatabaseNow.IsZero() &&
			!row.ReportedAt.After(row.DatabaseNow) &&
			!row.ReportedAt.Before(row.DatabaseNow.Add(-api.TrafficRuntimeObservationFreshness))
		if !fresh || !row.SecurityRevocation {
			legacy = append(legacy, row.NodeName)
		}
	}
	return len(legacy) == 0, legacy
}
