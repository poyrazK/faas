package edgecert

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
)

// Manager keeps one certificate for Names current: it orders the first one,
// renews it api.EdgeCertificateRenewBefore its expiry, and orders a new one
// when the edge-tls recipient changes.
type Manager struct {
	Names     []string
	Recipient *age.X25519Recipient
	Orderer   Orderer
	Store     Store
	Metrics   *Metrics
	Log       *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	failures int
	retryAt  time.Time
	// pending is an issued certificate the store has not taken yet. It is
	// stored before anything is ordered again: an order is scarce (5 for
	// the same names in 7 days) and the key in it is already sealed.
	pending *Certificate
}

// Run checks at once and then when each check says, until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
		wait := m.Step(ctx)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Step makes one check and returns how long to wait before the next.
func (m *Manager) Step(ctx context.Context) time.Duration {
	now := m.now()
	if m.pending != nil {
		if !m.store(ctx, *m.pending) {
			return api.EdgeCertificateRetryMin
		}
		m.pending = nil
	}
	if now.Before(m.retryAt) {
		return m.retryAt.Sub(now)
	}
	ca := m.Orderer.CA()
	terms, found, err := m.Store.NewestTerms(ctx, m.Names, ca)
	if err != nil {
		m.Log.Warn("edgecert: read the newest certificate", "err", err)
		return api.EdgeCertificateRetryMin
	}
	var newest *Terms
	if found {
		newest = &terms
		m.Metrics.stored(ca, terms.NotAfter)
		m.Metrics.accountURI(ca, terms.AccountURI)
	}
	m.prune(ctx, now)
	due, reason := Due(newest, m.Recipient.String(), now)
	if !due {
		return api.EdgeCertificateCheckInterval
	}
	m.Log.Info("edgecert: ordering a certificate", "names", strings.Join(m.Names, ","), "ca", ca, "reason", reason)
	orderCtx, cancel := context.WithTimeout(ctx, api.EdgeCertificateOrderTimeout)
	cert, err := Issue(orderCtx, m.Orderer, m.Names, m.Recipient)
	cancel()
	if err != nil {
		m.failures++
		wait := min(api.EdgeCertificateRetryMin<<min(m.failures-1, 16), api.EdgeCertificateRetryMax)
		m.retryAt = now.Add(wait)
		m.Metrics.ordered(ca, "failed")
		m.Log.Warn("edgecert: certificate order failed", "names", strings.Join(m.Names, ","), "ca", ca, "failures", m.failures, "retry_in", wait, "err", err)
		return wait
	}
	m.failures, m.retryAt = 0, time.Time{}
	m.Metrics.ordered(ca, "issued")
	m.Metrics.accountURI(ca, cert.AccountURI)
	if !m.store(ctx, cert) {
		m.pending = &cert
		return api.EdgeCertificateRetryMin
	}
	return api.EdgeCertificateCheckInterval
}

// store writes cert and reports whether it is stored.
func (m *Manager) store(ctx context.Context, cert Certificate) bool {
	id, err := m.Store.Insert(ctx, cert)
	if err != nil {
		m.Log.Warn("edgecert: store the certificate; keeping it to store again", "sha256", cert.SHA256, "err", err)
		return false
	}
	m.Metrics.stored(cert.CA, cert.NotAfter)
	m.Log.Info("edgecert: certificate stored", "id", id, "names", strings.Join(cert.Names, ","), "ca", cert.CA,
		"serial", cert.Serial, "sha256", cert.SHA256, "not_after", cert.NotAfter, "chain", pemCertificates(cert.ChainPEM), "account_uri", cert.AccountURI)
	return true
}

// prune drops certificates long expired; a failure waits for the next check.
func (m *Manager) prune(ctx context.Context, now time.Time) {
	n, err := m.Store.DeleteExpiredBefore(ctx, now.Add(-api.EdgeCertificateKeepAfterExpiry))
	if err != nil {
		m.Log.Warn("edgecert: drop expired certificates", "err", err)
		return
	}
	if n > 0 {
		m.Log.Info("edgecert: dropped expired certificates", "count", n)
	}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
