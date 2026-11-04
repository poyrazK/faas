// adr: 570
package main

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// trafficSecurityRoster reads the nodes this edge may dial and the wiring each
// compute gateway last reported (gateway_traffic_runtime_observations).
type trafficSecurityRoster interface {
	ActiveComputeNodes(context.Context) ([]state.ComputeNode, error)
	ListServingGatewayTrafficRuntime(context.Context) ([]state.ServingGatewayTrafficRuntime, error)
}

// trafficSecurityMembers names the gateways this edge may forward to. The
// database pool and a compute-node edge dial every active node with a gateway
// target (the compute gateway pool's own rule); a single-host or split-box edge
// dials one gateway, named by FAAS_NODE_NAME when the host has one.
type trafficSecurityMembers func([]state.ComputeNode) []string

func fleetTrafficSecurityMembers(nodes []state.ComputeNode) []string {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Active && node.GatewayTargetURL != nil && strings.TrimSpace(*node.GatewayTargetURL) != "" {
			names = append(names, node.Name)
		}
	}
	return names
}

func localTrafficSecurityMembers(nodeName string) trafficSecurityMembers {
	return func([]state.ComputeNode) []string {
		if nodeName = strings.TrimSpace(nodeName); nodeName == "" {
			return nil
		}
		return []string{nodeName}
	}
}

// trafficSecurityPeers decides whether the public edge may require the
// traffic security snapshot on compute gateway answers. Units release
// independently, so this edge runs beside N-1 compute gateways during a
// rollout (docs/release-compatibility.md). A gateway that predates the
// snapshot never stamps it and never reports security_revocation. While any
// gateway the edge may dial is such a peer, reports without the wiring, or has
// a stale or retired report (a node rolled back stops reporting), every answer
// passes unverified as before ADR-570, and a stamped value is dropped rather
// than trusted: an N-1 gateway does not strip guest-set X-Faas-* headers. On
// 2026-10-26 an rc.295 edge in front of rc.294 gateways refused every
// successful response as traffic_revocation_unavailable (10:39–12:14 UTC).
type trafficSecurityPeers struct {
	roster   trafficSecurityRoster
	members  trafficSecurityMembers
	log      *slog.Logger
	required atomic.Bool

	registry   *prometheus.Registry
	enforced   prometheus.Gauge
	legacy     prometheus.Gauge
	unverified prometheus.Counter
}

// newTrafficSecurityPeers reads the roster once before serving. Until a read
// succeeds the edge accepts unverified answers: the compute gateways still
// fence admission themselves, and refusing every success is an outage.
func newTrafficSecurityPeers(ctx context.Context, roster trafficSecurityRoster, members trafficSecurityMembers, log *slog.Logger) *trafficSecurityPeers {
	p := &trafficSecurityPeers{
		roster: roster, members: members, log: log,
		registry: prometheus.NewRegistry(),
		enforced: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gatewayd_public_traffic_security_enforced",
			Help: "1 while every compute gateway this edge may dial stamps the traffic security snapshot and the edge requires it; 0 while unstamped answers pass (ADR-570).",
		}),
		legacy: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gatewayd_public_traffic_security_legacy_gateways",
			Help: "Compute gateways this edge may dial that have no fresh traffic security wiring report (N-1, unwired, stale or retired) (ADR-570).",
		}),
		unverified: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gatewayd_public_traffic_security_unverified_total",
			Help: "Compute gateway answers this edge forwarded without verifying a traffic security snapshot because a gateway it may dial does not stamp one (ADR-570).",
		}),
	}
	p.registry.MustRegister(p.enforced, p.legacy, p.unverified)
	p.refresh(ctx)
	return p
}

// Required reports whether every compute gateway this edge may dial stamps
// the snapshot.
func (p *trafficSecurityPeers) Required() bool { return p.required.Load() }

// Unverified counts an answer forwarded without snapshot verification.
func (p *trafficSecurityPeers) Unverified() { p.unverified.Inc() }

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
	nodes, err := p.roster.ActiveComputeNodes(readCtx)
	var reports []state.ServingGatewayTrafficRuntime
	if err == nil {
		reports, err = p.roster.ListServingGatewayTrafficRuntime(readCtx)
	}
	if err != nil {
		p.log.Warn("gatewayd-public: compute gateway traffic security roster unavailable; keeping the last decision",
			"required", p.required.Load(), "err", err)
		return
	}
	members := p.members(nodes)
	required, legacy := allStampTrafficSecurity(members, reports)
	p.legacy.Set(float64(len(legacy)))
	if required {
		p.enforced.Set(1)
	} else {
		p.enforced.Set(0)
	}
	if p.required.Swap(required) == required {
		return
	}
	if required {
		p.log.Info("gatewayd-public: every compute gateway stamps the traffic security snapshot; requiring it", "gateways", len(members))
		return
	}
	p.log.Warn("gatewayd-public: a compute gateway does not stamp the traffic security snapshot; forwarding answers unverified",
		"gateways", len(members), "without_snapshot", legacy)
}

// allStampTrafficSecurity reports whether every member freshly reported
// security_revocation, and names the ones that did not. A member missing from
// the reports (an N-1 daemon, or a node the observation roster does not count)
// lacks it. No members means the edge cannot tell, so nothing is required.
func allStampTrafficSecurity(members []string, reports []state.ServingGatewayTrafficRuntime) (bool, []string) {
	var legacy []string
	for _, name := range members {
		i := slices.IndexFunc(reports, func(r state.ServingGatewayTrafficRuntime) bool { return r.NodeName == name })
		if i < 0 || !freshTrafficSecurity(reports[i]) {
			legacy = append(legacy, name)
		}
	}
	return len(members) > 0 && len(legacy) == 0, legacy
}

func freshTrafficSecurity(r state.ServingGatewayTrafficRuntime) bool {
	return r.SecurityRevocation && r.Generation > 0 && !r.ReportedAt.IsZero() && !r.DatabaseNow.IsZero() &&
		!r.ReportedAt.After(r.DatabaseNow) &&
		!r.ReportedAt.Before(r.DatabaseNow.Add(-api.TrafficRuntimeObservationFreshness))
}
