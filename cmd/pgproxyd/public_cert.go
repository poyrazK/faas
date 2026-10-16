package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// publicCertCheck is how often pgproxyd looks for a renewed certificate.
const publicCertCheck = 30 * time.Second

// publicCert serves pgproxyd's *.<host_suffix> certificate and reloads it
// when its files change. A renewal then reaches new handshakes without a
// restart, which would drop every client's session. A file that does not
// load (a chain without its new key yet, a bad file) keeps the certificate
// in use, and the next check retries.
type publicCert struct {
	certPath, keyPath string
	log               *slog.Logger
	now               func() time.Time

	cur atomic.Pointer[tls.Certificate]
	mu  sync.Mutex // serializes reloads
	// stamp identifies the files' contents at the last successful load.
	stamp   string
	reloads *prometheus.CounterVec
}

func loadPublicCert(certPath, keyPath string, reg prometheus.Registerer, log *slog.Logger) (*publicCert, error) {
	c := &publicCert{certPath: certPath, keyPath: keyPath, log: log, now: time.Now,
		reloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pgproxyd_tls_cert_reloads_total",
			Help: "Reloads of the public certificate after its files changed, by result (ok, failed). A failed reload keeps the certificate in use.",
		}, []string{"result"}),
	}
	if _, err := c.reload(); err != nil {
		return nil, err
	}
	expiry := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "pgproxyd_tls_cert_expiry_seconds",
		Help: "Seconds until the public *.<host_suffix> certificate in use expires.",
	}, func() float64 { return c.notAfter().Sub(c.now()).Seconds() })
	reg.MustRegister(c.reloads, expiry)
	return c, nil
}

// GetCertificate is the tls.Config hook: every handshake reads the
// certificate in use.
func (c *publicCert) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return c.cur.Load(), nil
}

func (c *publicCert) notAfter() time.Time {
	if cert := c.cur.Load(); cert != nil && cert.Leaf != nil {
		return cert.Leaf.NotAfter
	}
	return time.Time{}
}

// reload loads the files if they changed since the last load; it reports
// whether it replaced the certificate.
func (c *publicCert) reload() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp, err := fileStamp(c.certPath, c.keyPath)
	if err != nil {
		return false, err
	}
	if stamp == c.stamp {
		return false, nil
	}
	cert, err := tls.LoadX509KeyPair(c.certPath, c.keyPath)
	if err != nil {
		return false, fmt.Errorf("pgproxyd: server certificate: %w", err)
	}
	if cert.Leaf == nil {
		if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return false, fmt.Errorf("pgproxyd: server certificate: %w", err)
		}
	}
	if !c.now().Before(cert.Leaf.NotAfter) {
		return false, fmt.Errorf("pgproxyd: server certificate expired at %s", cert.Leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	c.cur.Store(&cert)
	c.stamp = stamp
	return true, nil
}

// watch reloads on every tick and on every hup until ctx ends.
func (c *publicCert) watch(ctx context.Context, interval time.Duration, hup <-chan os.Signal) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-hup:
		}
		replaced, err := c.reload()
		switch {
		case err != nil:
			c.reloads.WithLabelValues("failed").Inc()
			c.log.Warn("pgproxyd: public certificate reload failed; keeping the one in use", "err", err)
		case replaced:
			c.reloads.WithLabelValues("ok").Inc()
			c.log.Info("pgproxyd: public certificate reloaded", "not_after", c.notAfter().UTC().Format(time.RFC3339))
		}
	}
}

// fileStamp identifies both files' current contents by size and
// modification time; a renewal that installs new files changes it.
func fileStamp(paths ...string) (string, error) {
	stamp := ""
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("pgproxyd: server certificate: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("pgproxyd: server certificate: " + path + " is not a regular file")
		}
		stamp += fmt.Sprintf("%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
	}
	return stamp, nil
}
