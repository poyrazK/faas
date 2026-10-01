package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) bridgeActivity(session devbridge.Session) devbridge.Activity {
	if s.devBridgeObserver != nil {
		return s.devBridgeObserver.Snapshot(session)
	}
	status := "unknown"
	if session.RevokedAt != nil {
		status = "revoked"
	} else if !time.Now().Before(session.ExpiresAt) {
		status = "expired"
	}
	return devbridge.Activity{ConnectionState: status, Requests: []devbridge.RequestRecord{}}
}

func (s *server) listDevBridges(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.devBridgeStore(w)
	if !ok {
		return
	}
	sessions, err := store.ListDevBridges(r.Context(), acct.ID, api.DevBridgeInventoryLimit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list development bridges"))
		return
	}
	out := api.ListDevBridgesResponse{Sessions: make([]api.DevBridgeSessionSummary, 0, len(sessions))}
	for _, session := range sessions {
		out.Sessions = append(out.Sessions, api.DevBridgeSessionSummary{Session: session, ConnectionState: s.bridgeActivity(session).ConnectionState})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getDevBridgeActivity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.devBridgeStore(w)
	if !ok {
		return
	}
	session, err := store.DevBridgeByID(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such bridge session")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.bridgeActivity(session))
}

func (s *server) observeDevBridgeProxy(proxyTransport http.RoundTripper, r *http.Request) http.RoundTripper {
	if s.devBridgeObserver == nil || len(r.Header.Values(devbridge.AccountHeader)) != 1 || len(r.Header.Values(devbridge.TokenHeader)) != 1 {
		return proxyTransport
	}
	store, ok := s.store.(state.DevBridgeStore)
	if !ok {
		return proxyTransport
	}
	session, err := store.DevBridgeByID(r.Context(), r.Header.Get(devbridge.AccountHeader), r.PathValue("id"))
	if err != nil {
		return proxyTransport
	}
	if r.Pattern == "GET /v1/dev/bridges/{id}/connect" && session.AuthorizeAttachment(time.Now(), r.Header.Get(devbridge.TokenHeader)) == nil {
		return s.devBridgeObserver.ConnectionTransport(session, proxyTransport)
	}
	if r.Pattern == "/v1/dev/bridges/{id}/traffic/{path...}" && session.AuthorizeRequest(time.Now(), r.Header.Get(devbridge.TokenHeader), session.Scope.AccountID, session.Scope.EnvironmentID, session.Scope.TargetAppID) == nil {
		return s.devBridgeObserver.RequestTransport(session, proxyTransport)
	}
	return proxyTransport
}
