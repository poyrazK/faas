package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) appTCPListenerTLSStatus(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	listeners, ok := s.tcpListeners()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("TCP listeners are not available"))
		return
	}
	listener, err := listeners.TCPListenerByAppAndName(r.Context(), app.ID, r.PathValue("name"))
	if err != nil {
		writeTCPListenerStoreError(w, "read", err)
		return
	}
	if listener.AccountID != account.ID || listener.AppID != app.ID {
		writeTCPListenerStoreError(w, "read", state.ErrNotFound)
		return
	}
	store, ok := s.store.(state.TCPListenerTLSObservationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("TCP certificate observations are not available"))
		return
	}
	observations, err := store.ListTCPListenerTLSObservations(r.Context(), listener.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read TCP certificate observations"))
		return
	}
	writeJSON(w, http.StatusOK, tcpListenerTLSStatusResponse(listener, observations, time.Now()))
}

func tcpListenerTLSStatusResponse(listener state.TCPListener, observations []state.TCPListenerTLSObservation, now time.Time) api.TCPListenerTLSStatusResponse {
	out := api.TCPListenerTLSStatusResponse{Name: listener.ListenerName, TLS: api.TCPListenerTLSConfig{Mode: listener.TLSMode, Hostname: listener.TLSHostname}, Enabled: listener.Enabled, Scope: "observed_edges", Observations: make([]api.TCPListenerTLSCertificateStatus, 0, len(observations))}
	for _, observation := range observations {
		status := api.TCPListenerTLSCertificateStatus{EdgeID: observation.EdgeID, Status: observation.Status(listener, now), ObservedAt: observation.ObservedAt}
		if status.Status != "unknown" && !observation.NotAfter.IsZero() {
			expiry := observation.NotAfter
			status.NotAfter = &expiry
		}
		out.Observations = append(out.Observations, status)
	}
	return out
}
