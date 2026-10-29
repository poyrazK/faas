package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/apid/crawlerranges"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// runEdgeCrawlerRangesRefresher keeps crawler operators' published IP
// ranges current for the gateways (ADR-20261022083601).
// FAAS_EDGE_CRAWLER_RANGES_REFRESH=0 turns it off on installs without
// outbound internet; gateways then verify search crawlers by DNS only and
// never verify AI crawlers.
func (s *server) runEdgeCrawlerRangesRefresher(ctx context.Context) {
	if envOr("FAAS_EDGE_CRAWLER_RANGES_REFRESH", "1") == "0" {
		return
	}
	store, ok := s.store.(state.EdgeCrawlerRangesStore)
	if !ok {
		s.log.Error("edge crawler ranges store unavailable; AI crawlers will not verify")
		return
	}
	refresher := crawlerranges.Refresher{Store: store, Logger: s.log}
	if s.ops != nil {
		refreshes := promauto.With(s.ops.Registry()).NewCounterVec(prometheus.CounterOpts{
			Name: "edge_crawler_ranges_refresh_total",
			Help: "Crawler IP-range feed refreshes by crawler and outcome (ok, fresh, fetch_error, invalid, store_error).",
		}, []string{"crawler", "outcome"})
		refresher.Observe = func(crawler, outcome string) { refreshes.WithLabelValues(crawler, outcome).Inc() }
	}
	if err := refresher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("edge crawler ranges refresher exited", "error", err)
	}
}
