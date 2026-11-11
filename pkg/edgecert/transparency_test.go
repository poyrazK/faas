// adr: 20261103163954
package edgecert_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/edgecert"
)

// A stored chain is known by its leaf's SubjectPublicKeyInfo, which a
// precertificate and its certificate share.
func TestLeafKeySHA256IsTheLeafsPublicKeyInfo(t *testing.T) {
	ca := newFakeCA(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	chain, _, err := ca.Order(context.Background(), key, wildcard)
	if err != nil {
		t.Fatal(err)
	}
	spki, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	sum := sha256.Sum256(spki)
	got, err := edgecert.LeafKeySHA256(string(chain))
	if err != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatalf("LeafKeySHA256 = %q, %v; want %x", got, err, sum)
	}
	if _, err := edgecert.LeafKeySHA256("not a chain"); err == nil {
		t.Fatal("a chain without a certificate has a key")
	}
}

func TestNormalizeWatch(t *testing.T) {
	domain, allow, err := edgecert.NormalizeWatch(" Gregale.dev ", []string{"*.gregale.dev", "Operations.gregale.dev", "gregale.dev"})
	if err != nil || domain != "gregale.dev" || strings.Join(allow, ",") != "*.gregale.dev,gregale.dev,operations.gregale.dev" {
		t.Fatalf("NormalizeWatch = %q, %v, %v", domain, allow, err)
	}
	for _, tc := range []struct {
		name   string
		domain string
		allow  []string
	}{
		{"a wildcard domain", "*.gregale.dev", nil},
		{"no domain", "", nil},
		{"a bad allowed name", "gregale.dev", []string{"a b.gregale.dev"}},
		{"an allowed name twice", "gregale.dev", []string{"gregale.dev", "Gregale.dev"}},
		{"too many allowed names", "gregale.dev", strings.Split(strings.Repeat("x.gregale.dev,", 33), ",")[:33]},
	} {
		if _, _, err := edgecert.NormalizeWatch(tc.domain, tc.allow); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
}

// certSpotter answers pages of issuances in order, recording each query.
type certSpotter struct {
	pages   [][]map[string]any
	status  int
	queries []*http.Request
}

func (c *certSpotter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.queries = append(c.queries, r)
	if c.status != 0 {
		w.WriteHeader(c.status)
		_, _ = w.Write([]byte(`{"code":"rate_limited","message":"slow down"}`))
		return
	}
	page := []map[string]any{}
	if n := len(c.queries) - 1; n < len(c.pages) {
		page = c.pages[n]
	}
	_ = json.NewEncoder(w).Encode(page)
}

func issuance(id, pubkey string, names ...string) map[string]any {
	return map[string]any{
		"id": id, "tbs_sha256": "aa", "cert_sha256": "BB" + id, "dns_names": names, "pubkey_sha256": pubkey,
		"issuer":     map[string]any{"friendly_name": "Let's Encrypt", "name": "C=US, O=Let's Encrypt, CN=YE2"},
		"not_before": "2026-11-04T10:00:00Z", "not_after": "2027-02-02T10:00:00Z", "revoked": false,
	}
}

// The source pages through every unexpired issuance for the domain and the
// names under it, with their names and issuer, and stops at an empty page.
func TestCertSpotterPagesThroughTheIssuances(t *testing.T) {
	fake := &certSpotter{pages: [][]map[string]any{
		{issuance("10", "K1", "*.Gregale.dev", "gregale.dev"), issuance("11", "K2", "h1.gregale.dev")},
		{issuance("12", "K3", "operations.gregale.dev")},
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	src := &edgecert.CertSpotter{URL: srv.URL, Key: "k-123"}
	got, err := src.Issuances(context.Background(), "gregale.dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || len(fake.queries) != 3 {
		t.Fatalf("got %d issuances in %d queries", len(got), len(fake.queries))
	}
	first := got[0]
	if first.ID != "10" || strings.Join(first.Names, ",") != "*.gregale.dev,gregale.dev" || first.PubkeySHA256 != "k1" ||
		first.CertSHA256 != "bb10" || first.Issuer != "Let's Encrypt" || !first.NotAfter.Equal(time.Date(2027, 2, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("first = %+v", first)
	}
	q := fake.queries[0].URL.Query()
	if q.Get("domain") != "gregale.dev" || q.Get("include_subdomains") != "true" || strings.Join(q["expand"], ",") != "dns_names,issuer" || q.Get("after") != "" {
		t.Fatalf("first query = %s", fake.queries[0].URL.RawQuery)
	}
	if after := fake.queries[1].URL.Query().Get("after"); after != "11" {
		t.Fatalf("second page after = %q, want the last id of the first", after)
	}
	if auth := fake.queries[0].Header.Get("Authorization"); auth != "Bearer k-123" {
		t.Fatalf("Authorization = %q", auth)
	}
}

func TestCertSpotterReportsARateLimit(t *testing.T) {
	for _, tc := range []struct {
		status      int
		rateLimited bool
	}{{http.StatusTooManyRequests, true}, {http.StatusInternalServerError, false}} {
		srv := httptest.NewServer(&certSpotter{status: tc.status})
		_, err := (&edgecert.CertSpotter{URL: srv.URL}).Issuances(context.Background(), "gregale.dev")
		srv.Close()
		if err == nil || errors.Is(err, edgecert.ErrRateLimited) != tc.rateLimited {
			t.Errorf("status %d: err = %v", tc.status, err)
		}
	}
}

type issuances struct {
	list []edgecert.Issuance
	err  error
}

func (s *issuances) Issuances(context.Context, string) ([]edgecert.Issuance, error) {
	return s.list, s.err
}

type storedKeys struct {
	keys map[string]bool
	err  error
}

func (s *storedKeys) StoredKeys(context.Context) (map[string]bool, error) { return s.keys, s.err }

// A certificate is edgednsd's when its key is stored, someone else's by
// design when every name is allowed, and unexpected otherwise. Each
// unexpected one is logged once and counted while the logs list it.
func TestTransparencyCountsCertificatesEdgednsdDidNotIssue(t *testing.T) {
	src := &issuances{list: []edgecert.Issuance{
		{ID: "1", Names: []string{"*.gregale.dev"}, PubkeySHA256: "ours"},
		{ID: "2", Names: []string{"*.gregale.dev", "gregale.dev"}, PubkeySHA256: "cdn"},
		{ID: "3", Names: []string{"operations.gregale.dev"}, PubkeySHA256: "hosted"},
		{ID: "4", Names: []string{"h1.gregale.dev"}, PubkeySHA256: "node"},
		{ID: "5", Names: []string{"gregale.dev", "h2.gregale.dev"}, PubkeySHA256: "other"},
		{ID: "6", PubkeySHA256: "nameless"},
	}}
	keys := &storedKeys{keys: map[string]bool{"ours": true}}
	reg := prometheus.NewRegistry()
	var logs bytes.Buffer
	w := &edgecert.Transparency{
		Domain: "gregale.dev", Allow: []string{"*.gregale.dev", "gregale.dev", "operations.gregale.dev"},
		Source: src, Keys: keys, Metrics: edgecert.NewTransparencyMetrics(reg),
		Log: slog.New(slog.NewTextHandler(&logs, nil)),
	}
	if err := w.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := gauge(t, reg, "edge_certificate_transparency_unexpected"); got != 3 {
		t.Fatalf("unexpected = %v, want 3 (ids 4, 5, 6)", got)
	}
	for _, id := range []string{"issuance=4", "issuance=5", "issuance=6"} {
		if strings.Count(logs.String(), id) != 1 {
			t.Fatalf("log misses %s:\n%s", id, logs.String())
		}
	}
	if strings.Contains(logs.String(), "issuance=1 ") || strings.Contains(logs.String(), "issuance=2 ") {
		t.Fatalf("logged an expected certificate:\n%s", logs.String())
	}

	// The next check logs nothing new; once id 4 expires out of the logs
	// and id 5 is revoked and gone, only id 6 counts.
	logs.Reset()
	if err := w.Check(context.Background()); err != nil || logs.Len() != 0 {
		t.Fatalf("second check: err = %v, logs:\n%s", err, logs.String())
	}
	src.list = append(src.list[:3], src.list[5])
	if err := w.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := gauge(t, reg, "edge_certificate_transparency_unexpected"); got != 1 {
		t.Fatalf("unexpected = %v, want 1", got)
	}

	// A failed read keeps the last count and is counted by result.
	src.err = edgecert.ErrRateLimited
	if err := w.Check(context.Background()); err == nil {
		t.Fatal("a rate-limited check succeeded")
	}
	src.err, keys.err = nil, errors.New("db down")
	if err := w.Check(context.Background()); err == nil {
		t.Fatal("a check without the stored keys succeeded")
	}
	if got := gauge(t, reg, "edge_certificate_transparency_unexpected"); got != 1 {
		t.Fatalf("unexpected after failed checks = %v, want 1", got)
	}
	checks := `
		# HELP edge_certificate_transparency_checks_total Certificate Transparency checks edgednsd made for the apps domain, by result (ok, error, rate_limited) (ADR-20261103163954 §3).
		# TYPE edge_certificate_transparency_checks_total counter
		edge_certificate_transparency_checks_total{result="error"} 1
		edge_certificate_transparency_checks_total{result="ok"} 3
		edge_certificate_transparency_checks_total{result="rate_limited"} 1
	`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(checks), "edge_certificate_transparency_checks_total"); err != nil {
		t.Fatal(err)
	}
	if got := gauge(t, reg, "edge_certificate_transparency_last_success_timestamp_seconds"); got < float64(time.Now().Add(-time.Minute).Unix()) {
		t.Fatalf("last success = %v", got)
	}
}

func gauge(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name && len(f.GetMetric()) == 1 {
			return f.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("no %s", name)
	return 0
}
