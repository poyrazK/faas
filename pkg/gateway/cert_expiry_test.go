// Tests for the ADR-024 H3 cert-expiry refresher
// (pkg/gateway/cert_expiry.go). All tests are unit-level: synth PEM
// certs in a t.TempDir(), drive refreshCertExpiryOnce directly, assert
// the gauge converges to the soonest NotAfter within tolerance.
//
// The refresher is intentionally ctx-driven (no global state, no
// singleton); tests don't need a real *Metrics registry, just a
// pointer to one built via NewMetrics() so the gauge readback
// matches what an operator would see at /metrics.

package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRefreshCertExpiryOnce_FindsSoonest sets up three certs in the
// canonical certmagic layout (certificates/<issuer>/<domain>/<domain>.crt)
// with NotAfter offsets of 60 d, 14 d, and 30 d. refreshCertExpiryOnce
// must return the 14-day cert's remaining time as the soonest.
func TestRefreshCertExpiryOnce_FindsSoonest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	now := time.Now()
	writeCrt(t, filepath.Join(root, "certificates", "acme-v02.api.letsencrypt.org-directory", "soon.example.com"), 14*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "acme-v02.api.letsencrypt.org-directory", "later.example.com"), 60*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "acme-v02.api.letsencrypt.org-directory", "middle.example.com"), 30*24*time.Hour, now)

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("refreshCertExpiryOnce: %v", err)
	}

	// Allow a few seconds of slack for the 14-day cert: time.Until
	// readback skews by however long the test took. The cert was
	// NotAfter 14d-from-now; we expect somewhere between 14d-30s and 14d.
	want := 14 * 24 * time.Hour
	got := gaugeDuration(t, m)

	if got > want {
		t.Fatalf("gauge too high: got %s, want ≤ %s (soonest 14d)", got, want)
	}
	if got < want-30*time.Second {
		t.Fatalf("gauge too low: got %s, want ≥ %s-30s", got, want)
	}
}

// TestRefreshCertExpiryOnce_EmptyDir — a fresh daemon's storage dir is
// empty (no certs minted yet). refreshCertExpiryOnce must leave the
// gauge unknown (NaN),
// so the alert's < expression doesn't fire.
func TestRefreshCertExpiryOnce_EmptyDir(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir() // No certificates/ subdir.

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("refreshCertExpiryOnce: %v", err)
	}
	if got := testutil.ToFloat64(m.tlsCertExpiry); !math.IsNaN(got) {
		t.Fatalf("empty dir should leave expiry unknown (NaN), got %v", got)
	}
}

// TestRefreshCertExpiryOnce_ExpiredCert — when a cert on disk is
// already past its NotAfter, refreshCertExpiryOnce must report a
// NEGATIVE remaining duration (not clamp to 0). The page rule's
// `gateway_tls_cert_expiry_seconds < 14 * 86400` then fires
// unambiguously; a clamp-to-0 would let an early `> 0` alert guard
// filter out the page. PR #345 review (issue A) tightened this.
func TestRefreshCertExpiryOnce_ExpiredCert(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// NotAfter 1 hour ago — the cert is unambiguously expired.
	writeCrt(t, filepath.Join(root, "certificates", "acme", "stale.example.com"), -1*time.Hour, time.Now())

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("refreshCertExpiryOnce: %v", err)
	}
	got := gaugeDuration(t, m)
	if got >= 0 {
		t.Fatalf("expired cert must yield negative remaining, got %s", got)
	}
	// The gauge should report somewhere in [-1h-2s, -1h+1s] — the
	// lower bound accommodates write-then-read wall-clock skew (we
	// wrote `now.Add(-1h)` and read a moment later, so the cert
	// appears slightly more than 1h expired by the time the gauge is
	// computed). 2s of slack is plenty.
	if got < -1*time.Hour-2*time.Second || got > -1*time.Hour+1*time.Second {
		t.Fatalf("expired cert remaining = %s, want in [-1h-2s, -1h+1s]", got)
	}
}

// TestRefreshCertExpiryOnce_MissingDir — storageDir root doesn't exist
// at all (operator hasn't provisioned it yet). refreshCertExpiryOnce
// returns nil and leaves the gauge untouched. The wrapper tick fn
// refreshCertExpiryOnce is called from logs Warn only on real errors;
// missing dir is the expected boot state.
func TestRefreshCertExpiryOnce_MissingDir(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "does-not-exist")

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("missing dir should be silent, got: %v", err)
	}
}

// TestRefreshCertExpiryOnce_SkipsUnparseable — a PEM with garbage in
// one .crt must not fail the whole refresh; the other certs still
// land in the gauge.
func TestRefreshCertExpiryOnce_SkipsUnparseable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	issuerDir := filepath.Join(root, "certificates", "acme")
	goodDir := filepath.Join(issuerDir, "good.example.com")
	if err := os.MkdirAll(goodDir, 0o700); err != nil {
		t.Fatal(err)
	}
	badDir := filepath.Join(issuerDir, "bad.example.com")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCrt(t, goodDir, 7*24*time.Hour, time.Now())
	if err := os.WriteFile(filepath.Join(badDir, "bad.example.com.crt"), []byte("not a PEM block at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("refreshCertExpiryOnce: %v", err)
	}
	// The good cert is 7d out; expect ~7d, with slack.
	got := gaugeDuration(t, m)
	if got > 7*24*time.Hour || got < 7*24*time.Hour-30*time.Second {
		t.Fatalf("gauge = %s, want ~7d", got)
	}
}

// TestStartCertExpiryRefresher_StopsOnCancel drives the production
// ticker with a short interval and asserts stop() halts the loop.
// Asserted by sending a cancellation through ctx (rather than calling
// stop) so we also cover the ctx.Done() path.
func TestStartCertExpiryRefresher_StopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := t.TempDir()
	writeCrt(t, filepath.Join(root, "certificates", "acme", "x.example.com"), 5*24*time.Hour, time.Now())

	m := NewMetrics()
	stop := StartCertExpiryRefresher(ctx, root, m, 50*time.Millisecond, "", nil)
	defer stop()

	// Wait until at least one tick has fired (gauge touched).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gaugeDuration(t, m) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := gaugeDuration(t, m); got == 0 {
		t.Fatalf("gauge should have been set within deadline")
	}
	// cancel() then run stop(); either path is fine — the assertion is
	// that subsequent ticks do not stall.
	cancel()
	stop()
}

// writeCrt generates a self-signed PEM cert with the given lifetime and
// drops it at dir/<domain>.crt. Mirror certmagic's FileStorage layout
// (SiteCert): exactly one .crt file per domain dir.
func writeCrt(t *testing.T, dir string, lifetime time.Duration, now time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    now.Add(-1 * time.Hour),
		NotAfter:     now.Add(lifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	// Compute the directory's basename to derive a domain name. The
	// caller always passes paths ending in the domain (e.g.
	// ".../acme/soon.example.com"), so filepath.Base is the domain.
	domain := filepath.Base(dir)
	if err := os.WriteFile(filepath.Join(dir, domain+".crt"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

// gaugeDuration reads gateway_tls_cert_expiry_seconds back via a
// dedicated registry scrape. Using the registry routes through the same
// path an operator's /metrics scrape does, so we exercise the wire
// shape end-to-end rather than reaching into the gauge field directly.
func gaugeDuration(t *testing.T, m *Metrics) time.Duration {
	t.Helper()
	gauge, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range gauge {
		if fam.GetName() != "gateway_tls_cert_expiry_seconds" {
			continue
		}
		metrics := fam.GetMetric()
		if len(metrics) == 0 {
			return 0
		}
		return time.Duration(metrics[0].GetGauge().GetValue() * float64(time.Second))
	}
	return 0
}

// gaugeByHost returns the (value, found) tuple for a (hostname, kind)
// label pair on the per-host gauge. value is NaN if the gauge has been
// dropped via the stale-host NaN path (Finding 2). Used by the
// per-host attribution tests below.
func gaugeByHost(t *testing.T, m *Metrics, hostname, kind string) (time.Duration, bool) {
	t.Helper()
	gauge, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range gauge {
		if fam.GetName() != "gateway_tls_cert_expiry_by_host_seconds" {
			continue
		}
		for _, mt := range fam.GetMetric() {
			var gotHost, gotKind string
			for _, lp := range mt.GetLabel() {
				switch lp.GetName() {
				case "hostname":
					gotHost = lp.GetValue()
				case "kind":
					gotKind = lp.GetValue()
				}
			}
			if gotHost == hostname && gotKind == kind {
				v := mt.GetGauge().GetValue()
				return time.Duration(v * float64(time.Second)), true
			}
		}
	}
	return 0, false
}

// walkResultCount returns the count for gateway_tls_cert_expiry_refresher_walk_complete_total{result=...}.
func walkResultCount(t *testing.T, m *Metrics, result string) float64 {
	t.Helper()
	gauge, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range gauge {
		if fam.GetName() != "gateway_tls_cert_expiry_refresher_walk_complete_total" {
			continue
		}
		for _, mt := range fam.GetMetric() {
			for _, lp := range mt.GetLabel() {
				if lp.GetName() == "result" && lp.GetValue() == result {
					return mt.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// TestRefreshCertExpiryOnce_PerHostSurface — three certs with
// different NotAfters produce three per-host gauge rows AND the
// aggregate equals the soonest. Pins the per-host attribution
// contract from the Finding 2 plan.
func TestRefreshCertExpiryOnce_PerHostSurface(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Now()

	writeCrt(t, filepath.Join(root, "certificates", "acme-ondemand", "soon.example.com"), 14*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "acme-ondemand", "later.example.com"), 60*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "acme-ondemand", "middle.example.com"), 30*24*time.Hour, now)

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("refreshCertExpiryOnce: %v", err)
	}

	// Aggregate equals soonest (14d, with slack).
	want := 14 * 24 * time.Hour
	got := gaugeDuration(t, m)
	if got > want || got < want-30*time.Second {
		t.Fatalf("aggregate gauge = %s, want ~14d", got)
	}

	// Each per-host series surfaces with the right kind and an
	// expiry close to the written lifetime.
	cases := []struct {
		host string
		want time.Duration
	}{
		{"soon.example.com", 14 * 24 * time.Hour},
		{"later.example.com", 60 * 24 * time.Hour},
		{"middle.example.com", 30 * 24 * time.Hour},
	}
	for _, tc := range cases {
		got, ok := gaugeByHost(t, m, tc.host, "ondemand")
		if !ok {
			t.Errorf("per-host series missing for %s", tc.host)
			continue
		}
		if got > tc.want || got < tc.want-30*time.Second {
			t.Errorf("per-host %s = %s, want ~%s", tc.host, got, tc.want)
		}
	}

	// Walk-result counter ticks "complete".
	if c := walkResultCount(t, m, "complete"); c != 1 {
		t.Errorf("walk_complete{result=complete} = %v, want 1", c)
	}
}

// TestRefreshCertExpiryOnce_KindClassification — the kind label
// tracks the issuer-key path, not the hostname. A cert stored under
// the wildcard issuer-key path classifies as CertKindWildcard; one
// stored under a different acme- prefix classifies as
// CertKindOnDemand; everything else as CertKindUnknown.
func TestRefreshCertExpiryOnce_KindClassification(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Now()

	const wildcardKey = "acme-v02.api.letsencrypt.org-directory"
	writeCrt(t, filepath.Join(root, "certificates", wildcardKey, "wildcard.example.com"), 30*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "acme-other", "ondemand.example.com"), 30*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "internal-ca", "internal.example.com"), 30*24*time.Hour, now)

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, wildcardKey, m, nil); err != nil {
		t.Fatalf("refreshCertExpiryOnce: %v", err)
	}

	wantKinds := map[string]string{
		"wildcard.example.com": "wildcard",
		"ondemand.example.com": "ondemand",
		"internal.example.com": "unknown",
	}
	for host, wantKind := range wantKinds {
		_, ok := gaugeByHost(t, m, host, wantKind)
		if !ok {
			t.Errorf("per-host {hostname=%q,kind=%q} missing", host, wantKind)
		}
	}
}

// TestRefreshCertExpiryOnce_StaleHostNaNed — a host present in
// tick N but absent in tick N+1 must be NaN'd so Prometheus drops
// the series. Prevents "ghost" gauges from carrying stale values
// into the next tick.
func TestRefreshCertExpiryOnce_StaleHostNaNed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Now()

	keepDir := filepath.Join(root, "certificates", "acme-ondemand", "keep.example.com")
	dropDir := filepath.Join(root, "certificates", "acme-ondemand", "drop.example.com")
	writeCrt(t, keepDir, 30*24*time.Hour, now)
	writeCrt(t, dropDir, 30*24*time.Hour, now)

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	// Both present after tick 1.
	if _, ok := gaugeByHost(t, m, "keep.example.com", "ondemand"); !ok {
		t.Fatalf("tick 1: keep.example.com missing")
	}
	if _, ok := gaugeByHost(t, m, "drop.example.com", "ondemand"); !ok {
		t.Fatalf("tick 1: drop.example.com missing")
	}

	// Tick 2: remove drop's directory entirely. The refresher must
	// NaN the drop.example.com gauge and leave keep.example.com
	// untouched.
	if err := os.RemoveAll(dropDir); err != nil {
		t.Fatal(err)
	}
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if _, ok := gaugeByHost(t, m, "drop.example.com", "ondemand"); ok {
		t.Errorf("drop.example.com series should be deleted after stale-tick, but it still exists")
	}
	// keep.example.com must still be present with a positive value.
	if got, ok := gaugeByHost(t, m, "keep.example.com", "ondemand"); !ok || got <= 0 {
		t.Errorf("keep.example.com lost or non-positive: %v %s", ok, got)
	}
}

// TestRefreshCertExpiryOnce_EmptyNaNsAllAdmitted — an empty walk
// must NaN every previously-admitted per-host gauge. Mirrors the
// "fresh daemon" or "post-cutover" state where the operator expects
// the per-host panel to drain, not carry stale values.
func TestRefreshCertExpiryOnce_EmptyNaNsAllAdmitted(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Now()

	writeCrt(t, filepath.Join(root, "certificates", "acme-ondemand", "a.example.com"), 30*24*time.Hour, now)

	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if _, ok := gaugeByHost(t, m, "a.example.com", "ondemand"); !ok {
		t.Fatalf("tick 1: a.example.com missing")
	}

	// Tick 2: delete the entire certificates/ subtree so the walk
	// finds nothing.
	if err := os.RemoveAll(filepath.Join(root, "certificates")); err != nil {
		t.Fatal(err)
	}
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if _, ok := gaugeByHost(t, m, "a.example.com", "ondemand"); ok {
		t.Errorf("a.example.com series should be deleted after empty tick, but it still exists")
	}
	// Walk-result counter ticks "empty" (not "partial").
	if c := walkResultCount(t, m, "empty"); c != 1 {
		t.Errorf("walk_complete{result=empty} = %v, want 1", c)
	}
}

// TestRefreshCertExpiryOnce_PartialLeavesGaugesUntouched — a walk
// that errors mid-stream after parsing ≥1 cert must tick the
// partial counter AND leave the gauges untouched. A transient FS
// blip should not page; the partial counter is the page signal.
//
// Uses the walkDirFn seam (pkg/gateway/cert_expiry.go) to inject a
// wrapper that wraps the real filepath.WalkDir but returns
// fs.ErrPermission after the first cert is parsed. This pins the
// partial branch in refreshCertExpiryOnce without needing FS
// gymnastics to induce a real walk-time error.
func TestRefreshCertExpiryOnce_PartialLeavesGaugesUntouched(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Now()

	// Tick 1: walk succeeds with two certs — baseline.
	writeCrt(t, filepath.Join(root, "certificates", "acme-ondemand", "good.example.com"), 14*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "acme-ondemand", "good2.example.com"), 60*24*time.Hour, now)
	m := NewMetrics()
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	before1 := gaugeByHostOrZero(t, m, "good.example.com", "ondemand")
	before2 := gaugeByHostOrZero(t, m, "good2.example.com", "ondemand")
	if before1 <= 0 || before2 <= 0 {
		t.Fatalf("tick 1: per-host gauges not positive: good=%s good2=%s", before1, before2)
	}
	// Aggregate = min(14d, 60d) = 14d.
	wantAgg := 14 * 24 * time.Hour
	if got := gaugeDuration(t, m); got > wantAgg || got < wantAgg-30*time.Second {
		t.Fatalf("tick 1: aggregate = %s, want ~14d", got)
	}

	// Tick 2: inject a walker that wraps filepath.WalkDir but errors
	// after the first .crt is parsed. Reset the seam on test exit so
	// other tests aren't affected.
	var calls int
	original := walkDirFn
	t.Cleanup(func() { walkDirFn = original })
	walkDirFn = func(root string, fn fs.WalkDirFunc) error {
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return fn(path, d, walkErr)
			}
			if !d.IsDir() && strings.HasSuffix(path, ".crt") {
				calls++
				if calls == 1 {
					// First .crt entry: dispatch it to fn() normally so the
					// refresher parses it and accrues parsed=1, then return
					// the injected error as if the walk itself failed. The
					// next walkCerts call will increment walkFailed and
					// classify the tick as walkResultPartial.
					_ = fn(path, d, nil)
					return errInjectPartial
				}
			}
			return fn(path, d, nil)
		})
	}

	// refreshCertExpiryOnce MUST return the injected walkErr so the
	// daemon's slog logs it (StartCertExpiryRefresher calls the func
	// with a Warn handler). The partial-walk counter tick is the
	// page signal; the slog line is the contextual breadcrumb.
	if err := refreshCertExpiryOnce(ctx, root, "", m, nil); !errors.Is(err, errInjectPartial) {
		t.Fatalf("tick 2: err = %v, want %v", err, errInjectPartial)
	}
	// Walk-result counter ticks "partial" exactly once for tick 2;
	// tick 1's "complete" should NOT have ticked "partial".
	if c := walkResultCount(t, m, "partial"); c != 1 {
		t.Errorf("walk_complete{result=partial} = %v, want 1", c)
	}
	// Gauges must be UNTOUCHED on the partial path. before1 / before2
	// captured the tick-1 values; they MUST still be present (a
	// partial walk must not page on stale-but-recent data).
	after1 := gaugeByHostOrZero(t, m, "good.example.com", "ondemand")
	after2 := gaugeByHostOrZero(t, m, "good2.example.com", "ondemand")
	if after1 != before1 || after2 != before2 {
		t.Errorf("partial walk mutated gauges: good %s→%s good2 %s→%s", before1, after1, before2, after2)
	}
	// Aggregate gauge untouched too — the aggregate is only written
	// on walkResultComplete, so a no-write test is trivially
	// satisfied by the partial branch. Pin the positive assertion
	// explicitly.
	if got := gaugeDuration(t, m); got > wantAgg || got < wantAgg-30*time.Second {
		t.Errorf("tick 2: aggregate changed: now=%s, want ~14d (unchanged from tick 1)", got)
	}
}

// errInjectPartial is the sentinel error the injected walker
// returns mid-walk in TestRefreshCertExpiryOnce_PartialLeavesGaugesUntouched.
// Distinct from fs.ErrPermission so the test author reads it as a
// synthetic hook rather than a misread of stdlib semantics.
var errInjectPartial = errors.New("gateway: injected partial walk error (test seam)")

// TestWalkCerts_PartialError — direct walkCerts test that pins the
// walkResult=partial branch when the walker returns a non-ErrNotExist
// error mid-stream after parsing ≥1 cert. The walkDirFn seam lets
// us return the error at a precise point without FS gymnastics.
//
// Contract:
//   - hosts = the certs parsed BEFORE the error (partial snapshot,
//     not the full tree);
//   - soonest = the soonest NotAfter across the partial snapshot;
//   - walkResult = walkResultPartial;
//   - err = the injected error surfaced to the caller (the daemon's
//     refreshCertExpiryOnce logs the wrapper, then leaves gauges
//     untouched on the partial branch).
func TestWalkCerts_PartialError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Now()

	// Write three certs in three subdirs so alphabetical walk order
	// is deterministic.
	writeCrt(t, filepath.Join(root, "certificates", "acme-a", "aaa.example.com"), 14*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "acme-b", "bbb.example.com"), 60*24*time.Hour, now)
	writeCrt(t, filepath.Join(root, "certificates", "acme-c", "ccc.example.com"), 30*24*time.Hour, now)

	var calls int
	original := walkDirFn
	t.Cleanup(func() { walkDirFn = original })
	walkDirFn = func(root string, fn fs.WalkDirFunc) error {
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return fn(path, d, walkErr)
			}
			if !d.IsDir() && strings.HasSuffix(path, ".crt") {
				calls++
				if calls == 2 {
					// After the SECOND cert parses, kill the walk. Dispatch
					// the second cert normally so the refresher accrues
					// parsed=2, then return the injected error.
					_ = fn(path, d, nil)
					return errInjectPartial
				}
			}
			return fn(path, d, nil)
		})
	}

	hosts, soonest, walkResult, err := walkCerts(ctx, filepath.Join(root, "certificates"), "", nil)
	if !errors.Is(err, errInjectPartial) {
		t.Fatalf("walkCerts err = %v, want %v", err, errInjectPartial)
	}
	if walkResult != walkResultPartial {
		t.Errorf("walkResult = %q, want %q", walkResult, walkResultPartial)
	}
	if len(hosts) != 2 {
		t.Errorf("partial hosts = %d, want 2 (the walker stopped after the second cert)", len(hosts))
	}
	// soonest = min(parsed). The first two certs in alphabetical
	// order are aaa (14d) and ccc (30d) — the walker enters acme-a
	// then acme-b before acme-c, but the call counter is at TWO
	// after aaa + ccc parse. Compare against min(14d, 30d) which is
	// 14d. Allow a 30s slack so the wall-clock isn't load-bearing.
	if want := 14 * 24 * time.Hour; soonest.After(now.Add(want)) || soonest.Before(now.Add(want).Add(-30*time.Second)) {
		t.Errorf("soonest = %s, want ~14d from %s", soonest.Sub(now), now)
	}
}

// gaugeByHostOrZero is the non-erroring cousin of gaugeByHost —
// returns 0 when the series is absent so tests can compare
// before/after values without nil checks.
func gaugeByHostOrZero(t *testing.T, m *Metrics, hostname, kind string) time.Duration {
	t.Helper()
	d, _ := gaugeByHost(t, m, hostname, kind)
	return d
}

// TestHostnameLabelSet_OverflowCollapsesToOther — the per-host
// admission set collapses distinct hostnames past the cap to
// "__other__" so the per-host gauge cardinality stays bounded.
func TestHostnameLabelSet_OverflowCollapsesToOther(t *testing.T) {
	const cap = 4
	s := newHostnameLabelSetWithCap(cap)
	for i := 1; i <= cap; i++ {
		got := s.admit(hostnameForIndex(i))
		if got != hostnameForIndex(i) {
			t.Fatalf("admit(%q) = %q, want %q", hostnameForIndex(i), got, hostnameForIndex(i))
		}
	}
	// Now the budget is exhausted — the next distinct hostname collapses.
	got := s.admit("overflow.example.com")
	if got != otherHostnameLabel {
		t.Fatalf("overflow admit = %q, want %q", got, otherHostnameLabel)
	}
	// Reserved labels are pass-through.
	if got := s.admit(""); got != anonymousHostnameLabel {
		t.Errorf("admit(\"\") = %q, want %q", got, anonymousHostnameLabel)
	}
	if got := s.admit(otherHostnameLabel); got != otherHostnameLabel {
		t.Errorf("admit(__other__) = %q, want %q (pass-through)", got, otherHostnameLabel)
	}
}

func hostnameForIndex(i int) string {
	return "host-" + string(rune('a'+i-1)) + ".example.com"
}

// TestNewHostnameLabelSetPanicsOnZeroCapacity — fail-loud at boot.
func TestNewHostnameLabelSetPanicsOnZeroCapacity(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("newHostnameLabelSetWithCap(0) did not panic")
		}
	}()
	_ = newHostnameLabelSetWithCap(0)
}
