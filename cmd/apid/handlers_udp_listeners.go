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

// udpListeners returns the optional listener store without widening the
// apid dependency on every narrow state.Store test double.
func (s *server) udpListeners() (state.UDPListenerStore, bool) {
	store, ok := s.store.(state.UDPListenerStore)
	return store, ok
}

func udpListenerResponse(listener state.UDPListener) api.UDPListenerResponse {
	return api.UDPListenerResponse{
		ID: listener.ID, Name: listener.ListenerName, GuestPort: listener.GuestPort,
		PublicPort: listener.PublicPort, Protocol: listener.Protocol,
		Enabled: listener.Enabled, CreatedAt: listener.CreatedAt, UpdatedAt: listener.UpdatedAt,
	}
}

// appDeclaresUDPListener is the control-plane mirror of the gateway's
// manifest selector. A durable public listener must not invent a guest port
// or listener name that the deployed workload did not declare. Unnamed
// manifest entries use the same deterministic udp-<port> selector for UDP ports.
func appDeclaresUDPListener(app state.App, name string, guestPort int) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, port := range app.Manifest.Ports {
		// ADR-530: an internal listener never gets a public endpoint.
		if port.Internal || port.EffectiveProtocol() != api.WorkloadPortUDP || port.Port != guestPort {
			continue
		}
		declaredName := strings.ToLower(strings.TrimSpace(port.Name))
		if declaredName == "" {
			declaredName = fmt.Sprintf("udp-%d", port.Port)
		}
		if declaredName == name {
			return true
		}
	}
	return false
}

func writeUDPListenerStoreError(w http.ResponseWriter, action string, err error) {
	var quota *state.UDPListenerLimitError
	if errors.As(err, &quota) {
		api.WriteProblem(w, api.ErrUDPListenerLimit(quota.Limit, quota.Observed))
		return
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"UDP listener not found", "the app or listener does not exist"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"UDP listener conflict", "the listener name or public port is already in use"))
	case errors.Is(err, state.ErrInvalidUDPListener):
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid UDP listener", err.Error()))
	default:
		api.WriteProblem(w, api.ErrCapacity(fmt.Sprintf("could not %s UDP listener", action)))
	}
}

func (s *server) listAppUDPListeners(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.udpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("UDP listeners are not available"))
		return
	}
	listeners, err := store.ListUDPListenersForApp(r.Context(), app.ID)
	if err != nil {
		writeUDPListenerStoreError(w, "list", err)
		return
	}
	out := make([]api.UDPListenerResponse, 0, len(listeners))
	for _, listener := range listeners {
		out = append(out, udpListenerResponse(listener))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createAppUDPListener(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.udpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("UDP listeners are not available"))
		return
	}
	var req api.CreateUDPListenerRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid UDP listener", "invalid JSON body"))
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid UDP listener", "name is required"))
		return
	}
	if err := api.ValidateWorkloadPorts([]api.WorkloadPort{{Name: name, Port: req.GuestPort, Protocol: api.WorkloadPortUDP}}); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid UDP listener", err.Error()))
		return
	}
	if !appDeclaresUDPListener(app, name, req.GuestPort) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid UDP listener", "name and guest_port must match a declared UDP listener in the app manifest"))
		return
	}
	if req.PublicPort != 0 && (req.PublicPort < state.UDPListenerPublicPortMin || req.PublicPort > state.UDPListenerPublicPortMax) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid UDP listener", fmt.Sprintf("public_port must be between %d and %d", state.UDPListenerPublicPortMin, state.UDPListenerPublicPortMax)))
		return
	}

	if _, err := store.UDPListenerByAppAndName(r.Context(), app.ID, name); err == nil {
		writeUDPListenerStoreError(w, "create", state.ErrConflict)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		writeUDPListenerStoreError(w, "read", err)
		return
	}

	base := state.UDPListener{
		AppID: app.ID, AccountID: acct.ID, ListenerName: name,
		GuestPort: req.GuestPort, Protocol: "udp", Enabled: false,
	}
	if req.PublicPort != 0 {
		base.PublicPort = req.PublicPort
		created, err := store.CreateUDPListener(r.Context(), base)
		if err != nil {
			writeUDPListenerStoreError(w, "create", err)
			return
		}
		_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
			fmt.Sprintf(`{"kind":"udp_listener_created","app_id":"%s","listener_id":"%s"}`, app.ID, created.ID))
		writeJSON(w, http.StatusCreated, udpListenerResponse(created))
		return
	}

	// Public ports are globally unique. Retrying only conflicts makes the
	// allocation safe under concurrent API requests without adding another
	// store-wide method solely for the allocator.
	for publicPort := state.UDPListenerPublicPortMin; publicPort <= state.UDPListenerPublicPortMax; publicPort++ {
		candidate := base
		candidate.PublicPort = publicPort
		created, err := store.CreateUDPListener(r.Context(), candidate)
		if err == nil {
			_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
				fmt.Sprintf(`{"kind":"udp_listener_created","app_id":"%s","listener_id":"%s"}`, app.ID, created.ID))
			writeJSON(w, http.StatusCreated, udpListenerResponse(created))
			return
		}
		if !errors.Is(err, state.ErrConflict) {
			writeUDPListenerStoreError(w, "create", err)
			return
		}
		// A concurrent creator may have claimed the name rather than the
		// candidate public port. Stop instead of scanning the whole range.
		if _, lookupErr := store.UDPListenerByAppAndName(r.Context(), app.ID, name); lookupErr == nil {
			writeUDPListenerStoreError(w, "create", state.ErrConflict)
			return
		} else if !errors.Is(lookupErr, state.ErrNotFound) {
			writeUDPListenerStoreError(w, "read", lookupErr)
			return
		}
	}
	api.WriteProblem(w, api.ErrCapacity("UDP listener public port range is exhausted"))
}

func (s *server) updateAppUDPListener(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.udpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("UDP listeners are not available"))
		return
	}
	var req api.UpdateUDPListenerRequest
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid UDP listener", "enabled is required"))
		return
	}
	listener, err := store.UDPListenerByAppAndName(r.Context(), app.ID, r.PathValue("name"))
	if err != nil {
		writeUDPListenerStoreError(w, "read", err)
		return
	}
	if *req.Enabled && !appDeclaresUDPListener(app, listener.ListenerName, listener.GuestPort) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid UDP listener", "listener no longer matches a declared UDP port in the app manifest"))
		return
	}
	updated, err := store.SetUDPListenerEnabled(r.Context(), listener.ID, *req.Enabled)
	if err != nil {
		writeUDPListenerStoreError(w, "update", err)
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"udp_listener_updated","app_id":"%s","listener_id":"%s"}`, app.ID, updated.ID))
	writeJSON(w, http.StatusOK, udpListenerResponse(updated))
}

func (s *server) deleteAppUDPListener(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.udpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("UDP listeners are not available"))
		return
	}
	listener, err := store.UDPListenerByAppAndName(r.Context(), app.ID, r.PathValue("name"))
	if err != nil {
		writeUDPListenerStoreError(w, "read", err)
		return
	}
	if err := store.DeleteUDPListener(r.Context(), listener.ID); err != nil {
		writeUDPListenerStoreError(w, "delete", err)
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"udp_listener_deleted","app_id":"%s","listener_id":"%s"}`, app.ID, listener.ID))
	w.WriteHeader(http.StatusNoContent)
}
