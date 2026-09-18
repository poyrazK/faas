package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// tcpListeners returns the optional listener store without widening the
// apid dependency on every narrow state.Store test double.
func (s *server) tcpListeners() (state.TCPListenerStore, bool) {
	store, ok := s.store.(state.TCPListenerStore)
	return store, ok
}

func tcpListenerResponse(listener state.TCPListener) api.TCPListenerResponse {
	return api.TCPListenerResponse{
		ID: listener.ID, Name: listener.ListenerName, GuestPort: listener.GuestPort,
		PublicPort: listener.PublicPort, Protocol: listener.Protocol,
		Enabled: listener.Enabled, CreatedAt: listener.CreatedAt, UpdatedAt: listener.UpdatedAt,
	}
}

// appDeclaresTCPListener is the control-plane mirror of the gateway's
// manifest selector. A durable public listener must not invent a guest port
// or listener name that the deployed workload did not declare. Unnamed
// manifest entries use the same deterministic tcp-<port> selector as ADR-176.
func appDeclaresTCPListener(app state.App, name string, guestPort int) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, port := range app.Manifest.Ports {
		if port.EffectiveProtocol() != api.WorkloadPortTCP || port.Port != guestPort {
			continue
		}
		declaredName := strings.ToLower(strings.TrimSpace(port.Name))
		if declaredName == "" {
			declaredName = fmt.Sprintf("tcp-%d", port.Port)
		}
		if declaredName == name {
			return true
		}
	}
	return false
}

func writeTCPListenerStoreError(w http.ResponseWriter, action string, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"TCP listener not found", "the app or listener does not exist"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"TCP listener conflict", "the listener name or public port is already in use"))
	case errors.Is(err, state.ErrInvalidTCPListener):
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid TCP listener", err.Error()))
	default:
		api.WriteProblem(w, api.ErrCapacity(fmt.Sprintf("could not %s TCP listener", action)))
	}
}

func (s *server) listAppTCPListeners(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.tcpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("TCP listeners are not available"))
		return
	}
	listeners, err := store.ListTCPListenersForApp(r.Context(), app.ID)
	if err != nil {
		writeTCPListenerStoreError(w, "list", err)
		return
	}
	out := make([]api.TCPListenerResponse, 0, len(listeners))
	for _, listener := range listeners {
		out = append(out, tcpListenerResponse(listener))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createAppTCPListener(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.tcpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("TCP listeners are not available"))
		return
	}
	var req api.CreateTCPListenerRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid TCP listener", "invalid JSON body"))
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid TCP listener", "name is required"))
		return
	}
	if err := api.ValidateWorkloadPorts([]api.WorkloadPort{{Name: name, Port: req.GuestPort, Protocol: api.WorkloadPortTCP}}); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid TCP listener", err.Error()))
		return
	}
	if !appDeclaresTCPListener(app, name, req.GuestPort) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid TCP listener", "name and guest_port must match a declared TCP listener in the app manifest"))
		return
	}
	if req.PublicPort != 0 && (req.PublicPort < state.TCPListenerPublicPortMin || req.PublicPort > state.TCPListenerPublicPortMax) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid TCP listener", fmt.Sprintf("public_port must be between %d and %d", state.TCPListenerPublicPortMin, state.TCPListenerPublicPortMax)))
		return
	}

	base := state.TCPListener{
		AppID: app.ID, AccountID: acct.ID, ListenerName: name,
		GuestPort: req.GuestPort, Protocol: "tcp", Enabled: true,
	}
	if req.PublicPort != 0 {
		base.PublicPort = req.PublicPort
		created, err := store.CreateTCPListener(r.Context(), base)
		if err != nil {
			writeTCPListenerStoreError(w, "create", err)
			return
		}
		_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
			fmt.Sprintf(`{"kind":"tcp_listener_created","app_id":"%s","listener_id":"%s"}`, app.ID, created.ID))
		writeJSON(w, http.StatusCreated, tcpListenerResponse(created))
		return
	}

	// Public ports are globally unique. Retrying only conflicts makes the
	// allocation safe under concurrent API requests without adding another
	// store-wide method solely for the allocator.
	for publicPort := state.TCPListenerPublicPortMin; publicPort <= state.TCPListenerPublicPortMax; publicPort++ {
		candidate := base
		candidate.PublicPort = publicPort
		created, err := store.CreateTCPListener(r.Context(), candidate)
		if err == nil {
			_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
				fmt.Sprintf(`{"kind":"tcp_listener_created","app_id":"%s","listener_id":"%s"}`, app.ID, created.ID))
			writeJSON(w, http.StatusCreated, tcpListenerResponse(created))
			return
		}
		if !errors.Is(err, state.ErrConflict) {
			writeTCPListenerStoreError(w, "create", err)
			return
		}
	}
	api.WriteProblem(w, api.ErrCapacity("TCP listener public port range is exhausted"))
}

func (s *server) updateAppTCPListener(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.tcpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("TCP listeners are not available"))
		return
	}
	var req api.UpdateTCPListenerRequest
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid TCP listener", "enabled is required"))
		return
	}
	listener, err := store.TCPListenerByAppAndName(r.Context(), app.ID, r.PathValue("name"))
	if err != nil {
		writeTCPListenerStoreError(w, "read", err)
		return
	}
	updated, err := store.SetTCPListenerEnabled(r.Context(), listener.ID, *req.Enabled)
	if err != nil {
		writeTCPListenerStoreError(w, "update", err)
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"tcp_listener_updated","app_id":"%s","listener_id":"%s"}`, app.ID, updated.ID))
	writeJSON(w, http.StatusOK, tcpListenerResponse(updated))
}

func (s *server) deleteAppTCPListener(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.tcpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("TCP listeners are not available"))
		return
	}
	listener, err := store.TCPListenerByAppAndName(r.Context(), app.ID, r.PathValue("name"))
	if err != nil {
		writeTCPListenerStoreError(w, "read", err)
		return
	}
	if err := store.DeleteTCPListener(r.Context(), listener.ID); err != nil {
		writeTCPListenerStoreError(w, "delete", err)
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"tcp_listener_deleted","app_id":"%s","listener_id":"%s"}`, app.ID, listener.ID))
	w.WriteHeader(http.StatusNoContent)
}
