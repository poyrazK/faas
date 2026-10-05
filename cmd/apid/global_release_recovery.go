package main

// adr: 608
import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) globalReleaseService() *globaldirectory.DelegationService {
	journal, ok := s.globalDirectory.(globaldirectory.DelegationStore)
	if !ok || !s.globalDelegationKeys.Available() {
		return nil
	}
	return &globaldirectory.DelegationService{
		Store: s.globalDirectory, Journal: journal, Keys: s.globalDelegationKeys,
		Verifier: s.globalVerifier, Authorize: s.globalReleaseOwner,
	}
}
func (s *server) globalReleaseOwner(ctx context.Context, id string) error {
	if roots, ok := s.globalDirectory.(globaldirectory.OwnerStore); ok {
		owner, err := roots.OwnerByID(ctx, id)
		if err != nil && !errors.Is(err, globaldirectory.ErrNotFound) {
			return err
		}
		if err == nil && owner.Shared() {
			return globaldirectory.ErrDelegationOwnerInactive
		}
		if err == nil {
			id = owner.AccountID
		}
	}
	acct, err := s.store.AccountByID(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		return globaldirectory.ErrDelegationOwnerInactive
	}
	if err != nil {
		return err
	}
	if !acct.MayDeploy() || !acct.EmailVerified() {
		return globaldirectory.ErrDelegationOwnerInactive
	}
	return nil
}
func (s *server) runGlobalReleaseRecovery(ctx context.Context) {
	service := s.globalReleaseService()
	if service == nil {
		return
	}
	worker := globaldirectory.Recovery{Service: service, OnError: func() {
		s.log.Warn("global release recovery attempt incomplete; durable work retained")
	}}
	_ = worker.Run(ctx)
}

func (s *server) globalReleaseServiceForRequest(w http.ResponseWriter, r *http.Request, acct state.Account) *globaldirectory.DelegationService {
	service := s.globalReleaseService()
	if service == nil {
		api.WriteProblem(w, api.ErrCapacity("regional delegation sealing is unavailable"))
		return nil
	}
	err := s.globalReleaseOwner(r.Context(), globalOwnerID(r, acct))
	if errors.Is(err, globaldirectory.ErrDelegationOwnerInactive) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Global release owner inactive", "An active account with verified email is required to deploy."))
		return nil
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("global release owner is temporarily unavailable"))
		return nil
	}
	return service
}
