package edgecert

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

// Issuance is one certificate a Certificate Transparency log recorded for
// the watched domain or a name under it. A precertificate and its
// certificate are one issuance.
type Issuance struct {
	ID    string
	Names []string
	// PubkeySHA256 is the lowercase hex SHA-256 of the certificate's
	// SubjectPublicKeyInfo: a precertificate and its certificate share it,
	// so it identifies the key whichever of the two a log holds.
	PubkeySHA256 string
	CertSHA256   string
	Issuer       string
	NotBefore    time.Time
	NotAfter     time.Time
}

// IssuanceSource lists the unexpired issuances for domain and every name
// under it.
type IssuanceSource interface {
	Issuances(ctx context.Context, domain string) ([]Issuance, error)
}

// KeyStore lists the keys of the certificates edgednsd stored, as the
// lowercase hex SHA-256 of each leaf's SubjectPublicKeyInfo.
type KeyStore interface {
	StoredKeys(ctx context.Context) (map[string]bool, error)
}

// StoredKeys is the KeyStore over Postgres. It reads every stored chain,
// expired ones included: rows are kept api.EdgeCertificateKeepAfterExpiry
// past expiry, longer than a log lists them.
func (s *PgStore) StoredKeys(ctx context.Context) (map[string]bool, error) {
	chains, err := s.q.EdgeTLSCertificateChains(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("edgecert: read the stored chains: %w", err)
	}
	keys := make(map[string]bool, len(chains))
	for _, chain := range chains {
		key, err := LeafKeySHA256(chain)
		if err != nil {
			return nil, err
		}
		keys[key] = true
	}
	return keys, nil
}

// LeafKeySHA256 returns the lowercase hex SHA-256 of the SubjectPublicKeyInfo
// of chainPEM's first certificate.
func LeafKeySHA256(chainPEM string) (string, error) {
	block, _ := pem.Decode([]byte(chainPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("edgecert: a stored chain holds no certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("edgecert: parse a stored leaf: %w", err)
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), nil
}

// Transparency is edgednsd's Certificate Transparency watch
// (ADR-20261103163954 §3). Every api.EdgeCertificateTransparencyInterval it
// lists the publicly trusted certificates for Domain and every name under
// it, and counts those edgednsd did not issue: a certificate is
// edgednsd's when its key is one edgednsd stored. A certificate whose every
// name is in Allow is someone else's by design, such as the CDN's own
// certificate for the zone or a hosted site's.
//
// The count drives a page. A wildcard certificate needs control of the
// zone's DNS, so an unexpected one means the zone or its token is
// compromised; a certificate for a single app host is what a compromised
// compute node could obtain by answering a CA's challenge for the hosts it
// serves, and it stays valid after the apps domains' key is rotated.
type Transparency struct {
	Domain  string
	Allow   []string
	Source  IssuanceSource
	Keys    KeyStore
	Metrics *TransparencyMetrics
	Log     *slog.Logger

	reported map[string]bool
}

// Run checks at once, then every api.EdgeCertificateTransparencyInterval,
// until ctx ends. A failed check is retried on the next one.
func (t *Transparency) Run(ctx context.Context) {
	ticker := time.NewTicker(api.EdgeCertificateTransparencyInterval)
	defer ticker.Stop()
	for {
		if err := t.Check(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Log.Warn("edgecert: certificate transparency check failed", "domain", t.Domain, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Check lists the issuances once and sets the count of unexpected ones. It
// logs each unexpected issuance at error level the first time it sees it.
func (t *Transparency) Check(ctx context.Context) error {
	issuances, err := t.Source.Issuances(ctx, t.Domain)
	if err != nil {
		t.Metrics.checked(checkResult(err))
		return err
	}
	keys, err := t.Keys.StoredKeys(ctx)
	if err != nil {
		t.Metrics.checked("error")
		return err
	}
	seen := make(map[string]bool)
	unexpected := 0
	for _, iss := range issuances {
		if keys[iss.PubkeySHA256] || t.allowed(iss.Names) {
			continue
		}
		unexpected++
		seen[iss.ID] = true
		if t.reported[iss.ID] {
			continue
		}
		t.Log.Error("edgecert: a certificate edgednsd did not issue",
			"domain", t.Domain, "issuance", iss.ID, "names", strings.Join(iss.Names, ","),
			"issuer", iss.Issuer, "not_before", iss.NotBefore, "not_after", iss.NotAfter,
			"cert_sha256", iss.CertSHA256, "pubkey_sha256", iss.PubkeySHA256)
	}
	t.reported = seen
	t.Metrics.unexpected(unexpected)
	t.Metrics.checked("ok")
	return nil
}

// allowed reports whether every one of names is in t.Allow. A certificate
// with no names is not.
func (t *Transparency) allowed(names []string) bool {
	if len(names) == 0 {
		return false
	}
	for _, n := range names {
		if !slices.Contains(t.Allow, strings.ToLower(n)) {
			return false
		}
	}
	return true
}

// NormalizeWatch lowercases and checks the watch's domain, a host name, and
// its allow list: host names or one-label wildcards, at most
// api.EdgeCertificateTransparencyAllowNamesMax, sorted, no duplicates.
func NormalizeWatch(domain string, allow []string) (string, []string, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if strings.HasPrefix(domain, "*.") || !validName(domain) {
		return "", nil, fmt.Errorf("edgecert: the watched domain %q is not a host name", domain)
	}
	if len(allow) > api.EdgeCertificateTransparencyAllowNamesMax {
		return "", nil, fmt.Errorf("edgecert: the watch allows at most %d names, not %d", api.EdgeCertificateTransparencyAllowNamesMax, len(allow))
	}
	out := make([]string, 0, len(allow))
	for _, n := range allow {
		n = strings.ToLower(strings.TrimSpace(n))
		if !validName(n) {
			return "", nil, fmt.Errorf("edgecert: the allowed name %q is not a host name or a *. wildcard", n)
		}
		out = append(out, n)
	}
	slices.Sort(out)
	if len(slices.Compact(slices.Clone(out))) != len(out) {
		return "", nil, fmt.Errorf("edgecert: the watch's allow list %v names a host twice", out)
	}
	return domain, out, nil
}

// ErrRateLimited is a source's refusal to answer more queries for now.
var ErrRateLimited = errors.New("edgecert: certificate transparency source rate limited")

func checkResult(err error) string {
	if errors.Is(err, ErrRateLimited) {
		return "rate_limited"
	}
	return "error"
}

// CertSpotterAPI is SSLMate's Cert Spotter issuances endpoint.
const CertSpotterAPI = "https://api.certspotter.com/v1/issuances"

// CertSpotter is the IssuanceSource over SSLMate's Cert Spotter API. The key
// is optional: without one, Cert Spotter allows a few queries a day.
type CertSpotter struct {
	URL    string
	Key    string
	Client *http.Client
}

type certSpotterIssuance struct {
	ID           string    `json:"id"`
	DNSNames     []string  `json:"dns_names"`
	PubkeySHA256 string    `json:"pubkey_sha256"`
	CertSHA256   string    `json:"cert_sha256"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	Issuer       struct {
		FriendlyName string `json:"friendly_name"`
		Name         string `json:"name"`
	} `json:"issuer"`
}

// Issuances pages through the unexpired issuances for domain and its
// subdomains, at most api.EdgeCertificateTransparencyPagesMax pages.
func (c *CertSpotter) Issuances(ctx context.Context, domain string) ([]Issuance, error) {
	var out []Issuance
	after := ""
	for range api.EdgeCertificateTransparencyPagesMax {
		page, err := c.page(ctx, domain, after)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		for _, p := range page {
			names := make([]string, 0, len(p.DNSNames))
			for _, n := range p.DNSNames {
				names = append(names, strings.ToLower(n))
			}
			issuer := p.Issuer.FriendlyName
			if issuer == "" {
				issuer = p.Issuer.Name
			}
			out = append(out, Issuance{
				ID: p.ID, Names: names, PubkeySHA256: strings.ToLower(p.PubkeySHA256), CertSHA256: strings.ToLower(p.CertSHA256),
				Issuer: issuer, NotBefore: p.NotBefore, NotAfter: p.NotAfter,
			})
		}
		after = page[len(page)-1].ID
	}
	return nil, fmt.Errorf("edgecert: %s has more than %d pages of issuances", domain, api.EdgeCertificateTransparencyPagesMax)
}

func (c *CertSpotter) page(ctx context.Context, domain, after string) ([]certSpotterIssuance, error) {
	q := url.Values{}
	q.Set("domain", domain)
	q.Set("include_subdomains", "true")
	q.Add("expand", "dns_names")
	q.Add("expand", "issuer")
	if after != "" {
		q.Set("after", after)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EdgeCertificateTransparencyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url()+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("edgecert: certificate transparency request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gregale-edgednsd")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("edgecert: query certificate transparency: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.EdgeCertificateTransparencyPageBytesMax+1))
	if err != nil {
		return nil, fmt.Errorf("edgecert: read certificate transparency: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: %s", ErrRateLimited, snippet(body))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("edgecert: certificate transparency answered %d: %s", resp.StatusCode, snippet(body))
	case len(body) > api.EdgeCertificateTransparencyPageBytesMax:
		return nil, fmt.Errorf("edgecert: a certificate transparency page is over %d bytes", api.EdgeCertificateTransparencyPageBytesMax)
	}
	var page []certSpotterIssuance
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("edgecert: decode certificate transparency: %w", err)
	}
	return page, nil
}

func (c *CertSpotter) url() string {
	if c.URL != "" {
		return c.URL
	}
	return CertSpotterAPI
}

func (c *CertSpotter) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

func snippet(body []byte) string {
	const max = 200
	s := strings.TrimSpace(string(body))
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// TransparencyMetrics are the watch's series. A nil *TransparencyMetrics
// records nothing.
type TransparencyMetrics struct {
	checks      *prometheus.CounterVec
	unexpectedG prometheus.Gauge
	lastOK      prometheus.Gauge
}

// NewTransparencyMetrics registers the watch's series with reg.
func NewTransparencyMetrics(reg prometheus.Registerer) *TransparencyMetrics {
	m := &TransparencyMetrics{
		checks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "edge_certificate_transparency_checks_total",
			Help: "Certificate Transparency checks edgednsd made for the apps domain, by result (ok, error, rate_limited) (ADR-20261103163954 §3).",
		}, []string{"result"}),
		unexpectedG: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "edge_certificate_transparency_unexpected",
			Help: "Unexpired publicly trusted certificates for the apps domain or a name under it that edgednsd did not issue and the allow list does not cover, as of the last successful check (ADR-20261103163954 §3).",
		}),
		lastOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "edge_certificate_transparency_last_success_timestamp_seconds",
			Help: "Unix time of edgednsd's last successful Certificate Transparency check (ADR-20261103163954 §3).",
		}),
	}
	reg.MustRegister(m.checks, m.unexpectedG, m.lastOK)
	return m
}

func (m *TransparencyMetrics) checked(result string) {
	if m == nil {
		return
	}
	m.checks.WithLabelValues(result).Inc()
	if result == "ok" {
		m.lastOK.SetToCurrentTime()
	}
}

func (m *TransparencyMetrics) unexpected(n int) {
	if m != nil {
		m.unexpectedG.Set(float64(n))
	}
}
