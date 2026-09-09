package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// dnsPoller polls DNS for unverified custom-domain TXT challenges and marks
// them verified in the Store. Spec §7: customer publishes a TXT at
// _faas-verify.<domain>; apid polls and flips verified_at when it matches.
//
// This is a poll-only loop — it does NOT subscribe to pg_notify. A LISTEN
// path would replace the ticker once a domain_verify producer lands. Channel
// names use pkg/db constants to stay aligned with the apid NotifyChannels
// table.
const verifyInterval = 30 * time.Second

// startDNSPoller runs the DNS poll loop until ctx is cancelled. Caller is
// responsible for surfacing errors via the slog logger.
func startDNSPoller(ctx context.Context, s *server, log *slog.Logger) {
	if s.store == nil {
		return
	}
	go func() {
		t := time.NewTicker(verifyInterval)
		defer t.Stop()
		// Run once immediately so freshly-added domains don't wait a minute.
		s.runVerifyOnce(ctx, log)
		// ADR-120: ride the same 30 s ticker for the per-domain
		// doctor probe pass. Gated on api.DomainDoctorEnabled()
		// so an operator can disable the doctor without bouncing
		// the daemon — same dark-launch pattern as the
		// tenant-surfaces branch above. ADR-120 Tier A1: when the
		// flag is OFF, bump the skipped_flag_disabled counter so
		// an operator can correlate a fleet-wide stale-domain alert
		// with an explicit opt-out.
		if s.runtimeBool(runtimeConfigDomainDoctor, api.DomainDoctorEnabled()) {
			s.runDoctorOnce(ctx, log)
		} else {
			s.emitDoctorSkip(log)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runVerifyOnce(ctx, log)
				if s.runtimeBool(runtimeConfigDomainDoctor, api.DomainDoctorEnabled()) {
					s.runDoctorOnce(ctx, log)
				} else {
					s.emitDoctorSkip(log)
				}
			}
		}
	}()
}

func (s *server) runVerifyOnce(ctx context.Context, log *slog.Logger) {
	pending, err := s.pendingUnverifiedDomains(ctx)
	if err != nil {
		log.Warn("dns_poller: list failed", "err", err)
		return
	}
	for _, d := range pending {
		checkedAt := time.Now().UTC()
		if checkTXT(ctx, d.Domain, d.ChallengeToken) {
			if d.CertStatus == state.CustomDomainCertDNSDrifted {
				// Drifted domains must repair the routing target before the
				// TXT challenge can restore verification. This prevents a
				// stale TXT record from immediately re-enabling a wrong CNAME.
				probeDomain, _ := state.WildcardProbeHost(d.Domain)
				if pointsToG := checkPointsToGregale(ctx, probeDomain); pointsToG.Status != probeOK {
					continue
				}
			}
			if err := s.store.MarkDomainVerified(ctx, d.Domain); err != nil {
				log.Warn("dns_poller: mark verified failed", "domain", d.Domain, "err", err)
				continue
			}
			if err := s.store.UpdateCustomDomainCertStatus(ctx, d.Domain, state.CustomDomainCertPending, time.Time{}, "", checkedAt); err != nil && !errors.Is(err, state.ErrNotFound) {
				log.Warn("dns_poller: stamp domain DNS check failed", "domain", d.Domain, "err", err)
			}
			// gatewayd-internal listens for this event to eagerly mint a
			// customer-owned wildcard certificate with DNS-01.
			_ = s.notif.Notify(ctx, db.NotifyDomainVerify, `{"domain":"`+d.Domain+`"}`)
			log.Info("domain verified", "domain", d.Domain)
		} else if d.CertStatus != state.CustomDomainCertDNSDrifted {
			// Keep dns_drifted durable until the customer has both fixed the
			// target and satisfied the TXT challenge. A failed TXT lookup on
			// the next tick must not downgrade the warning back to pending.
			if err := s.store.UpdateCustomDomainCertStatus(ctx, d.Domain, state.CustomDomainCertPending, time.Time{}, "", checkedAt); err != nil && !errors.Is(err, state.ErrNotFound) {
				log.Warn("dns_poller: stamp domain DNS check failed", "domain", d.Domain, "err", err)
			}
		}
	}
	// ADR-100 / issue #879: poll tenant hostnames alongside
	// custom domains. Both use the same _faas-verify.<hostname>
	// TXT record format, so checkTXT is shared. The poller is
	// gated on api.TenantSurfacesEnabled() so a feature-flag
	// disable suppresses the LISTEN load on the poller goroutine
	// even when the table is empty.
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		return
	}
	pendingHostnames, err := s.pendingUnverifiedHostnames(ctx)
	if err != nil {
		log.Warn("dns_poller: list tenant hostnames failed", "err", err)
		return
	}
	for _, h := range pendingHostnames {
		if checkTXT(ctx, h.Hostname, h.ChallengeToken) {
			if err := s.store.MarkTenantHostnameVerified(ctx, h.Hostname); err != nil {
				log.Warn("dns_poller: mark tenant hostname verified failed", "hostname", h.Hostname, "err", err)
				continue
			}
			// tenant_surface_changed fires on the tenant_hostnames
			// UPDATE (the trigger at migrations/00243 fires on
			// every relevant column change including verified_at).
			// The gatewayd cert-remint subscriber picks it up and
			// asks the issuer for a fresh SAN-aggregated cert.
			//
			// Audit row: tenant_hostname.verified is the third
			// event of the surface lifecycle (added / removed /
			// verified). The data carries the surface_id so the
			// dashboard "verified at" timeline keys off it.
			if s.audit != nil {
				s.audit.Emit(ctx, "tenant_hostname.verified", nil, map[string]any{
					"hostname":   h.Hostname,
					"surface_id": h.SurfaceID,
				})
			}
			log.Info("tenant hostname verified", "hostname", h.Hostname, "surface", h.SurfaceID)
		}
	}
}

// pendingUnverifiedHostnames (ADR-100 / issue #879) returns the
// batch of unverified tenant hostnames due for a TXT poll. The
// batcher is ListPendingTenantHostnames — bounded by the poller
// limit (50 per pass) so a single batch doesn't dominate the
// goroutine.
func (s *server) pendingUnverifiedHostnames(ctx context.Context) ([]pendingHostnameRow, error) {
	rows, err := s.store.ListPendingTenantHostnames(ctx, time.Now(), 50)
	if err != nil {
		return nil, err
	}
	out := make([]pendingHostnameRow, len(rows))
	for i, h := range rows {
		out[i] = pendingHostnameRow{
			Hostname:       h.Hostname,
			ChallengeToken: h.ChallengeToken,
			SurfaceID:      h.SurfaceID,
		}
	}
	return out, nil
}

// pendingHostnameRow is the poller's view of an unverified
// tenant hostname. SurfaceID is logged for operator triage so a
// failed poll maps back to the customer surface without a
// second store hop.
type pendingHostnameRow struct {
	Hostname       string
	ChallengeToken string
	SurfaceID      string
}

// pendingUnverifiedDomains reads the unverified index directly. Implemented
// as a tiny helper here (rather than a Store method) because the poller
// goroutine is the only consumer.
func (s *server) pendingUnverifiedDomains(ctx context.Context) ([]pendingDomainRow, error) {
	// We can't reach a *sql.DB from server without exposing one on the
	// struct. The simpler path is to walk all apps and ListDomainsForApp,
	// which works fine at M5 scale (one-box, single-digit accounts). The
	// Store interface grows a dedicated method when this matters.
	var out []pendingDomainRow
	// Fast path: PgStore and MemStore expose the full row through this
	// optional interface. Keeping it optional preserves compatibility with
	// narrow test doubles that only implement the historical Store surface.
	type listUnverified interface {
		ListUnverifiedCustomDomains(ctx context.Context) ([]state.CustomDomain, error)
	}
	if lu, ok := s.store.(listUnverified); ok {
		domains, err := lu.ListUnverifiedCustomDomains(ctx)
		if err != nil {
			return nil, err
		}
		out = make([]pendingDomainRow, 0, len(domains))
		for _, d := range domains {
			out = append(out, pendingDomainRow{Domain: d.Domain, ChallengeToken: d.ChallengeToken, CertStatus: d.CertStatus})
		}
		return out, nil
	}
	// No compatible enumeration seam: leave the pass empty rather than
	// issuing an unbounded account/app walk from the poller goroutine.
	return out, nil
}

// pendingDomainRow is the poller's view of an unverified custom domain.
type pendingDomainRow struct {
	Domain         string
	ChallengeToken string
	CertStatus     state.CustomDomainCertStatus
}

// checkTXT does a TXT lookup for _faas-verify.<domain> and reports whether
// any returned record equals the expected token.
func checkTXT(ctx context.Context, domain, expected string) bool {
	target := state.CustomDomainChallengeName(domain)
	records, err := txtLookupFunc(ctx, target)
	if err != nil {
		return false
	}
	for _, r := range records {
		if strings.TrimSpace(r) == expected {
			return true
		}
	}
	return false
}

// txtLookupFunc is the test seam for the TXT verifier. Production
// uses the real net.Resolver; tests inject a fake that returns
// canned records. ADR-100 / issue #879: the same seam covers
// the custom-domain and tenant-hostname verification paths.
var txtLookupFunc = func(ctx context.Context, target string) ([]string, error) {
	return (&net.Resolver{}).LookupTXT(ctx, target)
}

// --- runDoctorOnce (ADR-120) ----------------------------------
//
// runDoctorOnce is the per-tick probe pass that writes
// domain_doctor_observations rows. It enumerates the union
// of custom_domains + tenant_hostnames via
// s.store.ListAllCustomDomainsForDoctor, runs the four DNS
// probes in parallel per domain, looks up the surface cert
// state (when known) or falls back to a port-443 dial
// (legacy custom_domains), and upserts a single row per
// domain. Errors per-domain are logged + skipped; the loop
// is best-effort and never aborts the whole pass on a
// single bad domain.
//
// The cert probe is deliberately NOT in the parallel fan-out
// — port-443 dials are stateful (the dialCertFunc seam
// already has ctx-cancellation wiring at dns_verify.go:86)
// and the poller can amortise the 5s budget across the
// batch by sequencing dials with a 2s per-dial cap.
//
// Per-domain ctx: each domain gets a fresh
// context.WithTimeout(ctx, probeTimeout+2s) so a slow
// upstream doesn't bleed into the next domain. The poller
// goroutine itself uses the parent ctx so a daemon shutdown
// cancels the entire pass cleanly.
func (s *server) runDoctorOnce(ctx context.Context, log *slog.Logger) {
	domains, err := s.store.ListAllCustomDomainsForDoctor(ctx)
	if err != nil {
		log.Warn("dns_poller: list domains for doctor failed", "err", err)
		return
	}
	for _, domain := range domains {
		domainCtx, cancel := context.WithTimeout(ctx, probeTimeout+2*time.Second)
		_ = s.runDoctorForDomain(domainCtx, log, domain)
		cancel()
	}
	// ADR-120 Tier A1: refresh the apid_domain_doctor_oldest_
	// observation_seconds gauge after the pass completes. The
	// gauge tracks (now − min(observed_at)) over every row, so a
	// single misbehaving domain can keep the alert quiet; the
	// dashboard renders per-domain staleness separately. We set
	// 0 when the row set is empty (cold start) and the wall-clock
	// age when rows exist. Backs FaasDomainDoctorStalled
	// (page, 30m) and FaasDomainDoctorStretched (warn, 30m) at
	// deploy/ansible/roles/prometheus/files/faas.rules.yml.
	s.emitDoctorOldestObservationGauge(ctx, log)
}

// emitDoctorOldestObservationGauge (ADR-120 Tier A1) reads
// every row in domain_doctor_observations, picks the minimum
// observed_at, and Sets apid_domain_doctor_oldest_observation_seconds
// to the wall-clock age. On an empty row set (cold start) it
// Sets 0 so the gauge never returns the stale last value after
// a deploy. Best-effort — a Store read failure is logged + the
// gauge is left untouched (the previous value is retained so a
// transient DB hiccup doesn't false-page on-call).
func (s *server) emitDoctorOldestObservationGauge(ctx context.Context, log *slog.Logger) {
	if s.ops == nil {
		return
	}
	oldest, err := s.store.OldestDoctorObservation(ctx)
	if err != nil {
		log.Warn("dns_poller: oldest doctor observation read failed", "err", err)
		return
	}
	if oldest.IsZero() {
		s.ops.DomainDoctorOldestObservationSeconds().Set(0)
		return
	}
	age := time.Since(oldest).Seconds()
	if age < 0 {
		// Clock skew between the apid host and Postgres can yield a
		// negative age; clamp to zero so the gauge never goes
		// negative (Prometheus doesn't render negative gauges well
		// and the alert's `time() − timestamp(<gauge>)` expression
		// would mis-fire).
		age = 0
	}
	s.ops.DomainDoctorOldestObservationSeconds().Set(age)
}

// emitDoctorSkip (ADR-120 Tier A1) bumps the
// apid_domain_doctor_skipped_flag_disabled_total counter so an
// operator can correlate a "doctor stale" alert with an explicit
// FAAS_DOMAIN_DOCTOR_ENABLED=false opt-out. Called once per
// dns_poller tick when the flag is off. Best-effort — nil-safe.
func (s *server) emitDoctorSkip(log *slog.Logger) {
	if s.ops == nil {
		return
	}
	s.ops.DomainDoctorSkippedFlagDisabled().Inc()
	log.Debug("doctor pass skipped — FAAS_DOMAIN_DOCTOR_ENABLED off")
}

func (s *server) runDoctorForDomain(ctx context.Context, log *slog.Logger, domain string) error {
	probeDomain, _ := state.WildcardProbeHost(domain)
	dnsFound, pointsToG, caa, aaaa := runProbesParallel(ctx, probeDomain)
	// Translate probe results into the observation row
	// shape. probeOK → true, probeFail → false, probePending
	// → false (we treat "transient" as "not currently
	// passing" so the dashboard reflects a degraded
	// posture; the next 30s tick will refresh it). probeNA
	// → true for dns_found (a CNAME-only setup is healthy
	// for the DNS check; the points_to_gregale check is
	// the load-bearing one).
	obs := state.DomainDoctorObservation{
		Domain:          domain,
		ObservedAt:      time.Now().UTC(),
		DNSRecordFound:  probeToBool(dnsFound.Status, true),
		PointsToGregale: probeToBool(pointsToG.Status, false),
		IPv6Conflict:    probeToBool(aaaa.Status, false),
		ObservedTarget:  pointsToG.Observed,
		ObservedAAAA:    aaaa.Observed,
		CAAObserved:     caa.Observed,
		DNSCheckedAt:    earliest(dnsFound.ObservedAt, pointsToG.ObservedAt, caa.ObservedAt, aaaa.ObservedAt),
	}
	// CAA is tri-state. ok = true; fail = false; pending
	// stays nil (NULL in the column, the handler renders
	// "transient" in that case so a flaky upstream doesn't
	// burn the customer's "permits" badge).
	switch caa.Status {
	case probeOK:
		v := true
		obs.CAAPermits = &v
	case probeFail:
		v := false
		obs.CAAPermits = &v
	}
	// Surface-aware cert state. Look up the tenant_surfaces
	// row via the hostname; if present, read its
	// cert_state (the cert engine is the SOLE writer per
	// CLAUDE.md ownership). If absent, the domain is a
	// legacy custom_domains row; fall back to a live
	// port-443 dial.
	if s.store != nil {
		if surface, err := s.store.TenantSurfaceByHostname(ctx, domain); err == nil {
			obs.SurfaceID = surface.ID
			obs.CertState = string(surface.CertState)
			obs.CertNotAfter = surface.CertNotAfter
		}
	}
	if obs.CertState == "" {
		var legacy state.CustomDomain
		var legacyErr error
		legacyLoaded := false
		// Legacy custom_domains path. An unverified row is still waiting
		// on the TXT challenge, so keep it pending and avoid a misleading
		// port-443 failure while DNS ownership is being established.
		legacy, legacyErr = s.store.DomainByName(ctx, domain)
		legacyLoaded = legacyErr == nil
		if legacyErr == nil && !legacy.Verified() {
			obs.CertState = certStatusPending
		} else {
			obs.CertState, obs.LastError, obs.CertNotAfter = dialCertForDoctor(ctx, probeDomain)
			obs.CertCheckedAt = time.Now().UTC()
		}

		// F3: a verified custom domain is revoked when the periodic doctor
		// sees a definite CNAME mismatch. The state mutation is optional so
		// narrow Store test doubles remain source-compatible; production
		// PgStore and MemStore both provide the atomic transition.
		dnsCheckedAt := obs.DNSCheckedAt
		if dnsCheckedAt.IsZero() {
			dnsCheckedAt = time.Now().UTC()
		}
		drifted := legacyLoaded && legacy.Verified() && pointsToG.Status == probeFail
		if legacyLoaded && !legacy.Verified() && legacy.CertStatus == state.CustomDomainCertDNSDrifted {
			// Preserve the durable drift state on subsequent passes. The TXT
			// verifier clears it by writing pending when verification succeeds.
			drifted = true
		}
		if drifted {
			reason := "DNS target no longer points to Gregale"
			if pointsToG.Observed != "" {
				reason += ": " + pointsToG.Observed
			}
			if ds, ok := s.store.(state.CustomDomainDNSDriftStore); ok {
				transitioned, err := ds.MarkCustomDomainDNSDrifted(ctx, domain, dnsCheckedAt, reason)
				if err != nil {
					log.Warn("dns_poller: mark custom-domain DNS drift failed", "domain", domain, "err", err)
					drifted = false
				} else {
					if transitioned {
						s.emitDomainDrifted(ctx, log, legacy, pointsToG, dnsCheckedAt, reason)
					}
					// Do not let the normal cert mirror overwrite dns_drifted
					// with the live dial result on this pass.
					drifted = true
				}
			} else {
				log.Warn("dns_poller: store does not support custom-domain DNS drift state", "domain", domain)
				drifted = false
			}
		}
		// F1: mirror the doctor's live cert observation onto the legacy
		// custom_domains row. This makes list/show/status useful without a
		// live dial and keeps the cert error text available for remediation.
		status := state.CustomDomainCertStatus(obs.CertState)
		switch obs.CertState {
		case certStatusCDN, certStatusDialFailed:
			status = state.CustomDomainCertFailed
		case certStatusPending:
			status = state.CustomDomainCertPending
		case certStatusIssued:
			status = state.CustomDomainCertIssued
		}
		if !drifted {
			if err := s.store.UpdateCustomDomainCertStatus(ctx, domain, status, obs.CertNotAfter, obs.LastError, dnsCheckedAt); err != nil && !errors.Is(err, state.ErrNotFound) {
				log.Warn("dns_poller: update custom-domain cert status failed", "domain", domain, "err", err)
			} else if status == state.CustomDomainCertFailed {
				if legacyErr == nil && legacy.CertStatus != state.CustomDomainCertFailed && s.ops != nil {
					if counter := s.ops.CertIssuanceFailedTotal(); counter != nil {
						counter.Inc()
					}
				}
				if err := s.sendCertIssuanceFailedEmail(ctx, log, domain); err != nil && !errors.Is(err, state.ErrNotFound) {
					log.Warn("dns_poller: certificate issuance-failed email skipped", "domain", domain, "err", err)
				}
			}
		}
	}
	if err := s.store.UpsertDoctorObservation(ctx, obs); err != nil {
		log.Warn("dns_poller: upsert doctor observation failed", "domain", domain, "err", err)
		return err
	}
	return nil
}

// emitDomainDrifted records the customer-visible transition and wakes the
// dashboard stream. The state mutation is the source of truth; both emits are
// best-effort, matching the rest of apid's audit/notify paths.
func (s *server) emitDomainDrifted(ctx context.Context, log *slog.Logger, d state.CustomDomain, probe ProbeResult, checkedAt time.Time, reason string) {
	var accountID *string
	if app, err := s.store.AppByID(ctx, d.AppID); err == nil && app.AccountID != "" {
		id := app.AccountID
		accountID = &id
	}
	data := map[string]any{
		"domain":          d.Domain,
		"app_id":          d.AppID,
		"observed_target": probe.Observed,
		"checked_at":      checkedAt.UTC().Format(time.RFC3339),
		"reason":          reason,
	}
	if s.audit != nil {
		s.audit.Emit(ctx, "domain.drifted", accountID, data)
	}
	if s.notif != nil {
		payload, err := json.Marshal(map[string]any{
			"kind":            "drifted",
			"domain":          d.Domain,
			"app_id":          d.AppID,
			"observed_target": probe.Observed,
		})
		if err != nil {
			log.Warn("dns_poller: marshal domain drift notification failed", "domain", d.Domain, "err", err)
		} else if err := s.notif.Notify(ctx, db.NotifyDomainChanged, string(payload)); err != nil {
			log.Warn("dns_poller: notify domain drift failed", "domain", d.Domain, "err", err)
		}
	}
}

// probeToBool converts a probeStatus into a bool column
// value. The defaultForOK parameter controls the
// interpretation of probeNA: dns_record_found is true on
// NA (a CNAME-only setup is a healthy DNS posture);
// points_to_gregale is false on NA (we don't know if
// they point at us; treat as not-passing so the operator
// can investigate). This keeps the doctor's 5-line
// shape boolean-only without losing the na case.
func probeToBool(s probeStatus, defaultForOK bool) bool {
	switch s {
	case probeOK, probeNA:
		return defaultForOK
	default:
		return false
	}
}

func earliest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.IsZero() {
			continue
		}
		if out.IsZero() || t.Before(out) {
			out = t
		}
	}
	return out
}

// dialCertForDoctor is a thin wrapper around dialCertFunc
// that maps the cert-dial outcome into the doctor's
// (cert_state, last_error, cert_not_after) triple.
// Reuses the existing dialCertFunc seam from
// dns_verify.go:86; the live dial + CDN detection + SAN
// check are all unchanged from the PR-3 verify path.
func dialCertForDoctor(ctx context.Context, domain string) (string, string, time.Time) {
	leaf, err := dialCert(ctx, domain)
	if err == nil {
		return certStatusIssued, "", leaf.NotAfter
	}
	if errors.Is(err, errCDNCert) {
		return certStatusCDN, err.Error(), time.Time{}
	}
	return certStatusDialFailed, err.Error(), time.Time{}
}

// Cert-state tokens rendered into the doctor's
// cert_state column. The "issued" / "pending" tokens
// re-use the existing certStatusIssued / certStatusPending
// consts at cmd/apid/dns_verify.go:73-74 to keep goconst
// from tripping on the literal "issued" / "pending" used
// in handlers_ext.go (PR-3's review fix). The "cdn" and
// "dial_failed" tokens are the doctor's surface-state
// mapping for legacy custom_domains where there's no
// tenant_surfaces row to read.
const (
	certStatusCDN        = "cdn"
	certStatusDialFailed = "dial_failed"
	certStatusFailed     = "failed"
)
