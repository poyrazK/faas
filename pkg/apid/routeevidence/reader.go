// Package routeevidence reads the gateway-measured evidence for opt-in route
// features (ADR-1152): the edges the service wake-ahead learner holds for a
// caller app and what wake-ahead did along them, and the route priority
// queue's outcomes per class. The gateways publish both as Prometheus series;
// apid reads the fleet aggregate the same way observed routes are read, so no
// gateway control listener is opened across the network. Without Prometheus
// the evidence is reported as unavailable.
package routeevidence

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

const queryTimeout = 5 * time.Second

// Querier is the Prometheus surface the reader needs; *promql.Client
// satisfies it.
type Querier interface {
	QueryVector(ctx context.Context, query string) ([]promql.VectorSample, error)
}

// AppLookup resolves measured target apps; state.Store satisfies it.
type AppLookup interface {
	AppByID(ctx context.Context, id string) (state.App, error)
}

// Reader reads route feature evidence. A nil Prom reports every block as
// unavailable.
type Reader struct {
	Prom Querier
	Apps AppLookup
}

// Window is the outcome window as shown to customers, for example "24h".
func Window() string {
	return strconv.Itoa(int(api.RouteEvidenceWindow.Hours())) + "h"
}

func (r Reader) query(ctx context.Context, query string) ([]promql.VectorSample, error) {
	if r.Prom == nil {
		return nil, errors.New("routeevidence: prometheus not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	return r.Prom.QueryVector(ctx, query)
}

// WakeAheadLearned reads the measured edges of a caller app. Targets outside
// the caller's account are never shown.
func (r Reader) WakeAheadLearned(ctx context.Context, acct state.Account, app state.App) api.ServiceWakeAheadLearned {
	out := api.ServiceWakeAheadLearned{Source: api.RouteEvidenceSourceUnavailable, Window: Window(), Targets: []api.ServiceWakeAheadTarget{}}
	caller := strconv.Quote(app.ID)
	shares, err := r.query(ctx, fmt.Sprintf(`max by (target_app, kind) (gateway_service_wake_ahead_edge_share{caller_app=%s})`, caller))
	if err != nil {
		return out
	}
	wakes, err := r.query(ctx, fmt.Sprintf(`max(gateway_service_wake_ahead_caller_wakes{caller_app=%s})`, caller))
	if err != nil {
		return out
	}
	outcomes, err := r.query(ctx, fmt.Sprintf(`sum by (target_app, outcome) (increase(gateway_service_wake_ahead_edge_total{caller_app=%s}[%s]))`, caller, Window()))
	if err != nil {
		return out
	}
	out.Source = api.RouteEvidenceSourceLive
	if len(wakes) > 0 {
		out.CallerWakes = int64(math.Round(wakes[0].Value))
	}
	targets := collectTargets(shares, outcomes)
	for id, t := range targets {
		if id == "" || r.Apps == nil {
			continue
		}
		peer, err := r.Apps.AppByID(ctx, id)
		if err != nil || peer.AccountID != acct.ID {
			continue
		}
		t.Slug = peer.Slug
		t.Qualifies = out.CallerWakes >= api.ServiceWakeAheadMinWakes && t.CallShare >= api.ServiceWakeAheadMinCallShare && t.ColdShare >= api.ServiceWakeAheadMinColdShare
		out.Targets = append(out.Targets, *t)
	}
	slices.SortFunc(out.Targets, func(a, b api.ServiceWakeAheadTarget) int {
		if a.CallShare != b.CallShare {
			if a.CallShare > b.CallShare {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Slug, b.Slug)
	})
	return out
}

// collectTargets folds the share and outcome samples into one entry per
// target app id.
func collectTargets(shares, outcomes []promql.VectorSample) map[string]*api.ServiceWakeAheadTarget {
	targets := map[string]*api.ServiceWakeAheadTarget{}
	target := func(id string) *api.ServiceWakeAheadTarget {
		if t, ok := targets[id]; ok {
			return t
		}
		t := &api.ServiceWakeAheadTarget{AppID: id}
		targets[id] = t
		return t
	}
	for _, sample := range shares {
		t := target(sample.Labels["target_app"])
		switch sample.Labels["kind"] {
		case "call":
			t.CallShare = sample.Value
		case "cold":
			t.ColdShare = sample.Value
		}
	}
	for _, sample := range outcomes {
		t, n := target(sample.Labels["target_app"]), int64(math.Round(sample.Value))
		switch sample.Labels["outcome"] {
		case "started":
			t.Started = n
		case "used":
			t.Used = n
		case "unused":
			t.Unused = n
		case "failed":
			t.Failed = n
		}
	}
	return targets
}

// PriorityQueue reads the app's warm-capacity queue outcomes per class.
func (r Reader) PriorityQueue(ctx context.Context, app state.App) api.RoutePriorityQueueStats {
	out := api.RoutePriorityQueueStats{Source: api.RouteEvidenceSourceUnavailable, Window: Window(), Classes: []api.RoutePriorityQueueClass{}}
	samples, err := r.query(ctx, fmt.Sprintf(`sum by (class, outcome) (increase(gateway_route_priority_queue_total{app=%s}[%s]))`, strconv.Quote(app.ID), Window()))
	if err != nil {
		return out
	}
	out.Source = api.RouteEvidenceSourceLive
	classes := []api.RoutePriorityQueueClass{{Class: api.RoutePriorityCritical}, {Class: "normal"}, {Class: api.RoutePriorityBulk}}
	for _, sample := range samples {
		i := slices.IndexFunc(classes, func(c api.RoutePriorityQueueClass) bool { return c.Class == sample.Labels["class"] })
		if i < 0 {
			continue
		}
		n := int64(math.Round(sample.Value))
		switch sample.Labels["outcome"] {
		case "queued":
			classes[i].Queued = n
		case "displacing":
			classes[i].Displacing = n
		case "displaced":
			classes[i].Displaced = n
		case "rejected":
			classes[i].Rejected = n
		}
	}
	out.Classes = classes
	return out
}
