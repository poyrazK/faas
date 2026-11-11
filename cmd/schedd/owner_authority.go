// adr: 20261110210000
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
)

// ownerAuthority is FAAS_APP_OWNER_AUTHORITY (ADR-20261110210000 §4): who
// assigns app owners.
//
//	any (default)  every schedd claims unplaced apps, pulls orphaned apps onto
//	               its own node and moves pressured apps (today's behaviour)
//	control-plane  only the control plane's schedd assigns owners: it claims
//	               unplaced apps and recovers orphaned ones with
//	               Engine.RebalanceOrphanedAppsCentral; a compute node's schedd
//	               wakes the apps it owns and never writes apps.node_id. A
//	               pressured node files a release request instead; the
//	               control plane moves the app and picks the destination.
//
// Pair control-plane with app_owner_authority.mode = observe, then enforce,
// so Postgres refuses an owner change from any compute login regardless of
// what the code on a node does.
type ownerAuthority string

const (
	ownerAuthorityAny          ownerAuthority = "any"
	ownerAuthorityControlPlane ownerAuthority = "control-plane"
)

func ownerAuthorityFromEnv() (ownerAuthority, error) {
	switch v := ownerAuthority(os.Getenv("FAAS_APP_OWNER_AUTHORITY")); v {
	case "", ownerAuthorityAny:
		return ownerAuthorityAny, nil
	case ownerAuthorityControlPlane:
		return v, nil
	default:
		return "", fmt.Errorf("FAAS_APP_OWNER_AUTHORITY is %q; want any or control-plane", v)
	}
}

// claimsOwners reports whether a schedd of role r claims unplaced apps.
func (a ownerAuthority) claimsOwners(r role.Role) bool {
	return a != ownerAuthorityControlPlane || r != role.RoleComputeOnly
}

// recoversOntoOwnNode reports whether a node-bound schedd of role r pulls
// orphaned apps onto its own node and moves pressured apps.
func (a ownerAuthority) recoversOntoOwnNode(r role.Role) bool {
	return a != ownerAuthorityControlPlane || r == role.RoleSingleBox
}

// recoversCentrally reports whether a schedd of role r runs the control
// plane's ownership recovery.
func (a ownerAuthority) recoversCentrally(r role.Role) bool {
	return a == ownerAuthorityControlPlane && r == role.RoleControlPlane
}

// ownerReleaseInterval is how often the control plane's schedd processes
// pressured nodes' release requests.
const ownerReleaseInterval = 15 * time.Second

// ownerAuthorityWindow is how far back faas_app_owner_unauthorized_writes
// counts; the alert fires on any non-zero value.
const ownerAuthorityWindow = 15 * time.Minute

// runOwnerAuthorityMetrics publishes the trigger's mode and recent
// unauthorized owner writes every interval until ctx ends.
func runOwnerAuthorityMetrics(ctx context.Context, reg prometheus.Registerer, store state.AppOwnerAuthorityReader, interval time.Duration, log *slog.Logger) {
	mode := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "faas_app_owner_authority_mode",
		Help: "1 for the app_owner_authority mode in force (any, observe, enforce), ADR-20261110210000.",
	}, []string{"mode"})
	unauthorized := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "faas_app_owner_unauthorized_writes",
		Help: "Owner changes to apps.node_id in the last 15 minutes from a login outside faas_owner_authority (recorded in observe mode).",
	})
	for _, c := range []prometheus.Collector{mode, unauthorized} {
		if err := reg.Register(c); err != nil {
			log.Warn("schedd: owner authority metrics: register", "err", err)
			return
		}
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		status, err := store.AppOwnerAuthorityStatus(readCtx, ownerAuthorityWindow)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("schedd: owner authority metrics: read", "err", err)
			}
		} else {
			mode.Reset()
			mode.WithLabelValues(status.Mode).Set(1)
			unauthorized.Set(float64(status.Unauthorized))
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
