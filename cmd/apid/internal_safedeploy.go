package main

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

const internalSafeDeployMinTokenLength = 32

// Safe Deploy's automated mutations are not customer API calls. meterd
// discovers rollouts across accounts, so one account-bound API key cannot
// authorize them. These routes exist only on apid's loopback operator
// listener; a separate credential is required for each operation class.
func (s *server) mountInternalSafeDeploy(mux *http.ServeMux, listenerAddr, canaryToken, actionToken string) error {
	if canaryToken == "" && actionToken == "" {
		return nil
	}
	if len(canaryToken) < internalSafeDeployMinTokenLength || len(actionToken) < internalSafeDeployMinTokenLength || canaryToken == actionToken {
		return errors.New("apid: Safe Deploy requires two distinct service tokens of at least 32 bytes")
	}
	if !loopbackListenAddr(listenerAddr) {
		return fmt.Errorf("apid: Safe Deploy operator listener %q must bind to loopback", listenerAddr)
	}
	mux.HandleFunc("POST /v1/internal/safe-deploy/deployments/{id}/canary/advance",
		internalSafeDeployAuth(canaryToken, s.internalAdvanceCanary))
	mux.HandleFunc("POST /v1/internal/safe-deploy/apps/{slug}/rollouts/recover",
		internalSafeDeployAuth(actionToken, s.internalForApp(s.recoverRollout)))
	mux.HandleFunc("POST /v1/internal/safe-deploy/apps/{slug}/rollback",
		internalSafeDeployAuth(actionToken, s.internalForApp(s.rollbackApp)))
	return nil
}

func loopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func internalSafeDeployAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		bearer := strings.TrimPrefix(authorization, "Bearer ")
		if len(bearer) != len(token) || subtle.ConstantTimeCompare([]byte(bearer), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *server) internalAdvanceCanary(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.DeploymentByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.internalSafeDeployLookupError(w, r, err, "deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil {
		s.internalSafeDeployLookupError(w, r, err, "deployment")
		return
	}
	acct, err := s.store.AccountByID(r.Context(), app.AccountID)
	if err != nil {
		s.internalSafeDeployLookupError(w, r, err, "deployment")
		return
	}
	s.idempotent(s.advanceCanary)(w, r, acct)
}

func (s *server) internalForApp(next accountHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			s.internalSafeDeployLookupError(w, r, err, "app")
			return
		}
		acct, err := s.store.AccountByID(r.Context(), app.AccountID)
		if err != nil {
			s.internalSafeDeployLookupError(w, r, err, "app")
			return
		}
		s.idempotent(next)(w, r, acct)
	}
}

func (s *server) internalSafeDeployLookupError(w http.ResponseWriter, r *http.Request, err error, kind string) {
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such "+kind)
		return
	}
	writeCustomerInternalProblem(w, r, s.log, "load Safe Deploy "+kind,
		"Gregale could not load this rollout.",
		"Retry in a moment; if it continues, contact support.", err)
}
