// adr: 531
package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

type managedDeadlineKey struct{}
type managedDeadlineChain struct{ chainID, accountID, receivingAppID string }

func (h *Handler) WithTrafficDeadlines(signer *trafficdeadline.Signer) *Handler {
	h.trafficDeadlines = signer
	return h
}

func newManagedDeadlineChain(ctx context.Context, accountID string) context.Context {
	if _, ok := ctx.Value(managedDeadlineKey{}).(managedDeadlineChain); ok {
		return ctx
	}
	return context.WithValue(ctx, managedDeadlineKey{}, managedDeadlineChain{chainID: uuid.NewString(), accountID: accountID})
}

func stampManagedDeadline(w http.ResponseWriter, r *http.Request, signer *trafficdeadline.Signer, appID, accountID string) bool {
	r.Header.Del(trafficdeadline.Header)
	chain, ok := r.Context().Value(managedDeadlineKey{}).(managedDeadlineChain)
	if !ok {
		return false
	}
	if chain.accountID != accountID {
		writeTrafficDeadlineError(w, r, trafficdeadline.ErrInvalid)
		return true
	}
	deadline, ok := r.Context().Deadline()
	if !ok {
		writeTrafficDeadlineError(w, r, trafficdeadline.ErrUnavailable)
		return true
	}
	token, err := signer.Mint(appID, accountID, chain.chainID, deadline)
	if err != nil {
		writeTrafficDeadlineError(w, r, err)
		return true
	}
	r.Header.Set(trafficdeadline.Header, token)
	return false
}

func (p *ServiceProxy) consumeManagedDeadline(w http.ResponseWriter, r *http.Request) bool {
	values := r.Header.Values(trafficdeadline.Header)
	r.Header.Del(trafficdeadline.Header)
	if len(values) == 0 {
		return false
	}
	if len(values) != 1 {
		writeTrafficDeadlineError(w, r, trafficdeadline.ErrInvalid)
		return true
	}
	claims, err := p.trafficDeadlines.Authenticate(values[0])
	if err != nil {
		writeTrafficDeadlineError(w, r, err)
		return true
	}
	ctx := context.WithValue(r.Context(), managedDeadlineKey{}, managedDeadlineChain{chainID: claims.ChainID, accountID: claims.AccountID, receivingAppID: claims.AppID})
	ctx, cancel, _ := reqbudget.WithStarted(ctx, claims.Issued(), claims.Deadline().Sub(claims.Issued()), time.Duration(api.MaxServiceReliabilityTimeoutMS)*time.Millisecond, "service_proxy", r.Method+":"+r.URL.Path)
	rememberBudgetCancel(r, ctx, cancel)
	return false
}

func validateManagedDeadlineCaller(w http.ResponseWriter, r *http.Request, appID string) bool {
	chain, ok := r.Context().Value(managedDeadlineKey{}).(managedDeadlineChain)
	if ok && chain.receivingAppID != appID {
		writeTrafficDeadlineError(w, r, trafficdeadline.ErrInvalid)
		return true
	}
	return false
}

func validateManagedDeadlineAccount(w http.ResponseWriter, r *http.Request, accountID string) bool {
	chain, ok := r.Context().Value(managedDeadlineKey{}).(managedDeadlineChain)
	if ok && accountID == "" {
		writeTrafficDeadlineError(w, r, trafficdeadline.ErrUnavailable)
		return true
	}
	if ok && chain.accountID != accountID {
		writeTrafficDeadlineError(w, r, trafficdeadline.ErrInvalid)
		return true
	}
	return false
}

func writeTrafficDeadlineError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, trafficdeadline.ErrExpired) {
		writeRequestBudgetExceededForRequest(w, r)
		return
	}
	status, code := http.StatusServiceUnavailable, api.CodeTrafficDeadlineUnavailable
	detail := "managed request deadline verification is unavailable; retry when the platform has recovered"
	if errors.Is(err, trafficdeadline.ErrInvalid) {
		status, code = http.StatusBadRequest, api.CodeTrafficDeadlineInvalid
		detail = "the managed request deadline carrier is invalid for this caller"
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(api.ErrorCodeHeader, code)
	api.WriteProblem(w, api.NewProblem(status, code, "Managed request deadline refused", detail))
}
