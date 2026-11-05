package edgecert

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/retry"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// Stored is one certificate as an edge reads it: its chain and its sealed
// key.
type Stored struct {
	ID        int64
	Names     []string
	ChainPEM  string
	KeySealed []byte
	NotBefore time.Time
	NotAfter  time.Time
}

// LiveStore returns, for each set of names, the newest certificate from ca
// that is valid now.
type LiveStore interface {
	Live(ctx context.Context, ca string) ([]Stored, error)
}

// Live is the LiveStore over Postgres.
func (s *PgStore) Live(ctx context.Context, ca string) ([]Stored, error) {
	rows, err := s.q.LiveEdgeTLSCertificates(ctx, s.pool, ca)
	if err != nil {
		return nil, fmt.Errorf("edgecert: read the live certificates: %w", err)
	}
	out := make([]Stored, 0, len(rows))
	for _, r := range rows {
		out = append(out, Stored{ID: r.ID, Names: r.Names, ChainPEM: r.ChainPem, KeySealed: r.KeySealed, NotBefore: r.NotBefore.Time, NotAfter: r.NotAfter.Time})
	}
	return out, nil
}

// Served is a compute edge's copy of the apps domains' certificates
// (ADR-20261103163954 §3). It opens each key with the edge-tls identity and
// keeps it in memory only, never in a file. A handshake reads only that
// copy, so no request reads Postgres, and the edge keeps serving what it
// has while Postgres is down.
type Served struct {
	ca         string
	identities []*age.X25519Identity
	store      LiveStore
	log        *slog.Logger
	now        func() time.Time

	set   atomic.Pointer[servedSet]
	loads *prometheus.CounterVec
}

type servedSet struct {
	certs []servedCert
}

type servedCert struct {
	id       int64
	names    []string
	cert     *tls.Certificate
	notAfter time.Time
}

// NewServed returns an empty copy of ca's certificates, opened with
// identities. Load fills it.
func NewServed(ca string, identities []*age.X25519Identity, store LiveStore, log *slog.Logger) (*Served, error) {
	if _, err := directoryFor(ca); err != nil {
		return nil, err
	}
	if len(identities) == 0 {
		return nil, errors.New("edgecert: no edge-tls identity to open the certificate key with")
	}
	return &Served{
		ca: ca, identities: identities, store: store, log: log, now: time.Now,
		loads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "edge_certificate_loads_total",
			Help: "Reads of the apps domains' certificates by this edge, by result: loaded (a new set is served), unchanged, kept (the read failed or found nothing, so the edge kept what it serves) (ADR-20261103163954 §3).",
		}, []string{"result"}),
	}, nil
}

// Loaded reports whether the edge has a certificate to serve.
func (s *Served) Loaded() bool {
	set := s.set.Load()
	return set != nil && len(set.certs) > 0
}

// Load reads the live certificates and serves them in place of the current
// ones. A failed read, or one that yields no certificate the edge can
// open, keeps the current ones: an outage or a bad row must not take the
// edge's certificate away. It reports whether the served set changed.
func (s *Served) Load(ctx context.Context) (bool, error) {
	rows, err := s.store.Live(ctx, s.ca)
	if err != nil {
		s.loads.WithLabelValues("kept").Inc()
		return false, err
	}
	next := &servedSet{}
	var failed []string
	for _, row := range rows {
		cert, err := s.open(row)
		if err != nil {
			failed = append(failed, fmt.Sprintf("certificate %d: %v", row.ID, err))
			continue
		}
		next.certs = append(next.certs, servedCert{id: row.ID, names: row.Names, cert: cert, notAfter: row.NotAfter})
	}
	if len(failed) > 0 {
		s.log.Warn("edgecert: certificates the edge could not open", "ca", s.ca, "errors", strings.Join(failed, "; "))
	}
	if len(next.certs) == 0 {
		s.loads.WithLabelValues("kept").Inc()
		if len(failed) > 0 {
			return false, errors.New("edgecert: no live certificate opened")
		}
		return false, fmt.Errorf("edgecert: no live certificate from %s", s.ca)
	}
	if current := s.set.Load(); current != nil && sameIDs(current, next) {
		s.loads.WithLabelValues("unchanged").Inc()
		return false, nil
	}
	s.set.Store(next)
	s.loads.WithLabelValues("loaded").Inc()
	for _, c := range next.certs {
		s.log.Info("edgecert: serving certificate", "id", c.id, "names", strings.Join(c.names, ","), "ca", s.ca, "not_after", c.notAfter)
	}
	return true, nil
}

// open builds the TLS certificate from row: its chain and its key, opened
// in memory and checked against the leaf.
func (s *Served) open(row Stored) (*tls.Certificate, error) {
	ns, der, err := secretbox.OpenBytesMulti(s.identities, row.KeySealed)
	if err != nil {
		return nil, fmt.Errorf("open the key: %w", err)
	}
	defer clear(der)
	if ns != KeyNamespace {
		return nil, fmt.Errorf("the key is sealed under %q, not %q", ns, KeyNamespace)
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse the key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("the key cannot sign")
	}
	cert := &tls.Certificate{PrivateKey: signer}
	rest := []byte(row.ChainPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			cert.Certificate = append(cert.Certificate, block.Bytes)
		}
	}
	if len(cert.Certificate) == 0 {
		return nil, errors.New("the chain holds no certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse the leaf: %w", err)
	}
	type publicKey interface{ Equal(crypto.PublicKey) bool }
	if pub, ok := leaf.PublicKey.(publicKey); !ok || !pub.Equal(signer.Public()) {
		return nil, errors.New("the key does not match the leaf")
	}
	cert.Leaf = leaf
	return cert, nil
}

// GetCertificate is the listener's tls.Config.GetCertificate: the served
// certificate covering the client's server name, the one that expires last
// when several do. Without one the handshake fails.
func (s *Served) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	set := s.set.Load()
	if set == nil {
		return nil, errors.New("edgecert: no certificate loaded")
	}
	name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	var best *servedCert
	for i := range set.certs {
		c := &set.certs[i]
		if covers(c.names, name) && (best == nil || c.notAfter.After(best.notAfter)) {
			best = c
		}
	}
	if best == nil {
		return nil, fmt.Errorf("edgecert: no certificate for %q", name)
	}
	return best.cert, nil
}

// covers reports whether names cover host: an exact name, or a *. wildcard
// one label above it.
func covers(names []string, host string) bool {
	for _, n := range names {
		if n == host {
			return true
		}
		if suffix, ok := strings.CutPrefix(n, "*."); ok {
			if label, rest, found := strings.Cut(host, "."); found && label != "" && rest == suffix {
				return true
			}
		}
	}
	return false
}

func sameIDs(a, b *servedSet) bool {
	if len(a.certs) != len(b.certs) {
		return false
	}
	for i := range a.certs {
		if a.certs[i].id != b.certs[i].id {
			return false
		}
	}
	return true
}

// Watch reloads on each NotifyChannel notice and every
// api.EdgeCertificateReloadInterval, until ctx ends. ready, when set, hears
// whether the edge has a certificate after each attempt. A lost LISTEN
// connection is retried after api.EdgeCertificateListenRetry; the timed
// reload covers notices missed meanwhile.
func (s *Served) Watch(ctx context.Context, pool *pgxpool.Pool, ready func(bool)) {
	notices := make(chan struct{}, 1)
	go s.listen(ctx, pool, notices)
	ticker := time.NewTicker(api.EdgeCertificateReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-notices:
		case <-ticker.C:
		}
		if _, err := s.Load(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("edgecert: reload; keeping the served certificates", "err", err)
		}
		if ready != nil {
			ready(s.Loaded())
		}
	}
}

func (s *Served) listen(ctx context.Context, pool *pgxpool.Pool, notices chan<- struct{}) {
	for attempt := 0; ctx.Err() == nil; attempt++ {
		if err := retry.Wait(ctx, api.EdgeCertificateListenRetry, 30*time.Second, attempt); err != nil {
			return
		}
		if err := s.listenOnce(ctx, pool, notices); err != nil && ctx.Err() == nil {
			s.log.Warn("edgecert: listen for new certificates", "err", err)
		}
	}
}

func (s *Served) listenOnce(ctx context.Context, pool *pgxpool.Pool, notices chan<- struct{}) error {
	// The shared hub uses the direct session pool for LISTEN, leaving ordinary
	// query slots free for requests. Resync covers lost notices.
	changes, err := db.SubscribeWithResync(ctx, pool, []string{NotifyChannel}, s.log)
	if err != nil {
		return err
	}
	// A certificate stored while the edge was not listening.
	select {
	case notices <- struct{}{}:
	default:
	}
	for range changes {
		select {
		case notices <- struct{}{}:
		default:
		}
	}
	return ctx.Err()
}

// Collectors are the edge's certificate series: the loads counter and
// edge_certificate_expiry_seconds{name}, the time left on the certificate
// served for each name, read at scrape time.
func (s *Served) Collectors() []prometheus.Collector {
	return []prometheus.Collector{s.loads, expiryCollector{s}}
}

var expiryDesc = prometheus.NewDesc("edge_certificate_expiry_seconds",
	"Seconds until the apps-domain certificate this edge serves for name expires; edgednsd renews 30 days ahead (ADR-20261103163954 §3).",
	[]string{"name"}, nil)

type expiryCollector struct{ s *Served }

func (c expiryCollector) Describe(ch chan<- *prometheus.Desc) { ch <- expiryDesc }

func (c expiryCollector) Collect(ch chan<- prometheus.Metric) {
	set := c.s.set.Load()
	if set == nil {
		return
	}
	now := c.s.now()
	for _, cert := range set.certs {
		for _, name := range cert.names {
			ch <- prometheus.MustNewConstMetric(expiryDesc, prometheus.GaugeValue, cert.notAfter.Sub(now).Seconds(), name)
		}
	}
}
