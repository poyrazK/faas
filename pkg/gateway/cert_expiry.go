// Cert-expiry refresher for the gateway_tls_cert_expiry_seconds gauge
// (ADR-024 H3, spec §12) AND its per-host sibling
// gateway_tls_cert_expiry_by_host_seconds (Finding 2 / ADR-024 H3
// follow-up).
//
// certmagic v0.25.4's Cache keeps full iteration private
// (cache.getAllCerts is unexported); the public surface for "what
// certs does this daemon have?" is the Storage interface that
// certmagic itself uses. We own the on-disk layout: certmagic's
// FileStorage writes certs under
//
//	<StorageDir>/certificates/<issuerKey>/<domain>/<domain>.crt
//
// (verified against /Users/poyrazk/go/pkg/mod/github.com/caddyserver/certmagic@v0.25.4/storage.go:230-235
// and filestorage.go:118-130). Walking that tree once per interval —
// and parsing each .crt's leaf for NotAfter — is the only public-API
// path to "soonest expiry across cached certs".
//
// The refresher is best-effort: a transient parse error on one cert
// does not fail the loop. A consistent "no certs on disk yet" case is
// silent — the aggregate gauge stays at +Inf (the prometheus.Gauge
// default) and the alert rule's `< 14 * 86400` expression correctly
// returns false for Inf, so no spurious page fires pre-traffic.
//
// PER-HOST ATTRIBUTION (Finding 2): the walk now also produces a
// per-host snapshot, classifies each host by issuer key, and writes
// one gateway_tls_cert_expiry_by_host_seconds gauge per host. Stale
// hosts (present in the prior tick's snapshot but absent in the
// current walk) are NaN'd so Prometheus drops the series and the
// alert's < expression returns false for the absent series. A
// walk-completeness classifier ticks a per-result counter so the
// operator can page on "refresher silently failing" instead of
// waiting for an actual cert to expire.

package gateway

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// certmagicCertDir is the top-level directory under cfg.StorageDir where
// certmagic's FileStorage writes issued/renewed certs. See storage.go:357
// (`prefixCerts = "certificates"`) in the vendored certmagic package.
const certmagicCertDir = "certificates"

// CertKind classifies a host's certificate by the issuer-key path
// that minted it (Finding 2). The classification is the source of
// truth for the `kind` label on
// gateway_tls_cert_expiry_by_host_seconds — it does NOT infer from
// the hostname (a wildcard hostname can be issued via the on-demand
// path on certmagic versions that share an issuer key).
type CertKind string

const (
	// CertKindWildcard marks the wildcard *.apps.<zone> certificate
	// minted via DNS-01.
	CertKindWildcard CertKind = "wildcard"
	// CertKindOnDemand marks a customer custom-domain certificate
	// minted via HTTP-01 against the custom_domains allowlist.
	CertKindOnDemand CertKind = "ondemand"
	// CertKindUnknown is the fallback when the issuer key doesn't
	// match either of the known buckets (e.g. a future issuer type
	// the refresher doesn't know about). The alert rules exclude
	// kind="unknown" so a misclassification does not page.
	CertKindUnknown CertKind = "unknown"
)

// hostCert is one entry in the per-host snapshot produced by walkCerts.
type hostCert struct {
	hostname string
	kind     CertKind
	notAfter time.Time
}

// StartCertExpiryRefresher walks cfg.StorageDir every interval and writes
// the minimum remaining lifetime across cached certs to
// m.SetTLSCertExpiry AND the per-host snapshot to m.ObserveHostCertExpiry.
// Returns a stop() closure the caller MUST invoke to halt the ticker on
// shutdown; main wires stop() into the signal-driven shutdown path.
//
// interval is typically 5 min — LE certs have a 90-day lifetime and
// certmagic's renew loop starts at the 30-day mark, so a 5-min poll
// gives the §12 alert plenty of headroom and keeps file I/O negligible
// (one filepath.Walk per tick, only the .crt files touched).
//
// storageDir is the same path certmagic writes to — the role installs
// it as faas:faas 0700, and the daemon runs as user faas, so reading
// is straightforward; nothing here writes to the dir.
//
// wildcardIssuerKey is the certmagic issuer-key glob (e.g.
// "acme-v02.api.letsencrypt.org-directory") used by the DNS-01 solver
// for the *.apps.<zone> wildcard. Issuer keys whose name matches this
// glob are classified as CertKindWildcard; everything else with a
// well-known on-demand issuer key is CertKindOnDemand; the rest are
// CertKindUnknown. An empty wildcardIssuerKey classifies every cert
// as CertKindOnDemand (the conservative default for a misconfigured
// daemon — operators see the per-host panel populated, just without
// the kind split).
//
// m may be nil for the unit-test path; SetTLSCertExpiry /
// ObserveHostCertExpiry / ObserveCertExpiryRefresherWalkResult are
// all nil-safe.
//
// log receives a single Warn per transient error (a single unparseable
// PEM, a directory-not-empty on rotation) and nothing else — a healthy
// refresher is silent.
func StartCertExpiryRefresher(ctx context.Context, storageDir string, m *Metrics, interval time.Duration, wildcardIssuerKey string, log *slog.Logger) (stop func()) {
	// Mirror pkg/gateway/idle.go's ticker pattern: one ticker, one done
	// channel that ctx propagates. The stop() closure channels into
	// done; the goroutine exits on whichever fires first.
	done := make(chan struct{})
	if log == nil {
		log = slog.Default()
	}

	go func() {
		// First tick fires after one interval, not immediately. The
		// daemon typically has zero certs at boot anyway; firing
		// instantly would log a "no certs found" on every restart.
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				if err := refreshCertExpiryOnce(ctx, storageDir, wildcardIssuerKey, m, log); err != nil {
					log.Warn("gateway: cert expiry refresh", "err", err)
				}
			}
		}
	}()

	return func() {
		// Non-blocking close so a stop() after ctx-cancel doesn't wedge.
		// done is buffered-by-channel-closure semantics — a receive on a
		// closed channel returns immediately, which is what we want.
		select {
		case <-done:
		default:
			close(done)
		}
	}
}

// refreshCertExpiryOnce is the per-tick body of StartCertExpiryRefresher.
// Split out so the unit test can drive a single pass without spinning a
// ticker. Returns a non-nil error only when the storage walk itself
// fails (storageDir missing, permission denied); per-cert parse errors
// are logged inside walkCerts and do not fail the call.
//
// Walk completeness policy (Finding 2):
//   - root missing or walk aborted before the first cert → result="empty";
//     aggregate untouched, per-host gauges NaN'd if previously set.
//   - walk returned an error after parsing ≥1 cert → result="partial";
//     aggregate AND per-host gauges UNTOUCHED (a transient FS blip must
//     not page; the partial counter is the page signal instead).
//   - walk succeeded with ≥1 cert → result="complete"; aggregate and
//     per-host gauges updated; stale per-host gauges NaN'd.
func refreshCertExpiryOnce(ctx context.Context, storageDir, wildcardIssuerKey string, m *Metrics, log *slog.Logger) error {
	if storageDir == "" {
		return errors.New("gateway: empty cert storage dir")
	}
	certsRoot := filepath.Join(storageDir, certmagicCertDir)
	hosts, soonest, walkResult, err := walkCerts(ctx, certsRoot, wildcardIssuerKey, log)

	// Always tick the walk-result counter first so even a degraded
	// tick leaves the operator-visible signal. The counter is the
	// page signal on `result="partial"`; err surfaces to the daemon
	// slog via StartCertExpiryRefresher's `Warn` so the operator gets
	// BOTH signals (counter page + slog Warn). Skipping the counter on
	// a returned error would mask a partial behind a transient log
	// line — wrong default for a page-grade signal.
	if m != nil {
		m.ObserveCertExpiryRefresherWalkResult(walkResult)
	}

	switch walkResult {
	case walkResultEmpty:
		// No certs on disk (boot or post-cutover state). Leave the
		// aggregate untouched. NaN every previously-observed per-host
		// gauge so the panel "drains" instead of showing stale values.
		if m != nil {
			nanAllAdmittedHostCertExpiry(m, hosts, log)
		}

	case walkResultPartial:
		// Walk errored after partial success — do NOT mutate gauges.
		// The counter just ticked `partial`; the alert's
		// `increase(...{result="partial"}[1h]) > 0` is the page signal.
		// Operators investigate BEFORE certs actually expire.
		if log != nil {
			log.Warn("gateway: cert expiry walk was partial; leaving gauges untouched",
				"observed_hosts", len(hosts))
		}

	case walkResultComplete:
		// Full success — write aggregate AND per-host. NaN stale hosts
		// (present in the prior admitted snapshot but absent here).
		// time.Until(soonest) is the time delta to the soonest-
		// expiring cert; it goes negative when a cert on disk is
		// already past its NotAfter. We deliberately do NOT clamp
		// to 0: the gauge should report the actual delta
		// (negative = expired) so the page rule's `< 14 * 86400`
		// fires without ambiguity.
		remaining := time.Until(soonest)
		if m != nil {
			m.SetTLSCertExpiry(remaining)
			// Track current admitted hosts so we can NaN the
			// stale ones at the end of the tick.
			current := make(map[string]struct{}, len(hosts))
			for _, h := range hosts {
				m.ObserveHostCertExpiry(h.hostname, string(h.kind), time.Until(h.notAfter))
				current[h.hostname] = struct{}{}
			}
			nanStaleAdmittedHostCertExpiry(m, current, log)
		}

	default:
		// walkCerts returns one of the three values above; this is
		// defensive against a future enum widening.
		if log != nil {
			log.Warn("gateway: cert expiry walk returned unknown result", "result", walkResult)
		}
	}
	return err
}

// walkCerts enumerates every .crt file under certsRoot and returns:
//   - hosts: the per-host snapshot (possibly empty);
//   - soonest: the soonest NotAfter across parsed certs (zero when empty);
//   - walkResult: walkResultComplete | walkResultPartial | walkResultEmpty
//     (see refreshCertExpiryOnce);
//   - err: non-nil only when the walk itself fails unrecoverably.
//
// walkResult constants — Finding 2: a single source-of-truth for
// the counter's {result} label set so goconst stops nagging and a
// future enum widening lives in one place. The literals appear in
// walkCerts / refreshCertExpiryOnce / alerting-runbook docs; the
// runbooks are unchanged because they're rendered text — they keep
// the human-readable form ("complete" / "partial" / "empty").
const (
	walkResultComplete = "complete"
	walkResultPartial  = "partial"
	walkResultEmpty    = "empty"
)

// walkDirFn is the package-level seam for the dir-walker so unit
// tests can inject a wrapper that returns errors mid-walk (and so
// exercise the partial-walk branch). Production keeps the default,
// filepath.WalkDir; tests reset it via t.Cleanup in
// TestWalkCerts_PartialError.
var walkDirFn = filepath.WalkDir

// Per-cert parse errors are logged + skipped so a single broken PEM
// does not stop the gauge from refreshing. A missing root is
// "empty" (not an error). A disappearing subtree mid-walk is logged
// + skipped (SkipDir, not SkipAll) so the rest of the walk continues
// — the result is "partial" if ≥1 cert parsed before the error, else
// "empty" if nothing parsed.
func walkCerts(ctx context.Context, certsRoot, wildcardIssuerKey string, log *slog.Logger) (hosts []hostCert, soonest time.Time, walkResult string, err error) {
	if log == nil {
		log = slog.Default()
	}
	_, statErr := os.Stat(certsRoot)
	if errors.Is(statErr, fs.ErrNotExist) {
		return nil, time.Time{}, walkResultEmpty, nil
	}
	if statErr != nil {
		return nil, time.Time{}, walkResultEmpty, statErr
	}

	// Initial sentinel: year-9999 so the first real parse always wins
	// (Go's time.Time comparison uses ns arithmetic that overflows at
	// MaxInt64, so a math.MaxInt64 sentinel is unsafe).
	soonest = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	var (
		parsed     int
		walkFailed bool
	)
	walkErr := walkDirFn(certsRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A subdirectory disappeared mid-walk (concurrent renewal
			// by certmagic's own goroutine). SkipDir so the rest of
			// the walk continues — the aggregate is computed across
			// what we DID parse; the walk-result classifier marks
			// this "partial" if ≥1 cert was parsed, "empty" otherwise.
			if errors.Is(walkErr, fs.ErrNotExist) {
				walkFailed = true
				return filepath.SkipDir
			}
			walkFailed = true
			return walkErr
		}
		if ctx.Err() != nil {
			walkFailed = true
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".crt") {
			return nil
		}
		// The certmagic layout is .../<issuerKey>/<domain>/<domain>.crt.
		// We can derive both from the walk path: <domain> from the
		// parent dir name, <issuerKey> from the grandparent dir name.
		hostname := filepath.Base(filepath.Dir(path))
		issuerKey := filepath.Base(filepath.Dir(filepath.Dir(path)))
		na, parseErr := parseCertNotAfter(path)
		if parseErr != nil {
			log.Warn("gateway: skip cert (parse failed)", "path", path, "err", parseErr)
			return nil
		}
		parsed++
		if na.Before(soonest) {
			soonest = na
		}
		hosts = append(hosts, hostCert{
			hostname: hostname,
			kind:     classifyByIssuerKey(issuerKey, wildcardIssuerKey),
			notAfter: na,
		})
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		// Any non-ErrNotExist walkErr means the walker returned a
		// real error (e.g. fs.ErrPermission on a subtree, or an
		// injected test seam error). Mark the walk as failed so the
		// walk-result classifier sees walkResultPartial when ≥1 cert
		// parsed before the failure. Surface the error to the caller
		// so the daemon's slog records it; we still want the partial
		// snapshot we collected.
		walkFailed = true
		err = walkErr
	}

	switch {
	case parsed == 0 && walkFailed:
		// Walk failed before parsing anything — treat as "empty".
		walkResult = walkResultEmpty
	case parsed == 0:
		// Walk succeeded but no certs exist yet — "empty".
		walkResult = walkResultEmpty
	case walkFailed:
		// Walk succeeded for some certs and failed for others —
		// "partial". refreshCertExpiryOnce leaves gauges untouched
		// and the partial counter ticks.
		walkResult = walkResultPartial
	default:
		walkResult = walkResultComplete
	}

	if parsed == 0 {
		return nil, time.Time{}, walkResult, err
	}
	return hosts, soonest, walkResult, err
}

// classifyByIssuerKey maps a certmagic issuer-key path to a CertKind.
// The wildcard issuer key (e.g. "acme-v02.api.letsencrypt.org-directory")
// mints the *.apps.<zone> cert via DNS-01. Anything else with a
// well-known on-demand issuer pattern is CertKindOnDemand; the rest
// (future issuer types) are CertKindUnknown so the alert rules can
// exclude them.
//
// An empty wildcardIssuerKey classifies every cert as CertKindOnDemand
// (conservative default for a misconfigured daemon — operators see
// the per-host panel populated, just without the kind split).
func classifyByIssuerKey(issuerKey, wildcardIssuerKey string) CertKind {
	if wildcardIssuerKey != "" && issuerKey == wildcardIssuerKey {
		return CertKindWildcard
	}
	// certmagic's on-demand issuer keys all start with "acme-" in
	// the version we vendor (certmagic v0.25.4). Anything else is
	// unknown; operators can audit the wildcardIssuerKey wiring if
	// a cert they expect to be "wildcard" comes back as "unknown".
	if strings.HasPrefix(issuerKey, "acme-") {
		return CertKindOnDemand
	}
	return CertKindUnknown
}

// nanStaleAdmittedHostCertExpiry walks the hostnameLabelSet's
// admitted snapshot and DELETES every per-host gauge whose
// hostname is NOT in the current tick's `keep` set. Deletion is
// the Prometheus-canonical way to drop a labelled series — the
// exposition drops absent series entirely, so the alert's <
// expression returns false for the missing series and the gauge
// "drains" instead of carrying a stale expiry into the next tick.
//
// We use Delete rather than NaN because the prometheus client_golang
// library does NOT drop NaN series — it keeps them with value=NaN,
// and only the exposition formatter skips them, which means a
// /metrics scrape and a Gather() call see different things. Deletion
// is unambiguous across both paths.
//
// The (hostname, kind) tuple to delete is read from m.hostKinds
// (the side-channel populated by ObserveHostCertExpiry) so the
// tuple matches exactly what was written — DeleteLabelValues is
// tuple-keyed, so "drop.example.com" written as "ondemand" must be
// deleted as ("drop.example.com", "ondemand"), not
// ("drop.example.com", "unknown").
//
// Caller passes the current admitted set (`keep`) and the metrics
// bundle. nil-safe on m.
func nanStaleAdmittedHostCertExpiry(m *Metrics, keep map[string]struct{}, log *slog.Logger) {
	if m == nil || m.hostnameLabels == nil {
		return
	}
	previous := m.hostnameLabels.snapshot()
	for hostname := range previous {
		if _, ok := keep[hostname]; ok {
			continue
		}
		// Skip reserved labels — they never correspond to a real
		// host, so no per-host gauge exists for them. The bounded-
		// admission overflow bucket doesn't get per-host entries
		// because callers see the overflow as a single label, not
		// a host.
		if hostname == otherHostnameLabel || hostname == anonymousHostnameLabel {
			continue
		}
		// Resolve the exact (hostname, kind) tuple the refresher
		// wrote; fall back to CertKindUnknown if the side-channel
		// was never populated (e.g. a Metrics constructed outside
		// NewMetrics).
		kind := string(CertKindUnknown)
		if k, ok := m.hostKinds[hostname]; ok {
			kind = k
		}
		m.DeleteHostCertExpiry(hostname, kind)
		if log != nil {
			log.Debug("gateway: cert expiry delete stale host", "hostname", hostname, "kind", kind)
		}
	}
}

// nanAllAdmittedHostCertExpiry is the empty-tick companion to
// nanStaleAdmittedHostCertExpiry: passes an empty keep set so every
// previously-admitted host is NaN'd. Used when the walk result is
// "empty" so a freshly-cut-over box (or a daemon that just lost its
// storage dir) doesn't carry stale per-host gauges into the next tick.
func nanAllAdmittedHostCertExpiry(m *Metrics, _ []hostCert, log *slog.Logger) {
	nanStaleAdmittedHostCertExpiry(m, map[string]struct{}{}, log)
}

// parseCertNotAfter decodes the first PEM block in path and returns the
// leaf cert's NotAfter. We use x509.ParseCertificate directly (not
// tls.X509KeyPair, which would also need a private key) — NotAfter is
// a per-leaf field, not a chain field, so chain validation is wasted
// here. A bundle-style file (fullchain.pem) hits the first CERTIFICATE
// block which is the leaf in standard certmagic ordering, matching
// SiteCert's single-cert write.
//
// Returns a non-nil error on read/decode failure or PEM-block-not-found;
// the caller logs and continues so a single broken PEM does not stall
// the gauge refresh.
func parseCertNotAfter(path string) (time.Time, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	// pem.Decode returns the first PEM block; for a single-cert .crt
	// written by certmagic's SiteCert that's the leaf. For a hypothetical
	// bundle-style file, the leaf is the first block by RFC 5246 ordering.
	block, _ := pem.Decode(raw)
	if block == nil {
		return time.Time{}, errors.New("no PEM block found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}
