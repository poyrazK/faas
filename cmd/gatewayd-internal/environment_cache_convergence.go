package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type environmentCacheCaptureKey struct{}
type environmentCacheCapture struct {
	AppID, EnvironmentID string
	Revision             int64
	RuleCount            int
}

// A direct compile avoids the request loader's last-known fallback. Evidence
// is published only for the current generation, after cache installation.
func (g *gatewaydEdgeRules) observeEnvironmentCache(ctx context.Context, p state.GatewayEnvironmentCachePolicy) error {
	host := gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, p.EnvironmentID, p.AppID)
	g.cache.InvalidateHosts([]string{host})
	generation := g.cache.Generation()
	capture := &environmentCacheCapture{}
	ctx = context.WithValue(gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, p.AccountID), p.AppID), environmentCacheCaptureKey{}, capture)
	entry, err := g.loadHostUncached(ctx, host)
	if err != nil {
		return err
	}
	if capture.AppID != p.AppID || capture.EnvironmentID != p.EnvironmentID || capture.Revision != p.Revision || len(entry.Cache) != capture.RuleCount {
		return state.ErrConflict
	}
	cached, ok := g.cache.GetHost(gateway.EdgeRuleCacheKey(host, p.AppID))
	if !ok || g.cache.Generation() != generation || len(cached.Cache) != len(entry.Cache) {
		return state.ErrConflict
	}
	for i := range cached.Cache {
		if cached.Cache[i].ID != entry.Cache[i].ID {
			return state.ErrConflict
		}
	}
	return nil
}

func reconcileEnvironmentCachePage(ctx context.Context, store state.EnvironmentCacheGatewayObserver, g *gatewaydEdgeRules, node, session, after string) (string, error) {
	if err := store.RefreshEnvironmentCacheGatewaySession(ctx, node, session); err != nil {
		return after, err
	}
	rows, err := store.ListEnvironmentCacheGatewayPolicies(ctx, node, session, after)
	if err != nil {
		return after, err
	}
	var failures []error
	for _, p := range rows {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		if err := g.observeEnvironmentCache(ctx, p); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := store.RecordEnvironmentCacheGatewayObserved(ctx, node, session, p.AppID, p.EnvironmentID, p.Revision); err != nil {
			failures = append(failures, err)
		}
	}

	if len(rows) < state.EnvironmentCacheObservationBatchSize {
		return "", errors.Join(failures...)
	}
	return rows[len(rows)-1].Key(), errors.Join(failures...)
}

func watchEnvironmentCacheConvergence(ctx context.Context, store state.EnvironmentCacheGatewayObserver, g *gatewaydEdgeRules, node string, log *slog.Logger, caches ...*gateway.ResponseCache) {
	node = strings.TrimSpace(node)
	if node == "" || store == nil || g == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	session, started := uuid.NewString(), time.Now().UTC()
	registered := false
	after := ""
	purgeCursor := int64(0)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		runCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		var err error
		if !registered {
			err = store.BeginEnvironmentCacheGatewaySession(runCtx, node, session, started)
			registered = err == nil
		}
		if registered && err == nil {
			after, err = reconcileEnvironmentCachePage(runCtx, store, g, node, session, after)
			if len(caches) > 0 && caches[0] != nil {
				if purges, ok := store.(state.EnvironmentCachePurgeObserver); ok {
					err = errors.Join(err, reconcileEnvironmentCachePurges(runCtx, purges, caches[0], node, session, &purgeCursor))
				}
			}
		}
		cancel()
		if err != nil {
			log.Warn("gatewayd: environment cache observation pending", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
