package main

// Fleet-wide observed-route collection for the customer routes and automatic
// OpenAPI surfaces. Production Prometheus already discovers every active
// compute gateway from the compute-node registry and attaches the stable
// node_id label. Reading that local, authenticated aggregate avoids opening
// gatewayd-internal's loopback-only control listener across the network while
// still retaining enough scrape health to distinguish complete, partial, and
// unavailable observations.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/promql"
)

const observedRoutesOther = "__route_other__"

type observedRoutesSnapshot struct {
	Routes             []string
	Source             string
	CollectorsExpected int
	CollectorsHealthy  int
	CapHit             bool
}

func unavailableObservedRoutes(expected int) observedRoutesSnapshot {
	return observedRoutesSnapshot{
		Routes:             []string{},
		Source:             api.AppRoutesSourceUnavailable,
		CollectorsExpected: expected,
	}
}

func (o observedRoutesSnapshot) available() bool {
	return o.Source == api.AppRoutesSourceLive || o.Source == api.AppRoutesSourcePartial
}

func (o observedRoutesSnapshot) rows() []openapidiff.RouteRow {
	rows := make([]openapidiff.RouteRow, 0, len(o.Routes))
	for _, route := range o.Routes {
		rows = append(rows, openapidiff.RouteRow{Route: route})
	}
	return rows
}

// cacheSHA includes collector coverage as well as the route union. A node
// becoming unavailable may leave the same route labels behind in Prometheus
// for a short staleness window; including coverage prevents a cached "live"
// automatic document from masking that transition (and vice versa).
func (o observedRoutesSnapshot) cacheSHA() [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(o.Source))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.Itoa(o.CollectorsExpected)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.Itoa(o.CollectorsHealthy)))
	for _, route := range o.Routes {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(route))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// collectObservedRoutes uses the fleet aggregate whenever Prometheus is
// configured. The single gateway control URL remains as a development and
// single-box fallback; production must never fall back to it after a fleet
// query failure because that would silently turn incomplete data into live
// data.
func (s *server) collectObservedRoutes(ctx context.Context, appID, slug string) observedRoutesSnapshot {
	if s.promqlClient != nil {
		return s.collectFleetObservedRoutes(ctx, appID)
	}
	return s.collectLegacyObservedRoutes(ctx, appID, slug)
}

func (s *server) collectFleetObservedRoutes(ctx context.Context, appID string) observedRoutesSnapshot {
	nodes, err := s.store.ListComputeNodes(ctx, false)
	if err != nil {
		if s.log != nil {
			s.log.Warn("observed routes compute registry read failed", "err", err)
		}
		return unavailableObservedRoutes(0)
	}
	expected := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		// This matches the Prometheus HTTP-SD eligibility contract. An active
		// legacy/synthetic row without a gateway endpoint is not a route
		// collector and therefore must not lower fleet coverage.
		if node.Active && node.GatewayTargetURL != nil && strings.TrimSpace(node.ID) != "" {
			expected[node.ID] = struct{}{}
		}
	}
	if len(expected) == 0 {
		return unavailableObservedRoutes(0)
	}

	queryCtx, cancel := context.WithTimeout(ctx, routesDialTimeout)
	defer cancel()
	routeQuery := fmt.Sprintf(`max by (node_id, route) (gateway_requests_by_route_total{app=%s})`, strconv.Quote(appID))
	healthQuery := `max by (node_id) (up{job="gatewayd-internal"})`

	var routeSamples, healthSamples []promql.VectorSample
	var routeErr, healthErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		routeSamples, routeErr = s.promqlClient.QueryVector(queryCtx, routeQuery)
	}()
	go func() {
		defer wg.Done()
		healthSamples, healthErr = s.promqlClient.QueryVector(queryCtx, healthQuery)
	}()
	wg.Wait()
	if routeErr != nil || healthErr != nil {
		if s.log != nil {
			s.log.Warn("fleet observed routes query failed", "routes_err", routeErr, "health_err", healthErr)
		}
		return unavailableObservedRoutes(len(expected))
	}

	healthyIDs := make(map[string]struct{}, len(healthSamples))
	for _, sample := range healthSamples {
		id := strings.TrimSpace(sample.Labels["node_id"])
		if sample.Value == 1 {
			if _, ok := expected[id]; ok {
				healthyIDs[id] = struct{}{}
			}
		}
	}
	if len(healthyIDs) == 0 {
		// Route counter series can remain queryable briefly after every
		// collector goes down. Do not advertise those stale rows as current.
		return unavailableObservedRoutes(len(expected))
	}

	routeSet := make(map[string]struct{}, len(routeSamples))
	capHit := false
	for _, sample := range routeSamples {
		if _, ok := healthyIDs[strings.TrimSpace(sample.Labels["node_id"])]; !ok {
			// Ignore stale series from a drained/replaced node and any series
			// missing the registry identity attached by HTTP-SD.
			continue
		}
		route := strings.TrimSpace(sample.Labels["route"])
		switch route {
		case "":
			continue
		case observedRoutesOther:
			capHit = true
			continue
		default:
			routeSet[route] = struct{}{}
		}
	}
	routes := make([]string, 0, len(routeSet)+1)
	for route := range routeSet {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	if len(routes) >= api.RouteMetricsPerAppCap {
		capHit = true
	}
	if len(routes) > api.RouteMetricsPerAppCap {
		routes = routes[:api.RouteMetricsPerAppCap]
	}
	if capHit {
		routes = append(routes, observedRoutesOther)
	}

	source := api.AppRoutesSourceLive
	if len(healthyIDs) < len(expected) {
		source = api.AppRoutesSourcePartial
	}
	return observedRoutesSnapshot{
		Routes:             routes,
		Source:             source,
		CollectorsExpected: len(expected),
		CollectorsHealthy:  len(healthyIDs),
		CapHit:             capHit,
	}
}

func (s *server) collectLegacyObservedRoutes(ctx context.Context, appID, slug string) observedRoutesSnapshot {
	if s.gatewaydControlURL == "" {
		return unavailableObservedRoutes(0)
	}
	dialCtx, cancel := context.WithTimeout(ctx, routesDialTimeout)
	defer cancel()
	endpoint, err := url.JoinPath(s.gatewaydControlURL, "v1", "internal", "apps", slug, "routes")
	if err != nil {
		return unavailableObservedRoutes(1)
	}
	req, err := http.NewRequestWithContext(dialCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return unavailableObservedRoutes(1)
	}
	resp, err := (&http.Client{Timeout: routesDialTimeout}).Do(req)
	if err != nil {
		if s.log != nil {
			s.log.Debug("apid to gatewayd observed-routes dial failed", "err", err, "url", endpoint)
		}
		return unavailableObservedRoutes(1)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return unavailableObservedRoutes(1)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return unavailableObservedRoutes(1)
	}
	var upstream routesUpstreamResponse
	if err := json.Unmarshal(body, &upstream); err != nil {
		return unavailableObservedRoutes(1)
	}
	if upstream.Routes == nil {
		upstream.Routes = []string{}
	}
	// Trust the app already authorized by apid, not an optional identity in
	// the unauthenticated loopback response.
	_ = appID
	return observedRoutesSnapshot{
		Routes:             upstream.Routes,
		Source:             api.AppRoutesSourceLive,
		CollectorsExpected: 1,
		CollectorsHealthy:  1,
		CapHit:             upstream.CapHit,
	}
}
