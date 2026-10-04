// adr: 531
package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrTargetReadinessUnavailable = errors.New("gateway: target traffic readiness unavailable")

// RecordTargetWithReadiness keeps admitted capacity even when the scoped read
// fails or probes are not ready. Bare publications cannot certify configuration.
func (b *PGBackend) RecordTargetWithReadiness(ctx context.Context, appID string, target Target) error {
	if b == nil || appID == "" || target.InstanceID == "" || target.NodeID == "" || (target.AppID != "" && target.AppID != appID) {
		return fmt.Errorf("publish target identity: %w", ErrTargetReadinessUnavailable)
	}
	target.AppID = appID
	err := b.loadAdmissionReadiness(ctx, &target)
	b.RecordTarget(appID, target)
	return err
}

// VerifyTargetReadiness also covers direct mirror forwarding, which must not
// register a dedicated mirror VM in the ordinary public placement cache.
func (b *PGBackend) VerifyTargetReadiness(ctx context.Context, target Target) (Target, error) {
	if b == nil || target.AppID == "" || target.InstanceID == "" || target.NodeID == "" {
		return target, ErrTargetReadinessUnavailable
	}
	if err := b.loadAdmissionReadiness(ctx, &target); err != nil {
		return target, err
	}
	b.tgtMu.Lock()
	defer b.tgtMu.Unlock()
	b.mergeTargetReadinessLocked(&target)
	if b.targetQuarantinedLocked(target.AppID, target, time.Now()) || !target.routeReady() {
		return target, ErrTargetReadinessUnavailable
	}
	return target, nil
}

type targetReadinessIdentity struct {
	app, instance, deployment, node, wake string
	port                                  int
}

type targetReadinessConfiguration struct {
	identity targetReadinessIdentity
	sources  []string
}

func readinessIdentity(target Target) targetReadinessIdentity {
	port := target.Port
	if port == 0 {
		port = api.DefaultAppPort
	}
	return targetReadinessIdentity{target.AppID, target.InstanceID, target.DeploymentID, target.NodeID, target.WakeID, port}
}

func (target Target) hasReadinessConfiguration() bool {
	config := target.readinessConfiguration
	if config == nil || config.identity != readinessIdentity(target) || target.RequiresReadiness != (len(config.sources) > 0) {
		return false
	}
	var sources []string
	if target.ReadinessGates != nil {
		sources = target.ReadinessGates.RequiredSources
	}
	return slices.Equal(config.sources, sources)
}

func bareTargetReadiness(target Target) bool {
	return !target.hasReadinessConfiguration() && !target.RequiresReadiness && target.ReadinessGates == nil && !target.Ready &&
		target.ReadinessUpdatedAt.IsZero() && target.ReadinessEventID == 0 && !target.ReadinessUnavailable && target.ReadinessVerifiedUntil.IsZero()
}

// A notification/scheduler replay may omit readiness fields. Carry forward
// only an exact lifetime's existing certificate, failure and expiry.
func (b *PGBackend) inheritTargetReadinessLocked(appID string, target *Target) {
	if !bareTargetReadiness(*target) {
		return
	}
	picker := b.appsPicker[appID]
	if picker == nil {
		return
	}
	for _, set := range picker.sets {
		for _, old := range set.entries {
			if !sameTargetPlacement(old, *target) || !old.hasReadinessConfiguration() {
				continue
			}
			target.readinessConfiguration = old.readinessConfiguration
			target.RequiresReadiness, target.Ready = old.RequiresReadiness, old.Ready
			target.ReadinessGates = cloneReadinessGates(old.ReadinessGates)
			target.ReadinessUpdatedAt, target.ReadinessEventID = old.ReadinessUpdatedAt, old.ReadinessEventID
			target.ReadinessUnavailable, target.ReadinessVerifiedUntil = old.ReadinessUnavailable, old.ReadinessVerifiedUntil
			return
		}
	}
}

// Notifications can precede discovery of a source's configuration. Merge the
// retained lifetime-scoped observation after certifying the complete source set.
func (b *PGBackend) applyTargetReadinessLocked(target *Target, snapshot TargetReadinessSnapshot, now time.Time) bool {
	if !applyTargetReadiness(target, snapshot, now) {
		return false
	}
	b.mergeTargetReadinessLocked(target)
	return true
}

func (b *PGBackend) mergeTargetReadinessLocked(target *Target) {
	if target.ReadinessGates != nil {
		for _, source := range target.ReadinessGates.RequiredSources {
			if state, exists := b.readinessState[readinessLifetimeStateKey(target.AppID, target.InstanceID, target.WakeID, target.NodeID, source)]; exists {
				applyReadinessState(target, source, state)
			}
		}
	}
	target.Ready = target.routeReady()
}
