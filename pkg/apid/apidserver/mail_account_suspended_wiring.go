package apidserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func (s *server) mountInternalMailSuspension(mux *http.ServeMux, listenerAddr string, getenv func(string) string) error {
	if getenv(internalmail.AccountSuspendedOutboxEnableEnv) != "1" {
		return nil
	}
	if !loopbackListenAddr(listenerAddr) || s.deletionMailOutbox == nil {
		return errors.New("apid: suspension mail requires its outbox and existing loopback operator listener")
	}
	token, err := api.LoadInternalMailSuspensionToken(getenv(api.InternalMailSuspensionTokenPathEnv))
	if err != nil {
		return err
	}
	for _, name := range []string{"FAAS_CANARY_PROGRESSION_TOKEN", "FAAS_SAFEDEPLOY_TOKEN", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN"} {
		if token == getenv(name) {
			return errors.New("apid: suspension command requires a distinct service credential")
		}
	}
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		return internalSafeDeployAuth(token, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if len(r.Header.Values("Authorization")) != 1 || s.accountSuspendedOutboxRequested == nil || !s.accountSuspendedOutboxRequested() || !internalmail.AccountSuspendedOutboxEnabled(getenv) {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET /v1/internal/billing/suspensions/readyz", wrap(s.internalMailSuspensionReady))
	mux.HandleFunc("POST /v1/internal/billing/accounts/{id}/suspension", wrap(s.internalMailSuspendAccount))
	mux.HandleFunc("POST /v1/internal/billing/accounts/{id}/suspension/complete", wrap(s.internalMailSuspensionComplete))
	return nil
}

func (s *server) internalMailSuspensionReady(w http.ResponseWriter, r *http.Request) {
	if err := s.deletionMailOutbox.AccountSuspendedReady(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) internalMailSuspendAccount(w http.ResponseWriter, r *http.Request) {
	var input api.InternalMailSuspensionRequest
	if !decodeInternalMailSuspension(w, r, &input) {
		return
	}
	result, err := s.deletionMailOutbox.SuspendWithNotice(r.Context(), r.PathValue("id"), input.PastDueAt, input.RequestID)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *server) internalMailSuspensionComplete(w http.ResponseWriter, r *http.Request) {
	var input api.InternalMailSuspensionCompleteRequest
	if !decodeInternalMailSuspension(w, r, &input) {
		return
	}
	if err := s.deletionMailOutbox.CompleteSuspensionFollowup(r.Context(), r.PathValue("id"), input.IntentID, input.LeaseID); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeInternalMailSuspension(w http.ResponseWriter, r *http.Request, output any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.InternalMailSuspensionCommandMaxBytes))
	d.DisallowUnknownFields()
	if d.Decode(output) != nil || d.Decode(new(any)) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}
