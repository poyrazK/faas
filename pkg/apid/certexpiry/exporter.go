// Package certexpiry exports the edge certificate expiries apid's domain
// doctor records, under the gateway_tls_cert_expiry_* names the
// FaasTLSCertExpiry* alerts read.
//
// Caddy, not a Go daemon, holds customer certificates, in storage no daemon
// reads (private keys sit beside them), and nothing in production set those
// metrics, so the alerts could never fire (bug hunt 32 M6). The doctor already
// completes a TLS handshake with every verified domain and stores the leaf's
// NotAfter in custom_domains.cert_expires_at; this collector reads that
// column at scrape time.
package certexpiry

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// KindOnDemand is the kind label of a customer certificate: the edge obtains
// every one on demand (ADR-520), wildcard children included.
const KindOnDemand = "ondemand"

// Exporter is a prometheus.Collector over the doctor's certificate expiries.
type Exporter struct {
	store state.CustomDomainCertExpiryStore
	now   func() time.Time
	log   *slog.Logger

	minDesc    *prometheus.Desc
	byHostDesc *prometheus.Desc

	mu       sync.Mutex
	cached   []state.CustomDomainCertExpiry
	cachedAt time.Time
}

// New returns an Exporter reading store.
func New(store state.CustomDomainCertExpiryStore, now func() time.Time, log *slog.Logger) *Exporter {
	return &Exporter{
		store: store, now: now, log: log,
		minDesc: prometheus.NewDesc("gateway_tls_cert_expiry_seconds",
			"Smallest remaining lifetime of the edge certificates apid's domain doctor observed on verified custom domains (custom_domains.cert_expires_at). Absent until a certificate is observed. Negative means a certificate is already past its NotAfter.",
			nil, nil),
		byHostDesc: prometheus.NewDesc("gateway_tls_cert_expiry_by_host_seconds",
			"Remaining lifetime of the edge certificate the domain doctor observed, for the soonest-expiring verified custom domains (at most CustomDomainCertExpiryExportMaxHosts). kind is ondemand: the edge obtains every customer certificate on demand.",
			[]string{"hostname", "kind"}, nil),
	}
}

// Describe implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.minDesc
	ch <- e.byHostDesc
}

// Collect implements prometheus.Collector. A failed read exports nothing, so
// a stale value can neither page nor hide an expiry.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	expiries := e.expiries()
	if len(expiries) == 0 {
		return
	}
	now := e.now()
	ch <- prometheus.MustNewConstMetric(e.minDesc, prometheus.GaugeValue, expiries[0].NotAfter.Sub(now).Seconds())
	for _, x := range expiries {
		ch <- prometheus.MustNewConstMetric(e.byHostDesc, prometheus.GaugeValue, x.NotAfter.Sub(now).Seconds(), x.Domain, KindOnDemand)
	}
}

func (e *Exporter) expiries() []state.CustomDomainCertExpiry {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.cachedAt.IsZero() && now.Sub(e.cachedAt) < api.CustomDomainCertExpiryExportCacheSeconds*time.Second {
		return e.cached
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	expiries, err := e.store.SoonestCustomDomainCertExpiries(ctx, api.CustomDomainCertExpiryExportMaxHosts)
	if err != nil {
		if e.log != nil {
			e.log.Warn("apid: certificate expiry export read failed", "err", err)
		}
		expiries = nil
	}
	e.cached, e.cachedAt = expiries, now
	return e.cached
}
