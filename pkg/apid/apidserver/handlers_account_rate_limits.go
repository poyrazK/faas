package apidserver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) accountDeployRateStore() (state.AccountDeployRateStore, bool) {
	store, ok := s.store.(state.AccountDeployRateStore)
	return store, ok
}

func accountDeployRateDTO(snapshot state.AccountDeployRateSnapshot) api.AccountDeployRateLimit {
	return api.AccountDeployRateLimit{
		Used:           snapshot.Used,
		Limit:          snapshot.Limit,
		Remaining:      snapshot.Remaining,
		WindowResetsAt: snapshot.WindowResetsAt.UTC(),
	}
}

func deployRateResetSeconds(snapshot state.AccountDeployRateSnapshot, now time.Time) int {
	seconds := int(math.Ceil(snapshot.WindowResetsAt.Sub(now).Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

func writeDeployRateHeaders(h http.Header, snapshot state.AccountDeployRateSnapshot, now time.Time) {
	h.Set("RateLimit-Limit", strconv.Itoa(snapshot.Limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(snapshot.Remaining))
	h.Set("RateLimit-Reset", strconv.Itoa(deployRateResetSeconds(snapshot, now)))
}

func (s *server) readAccountDeployRate(ctx context.Context, acct state.Account, now time.Time) (state.AccountDeployRateSnapshot, error) {
	store, ok := s.accountDeployRateStore()
	if !ok {
		return state.AccountDeployRateSnapshot{}, errors.New("account deploy rate store is unavailable")
	}
	return store.ReadAccountDeployRate(ctx, acct.ResourceOwnerID(), acct.Plan.DeploysPerHour(), now)
}

func (s *server) getAccountRateLimits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	now := timeNow().UTC()
	snapshot, err := s.readAccountDeployRate(r.Context(), acct, now)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "read account deploy rate limits",
			"Gregale could not load this account's deployment limits.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return
	}
	writeJSON(w, http.StatusOK, api.AccountRateLimitsResponse{Deploys: accountDeployRateDTO(snapshot)})
}

func (s *server) admitAccountDeploy(w http.ResponseWriter, r *http.Request, acct state.Account) bool {
	// Spec §4.7: past_due blocks deploys. Account.Active admits
	// past_due (its apps keep serving), and nothing else refused a
	// deploy, so a customer who stopped paying could keep deploying.
	if !acct.MayDeploy() {
		api.WriteProblem(w, acct.DeployBlockedProblem())
		return false
	}
	// Every deploy route also wraps requireVerifiedEmail; this keeps
	// the rule for paths that reach admission another way (retries).
	if !actorAccount(r, acct).EmailVerified() {
		api.WriteProblem(w, api.ErrEmailVerificationRequired())
		return false
	}
	now := timeNow().UTC()
	snapshot, err := s.consumeAccountDeployRate(r.Context(), acct, now)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "apply account deploy rate limits",
			"Gregale could not check this account's deployment limit.",
			"Retry the deployment in a moment; if it continues, contact support.", err)
		return false
	}
	writeDeployRateHeaders(w.Header(), snapshot, now)
	if !snapshot.Allowed {
		api.WriteProblem(w, api.ErrDeployRateLimited(snapshot.Limit, deployRateResetSeconds(snapshot, now)))
		return false
	}
	return true
}

// projectDeployRateShortfall reports, as a customer message, that a project
// deploy of need services does not fit the account's remaining hourly
// deploys; empty when it fits or the budget cannot be read (each service's
// own admission still applies). Every service takes one deploy, and admitting
// them one by one deployed a project partly and left it on mixed releases
// (ADR-20261105095300).
func (s *server) projectDeployRateShortfall(ctx context.Context, acct state.Account, need int, now time.Time) string {
	store, ok := s.accountDeployRateStore()
	if !ok || need <= 1 {
		return ""
	}
	snapshot, err := store.ReadAccountDeployRate(ctx, acct.ResourceOwnerID(), acct.Plan.DeploysPerHour(), now)
	if err != nil || snapshot.Remaining >= need {
		return ""
	}
	return fmt.Sprintf("not deployed: this deploy needs %d deploys for its services and the account has %d of %d left this hour (resets at %s); deploy again then, or deploy fewer services with --only",
		need, snapshot.Remaining, snapshot.Limit, snapshot.WindowResetsAt.UTC().Format(time.RFC3339))
}

// reserveProjectDeployRate takes need deploys of the account's hourly window
// for a project deploy, all or none. reserved reports that the services must
// not consume the window again; a shortfall names why none deploy. Without a
// reserving store it falls back to the read-then-consume shortfall check.
func (s *server) reserveProjectDeployRate(ctx context.Context, acct state.Account, need int, now time.Time) (shortfall string, reserved bool) {
	store, ok := s.store.(state.AccountDeployRateReserver)
	if !ok || need <= 1 {
		return s.projectDeployRateShortfall(ctx, acct, need, now), false
	}
	snapshot, err := store.ReserveAccountDeployRate(ctx, acct.ResourceOwnerID(), acct.Plan.DeploysPerHour(), need, now)
	if err != nil {
		if s.log != nil {
			s.log.Warn("apid: reserve project deploy rate", "account_id", acct.ID, "need", need, "err", err)
		}
		return "not deployed: Gregale could not check this account's deploy limit; deploy again in a moment", false
	}
	if !snapshot.Allowed {
		return fmt.Sprintf("not deployed: this deploy needs %d deploys for its services and the account has %d of %d left this hour (resets at %s); deploy again then, or deploy fewer services with --only",
			need, snapshot.Remaining, snapshot.Limit, snapshot.WindowResetsAt.UTC().Format(time.RFC3339)), false
	}
	return "", true
}

func (s *server) consumeAccountDeployRate(ctx context.Context, acct state.Account, now time.Time) (state.AccountDeployRateSnapshot, error) {
	store, ok := s.accountDeployRateStore()
	if !ok {
		return state.AccountDeployRateSnapshot{}, errors.New("account deploy rate store is unavailable")
	}
	return store.ConsumeAccountDeployRate(ctx, acct.ResourceOwnerID(), acct.Plan.DeploysPerHour(), now)
}
