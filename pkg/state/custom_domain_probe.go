package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrStaleCustomDomainProbe = errors.New("state: custom-domain probe belongs to an obsolete claim")

// ErrStaleCustomDomainObservation prevents delayed DNS results from publishing
// lifecycle changes or doctor side effects after a newer observation. It also
// matches ErrStaleCustomDomainProbe so callers discard the entire old probe.
var ErrStaleCustomDomainObservation = fmt.Errorf("state: custom-domain observation superseded: %w", ErrStaleCustomDomainProbe)

type customDomainProbeKey struct{}

// WithCustomDomainProbe binds subsequent observations and lifecycle writes to
// the claim whose DNS was checked. The value is immutable and grants no access.
func WithCustomDomainProbe(ctx context.Context, claim CustomDomain) context.Context {
	return context.WithValue(ctx, customDomainProbeKey{}, claim)
}

func CustomDomainProbe(ctx context.Context) (CustomDomain, bool) {
	claim, ok := ctx.Value(customDomainProbeKey{}).(CustomDomain)
	return claim, ok
}

func SameCustomDomainClaim(a, b CustomDomain) bool {
	return strings.EqualFold(a.Domain, b.Domain) && a.AppID == b.AppID && a.EnvironmentID == b.EnvironmentID && a.ChallengeToken == b.ChallengeToken
}

func customDomainProbeMatches(ctx context.Context, current CustomDomain) bool {
	expected, bound := CustomDomainProbe(ctx)
	return !bound || SameCustomDomainClaim(expected, current)
}

func probeDomainMatches(expected CustomDomain, domain string) error {
	if !strings.EqualFold(expected.Domain, domain) {
		return ErrStaleCustomDomainProbe
	}
	return nil
}

func (s *PgStore) updateCustomDomainProbeCert(ctx context.Context, claim CustomDomain, domain string, status CustomDomainCertStatus, expires time.Time, lastError string, checked time.Time) error {
	bound := claim.Domain != ""
	if bound {
		if err := probeDomainMatches(claim, domain); err != nil {
			return err
		}
	}
	n, err := sqlc.New().UpdateCustomDomainProbeCert(ctx, s.pool, sqlc.UpdateCustomDomainProbeCertParams{
		Domain: domain, AppID: claim.AppID, Token: claim.ChallengeToken, Status: string(status),
		ExpiresAt: pgtypeFromTime(expires), LastError: lastError, CheckedAt: pgtypeFromTime(checked),
		ClaimBound: bound,
	})
	if err != nil {
		return fmt.Errorf("update custom-domain probe certificate: %w", err)
	}
	if n == 0 {
		if !bound {
			_, err := s.DomainByName(ctx, domain)
			return err
		}
		current, err := sqlc.New().ReadCustomDomainProbeClaim(ctx, s.pool, sqlc.ReadCustomDomainProbeClaimParams{Domain: domain, AppID: claim.AppID, Token: claim.ChallengeToken})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaleCustomDomainProbe
		}
		if err == nil && current.DnsLastCheckedAt.Valid && (checked.IsZero() || current.DnsLastCheckedAt.Time.After(checked)) {
			return ErrStaleCustomDomainObservation
		}
		return err
	}
	return nil
}

func (s *PgStore) checkCustomDomainProbeClaim(ctx context.Context, claim CustomDomain) error {
	_, err := sqlc.New().ReadCustomDomainProbeClaim(ctx, s.pool, sqlc.ReadCustomDomainProbeClaimParams{Domain: claim.Domain, AppID: claim.AppID, Token: claim.ChallengeToken})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleCustomDomainProbe
	}
	return err
}

// CustomDomainVerificationCheckStore records a TXT miss without resetting
// certificate state established by another verifier or the certificate engine.
type CustomDomainVerificationCheckStore interface {
	RecordCustomDomainVerificationCheck(context.Context, CustomDomain, time.Time) error
}

func (s *PgStore) RecordCustomDomainVerificationCheck(ctx context.Context, claim CustomDomain, checked time.Time) error {
	n, err := sqlc.New().RecordCustomDomainVerificationCheck(ctx, s.pool, sqlc.RecordCustomDomainVerificationCheckParams{Domain: claim.Domain, AppID: claim.AppID, Token: claim.ChallengeToken, CheckedAt: pgtypeFromTime(checked)})
	if err != nil {
		return fmt.Errorf("record domain verification check: %w", err)
	}
	if n == 0 {
		return s.checkCustomDomainProbeClaim(ctx, claim)
	}
	return nil
}

func (m *MemStore) RecordCustomDomainVerificationCheck(_ context.Context, claim CustomDomain, checked time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[claim.Domain]
	if !ok || !SameCustomDomainClaim(claim, d) {
		return ErrStaleCustomDomainProbe
	}
	if !d.Verified() && !d.DNSLastCheckedAt.After(checked) {
		d.DNSLastCheckedAt = checked
		m.domains[d.Domain] = d
	}
	return nil
}

func (s *PgStore) markCustomDomainProbeDrifted(ctx context.Context, claim CustomDomain, domain string, checked time.Time, reason string) (bool, error) {
	if err := probeDomainMatches(claim, domain); err != nil {
		return false, err
	}
	queries := sqlc.New()
	current, err := queries.ReadCustomDomainProbeClaim(ctx, s.pool, sqlc.ReadCustomDomainProbeClaimParams{Domain: domain, AppID: claim.AppID, Token: claim.ChallengeToken})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrStaleCustomDomainProbe
	}
	if err != nil {
		return false, err
	}
	if current.DnsLastCheckedAt.Valid && current.DnsLastCheckedAt.Time.After(checked) {
		return false, ErrStaleCustomDomainObservation
	}
	if !current.Verified && current.Status != string(CustomDomainCertDNSDrifted) {
		return false, nil
	}
	n, err := queries.MarkCustomDomainProbeDrifted(ctx, s.pool, sqlc.MarkCustomDomainProbeDriftedParams{
		Domain: domain, AppID: claim.AppID, Token: claim.ChallengeToken, CheckedAt: pgtypeFromTime(checked), Reason: reason, Transition: current.Verified,
	})
	if err != nil {
		return false, err
	}
	if n == 0 {
		// The conditional UPDATE can lose to a newer observation or replacement.
		latest, err := queries.ReadCustomDomainProbeClaim(ctx, s.pool, sqlc.ReadCustomDomainProbeClaimParams{Domain: domain, AppID: claim.AppID, Token: claim.ChallengeToken})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrStaleCustomDomainProbe
		}
		if err == nil && latest.DnsLastCheckedAt.Valid && latest.DnsLastCheckedAt.Time.After(checked) {
			return false, ErrStaleCustomDomainObservation
		}
		return false, err
	}
	return current.Verified, nil
}

func (s *PgStore) upsertCustomDomainProbe(ctx context.Context, claim CustomDomain, obs DomainDoctorObservation) error {
	if err := probeDomainMatches(claim, obs.Domain); err != nil {
		return err
	}
	var surfaceID any
	if obs.SurfaceID != "" {
		surfaceID = obs.SurfaceID
	}
	data, err := json.Marshal(map[string]any{
		"surface_id": surfaceID, "observed_at": obs.ObservedAt,
		"dns_record_found": obs.DNSRecordFound, "points_to_gregale": obs.PointsToGregale,
		"caa_permits": obs.CAAPermits, "ipv6_conflict": obs.IPv6Conflict,
		"observed_target": nullableStr(obs.ObservedTarget), "observed_aaaa": nullableStr(obs.ObservedAAAA), "caa_observed": nullableStr(obs.CAAObserved),
		"cert_state": obs.CertState, "cert_not_after": nullableTime(obs.CertNotAfter), "last_error": nullableStr(obs.LastError),
		"dns_checked_at": nullableTime(obs.DNSCheckedAt), "cert_checked_at": nullableTime(obs.CertCheckedAt),
	})
	if err != nil {
		return fmt.Errorf("encode custom-domain observation: %w", err)
	}
	n, err := sqlc.New().UpsertCustomDomainProbeObservation(ctx, s.pool, sqlc.UpsertCustomDomainProbeObservationParams{Domain: obs.Domain, AppID: claim.AppID, Token: claim.ChallengeToken, Observation: data})
	if err != nil {
		return err
	}
	if n == 0 {
		if err := s.checkCustomDomainProbeClaim(ctx, claim); err != nil {
			return err
		}
		return ErrStaleCustomDomainObservation
	}
	return nil
}

func staleDoctorObservation(current, next DomainDoctorObservation) bool {
	return next.ObservedAt.Before(current.ObservedAt) ||
		(!next.DNSCheckedAt.IsZero() && next.DNSCheckedAt.Before(current.DNSCheckedAt)) ||
		(!next.CertCheckedAt.IsZero() && next.CertCheckedAt.Before(current.CertCheckedAt))
}

func (s *PgStore) readCustomDomainProbe(ctx context.Context, claim CustomDomain, domain string) (DomainDoctorObservation, error) {
	if err := probeDomainMatches(claim, domain); err != nil {
		return DomainDoctorObservation{}, err
	}
	data, err := sqlc.New().ReadCustomDomainProbeObservation(ctx, s.pool, sqlc.ReadCustomDomainProbeObservationParams{Domain: domain, AppID: claim.AppID, Token: claim.ChallengeToken})
	if errors.Is(err, pgx.ErrNoRows) {
		return DomainDoctorObservation{}, ErrStaleCustomDomainProbe
	}
	if err == nil && (len(data) == 0 || string(data) == "null") {
		return DomainDoctorObservation{}, ErrNotFound
	}
	return decodePublicHostJSON[DomainDoctorObservation](data, err)
}
