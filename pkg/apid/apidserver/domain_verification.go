package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/domainverify"
	"github.com/onebox-faas/faas/pkg/state"
)

// checkDomainNow shares the poller's claim-bound ownership verifier and the
// doctor's routing/TLS probes. The durable cooldown covers replicas; Checker
// coalesces waiters and bounds the number of live manual probes in this process.
func (s *server) checkDomainNow(ctx context.Context, d state.CustomDomain) error {
	if !d.ClaimLive(time.Now()) {
		return nil
	}
	key := d.Domain + "/" + d.AppID + "/" + d.EnvironmentID + "/" + d.BindingVersion + "/" + d.ChallengeToken
	return s.domainChecks.Do(ctx, key, api.DomainVerificationManualWorkers, api.DomainVerificationManualTimeout, func(ctx context.Context) error {
		store, ok := s.store.(state.CustomDomainManualCheckStore)
		if !ok {
			return errors.New("manual domain verification unavailable")
		}
		allowed, err := store.BeginCustomDomainManualCheck(ctx, d, time.Now(), api.DomainVerificationManualCooldown)
		if err != nil || !allowed {
			return err
		}
		ctx = state.WithCustomDomainProbe(ctx, d)
		if !d.Verified() {
			s.verifyCustomDomainOwnership(ctx, s.log, d, false)
		}
		current, err := s.store.DomainByName(ctx, d.Domain)
		if err != nil {
			return err
		}
		if !state.SameCustomDomainClaim(d, current) {
			return state.ErrStaleCustomDomainProbe
		}
		return s.runDoctorForDomain(state.WithCustomDomainProbe(ctx, current), s.log, d.Domain)
	})
}

func (s *server) manualDomainCheckProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrStaleCustomDomainProbe) || errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "domain binding changed; refresh before checking again")
		return
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(api.DomainVerificationManualCooldown/time.Second)))
	detail := "could not complete domain check; retry shortly"
	if errors.Is(err, domainverify.ErrBusy) {
		detail = "domain check workers are busy; retry shortly"
	}
	api.WriteProblem(w, api.ErrCapacity(detail))
}

// Cached responses never cause network probes. Old observations and observations
// superseded by a verification/claim transition cannot establish readiness.
func (s *server) decorateDomainResponse(ctx context.Context, d state.CustomDomain, resp *api.CustomDomainResponse) {
	now := time.Now()
	if len(resp.Checks) == 0 {
		if obs, err := s.store.GetDoctorObservation(state.WithCustomDomainProbe(ctx, d), d.Domain); err == nil &&
			domainverify.MatchesObservationStamp(d, obs) &&
			!obs.ObservedAt.Before(d.VerifiedAt) && now.Sub(obs.ObservedAt) < s.doctorTTL() {
			resp.Checks = doctorReportFromObs(d, obs, false).Checks
		}
	}
	if schedule, ok := s.store.(state.CustomDomainManualCheckStore); ok && d.Verified() {
		if retry, err := schedule.CustomDomainCertificateRetryAt(ctx, d); err == nil && retry.After(now) {
			resp.CertRetryAt = retry.UTC().Format(time.RFC3339Nano)
		}
	}
	readiness := resp.AssessDomainReadiness(now)
	resp.Readiness = &readiness
}
