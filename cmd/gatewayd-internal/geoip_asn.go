package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/geoip"
)

// openASNReader opens the DB-IP ASN database behind the asn match field
// (ADR-966) and, with FAAS_GEOIP_AUTO_REFRESH=1, refreshes it on the same
// weekly cadence as the country database. Failure is logged and leaves
// the field absent; it never blocks boot.
func openASNReader(ctx context.Context, deps *runDeps, log *slog.Logger) {
	if geoipASNDBPath == "" {
		return
	}
	reader, err := geoip.Open(geoipASNDBPath, geoip.SourceDBIP, geoip.DBIPAttribution, log)
	if err != nil {
		log.Warn("geoip: ASN database unavailable; asn match conditions never match",
			"path", geoipASNDBPath, "err", err)
		return
	}
	deps.asnReader = reader
	log.Info("geoip: ASN database loaded", "path", geoipASNDBPath, "attribution", geoip.DBIPAttribution)
	if geoipAutoRefresh != "1" {
		return
	}
	watcher, err := geoip.NewWatcherForURL(reader, 168*time.Hour, geoip.DBIPASNDownloadURL, log)
	if err != nil {
		log.Warn("geoip: ASN watcher init failed; continuing without auto-refresh", "err", err)
		return
	}
	deps.asnWatcher = watcher
	if err := watcher.WatcherOnce(ctx); err != nil {
		log.Warn("geoip: ASN boot refresh failed; continuing with the on-disk file", "err", err)
	}
	watcher.Start(ctx)
}
