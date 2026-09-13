package main

import (
	"context"
	"errors"
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
	return store.ReadAccountDeployRate(ctx, acct.ID, acct.Plan.DeploysPerHour(), now)
}

func (s *server) getAccountRateLimits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	now := timeNow().UTC()
	snapshot, err := s.readAccountDeployRate(r.Context(), acct, now)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("read account deploy rate: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, api.AccountRateLimitsResponse{Deploys: accountDeployRateDTO(snapshot)})
}

func (s *server) admitAccountDeploy(w http.ResponseWriter, r *http.Request, acct state.Account) bool {
	now := timeNow().UTC()
	snapshot, err := s.consumeAccountDeployRate(r.Context(), acct, now)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("consume account deploy rate: "+err.Error()))
		return false
	}
	writeDeployRateHeaders(w.Header(), snapshot, now)
	if !snapshot.Allowed {
		api.WriteProblem(w, api.ErrDeployRateLimited(snapshot.Limit, deployRateResetSeconds(snapshot, now)))
		return false
	}
	return true
}

func (s *server) consumeAccountDeployRate(ctx context.Context, acct state.Account, now time.Time) (state.AccountDeployRateSnapshot, error) {
	store, ok := s.accountDeployRateStore()
	if !ok {
		return state.AccountDeployRateSnapshot{}, errors.New("account deploy rate store is unavailable")
	}
	return store.ConsumeAccountDeployRate(ctx, acct.ID, acct.Plan.DeploysPerHour(), now)
}
