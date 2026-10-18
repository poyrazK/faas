package apidserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func (s *server) mountInternalMailNonpaymentDeletion(mux *http.ServeMux, listenerAddr string, getenv func(string) string) error {
	if getenv(internalmail.NonpaymentDeletionOutboxEnableEnv) != "1" {
		return nil
	}
	if !loopbackListenAddr(listenerAddr) || s.deletionMailOutbox == nil {
		return errors.New("apid: non-payment deletion mail requires its outbox and existing loopback operator listener")
	}
	token, err := nonpaymentDeletionServiceToken(getenv)
	if err != nil {
		return err
	}
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		return internalSafeDeployAuth(token, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if len(r.Header.Values("Authorization")) != 1 || s.nonpaymentDeletionOutboxRequested == nil || !s.nonpaymentDeletionOutboxRequested() || !internalmail.NonpaymentDeletionOutboxEnabled(getenv) {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET /v1/internal/billing/nonpayment-deletions/readyz", wrap(s.internalMailNonpaymentDeletionReady))
	mux.HandleFunc("POST /v1/internal/billing/accounts/{id}/nonpayment-deletion", wrap(s.internalMailScheduleNonpaymentDeletion))
	mux.HandleFunc("POST /v1/internal/billing/accounts/{id}/nonpayment-deletion/complete", wrap(s.internalMailNonpaymentDeletionComplete))
	return nil
}

func (s *server) internalMailNonpaymentDeletionReady(w http.ResponseWriter, r *http.Request) {
	if err := s.deletionMailOutbox.NonpaymentDeletionReady(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) internalMailScheduleNonpaymentDeletion(w http.ResponseWriter, r *http.Request) {
	var input api.InternalMailNonpaymentDeletionRequest
	if !decodeInternalMailNonpaymentDeletion(w, r, &input) {
		return
	}
	result, err := s.deletionMailOutbox.ScheduleNonpaymentDeletionWithNotice(r.Context(), r.PathValue("id"), input.PastDueAt, input.RequestID)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *server) internalMailNonpaymentDeletionComplete(w http.ResponseWriter, r *http.Request) {
	var input api.InternalMailNonpaymentDeletionCompleteRequest
	if !decodeInternalMailNonpaymentDeletion(w, r, &input) {
		return
	}
	if err := s.deletionMailOutbox.CompleteNonpaymentDeletionFollowup(r.Context(), r.PathValue("id"), input.IntentID, input.LeaseID); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeInternalMailNonpaymentDeletion(w http.ResponseWriter, r *http.Request, output any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.InternalMailNonpaymentDeletionCommandMaxBytes))
	d.DisallowUnknownFields()
	if d.Decode(output) != nil || d.Decode(new(any)) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

func nonpaymentDeletionServiceToken(getenv func(string) string) (string, error) {
	token, err := api.LoadInternalMailNonpaymentDeletionToken(getenv(api.InternalMailNonpaymentDeletionTokenPathEnv))
	if err != nil {
		return "", err
	}
	for _, name := range []string{"FAAS_CANARY_PROGRESSION_TOKEN", "FAAS_SAFEDEPLOY_TOKEN", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN"} {
		if token == strings.TrimSpace(getenv(name)) {
			return "", errors.New("apid: non-payment deletion command requires a distinct service credential")
		}
	}
	if internalmail.AccountSuspendedOutboxEnabled(getenv) {
		sibling, err := api.LoadInternalMailSuspensionToken(getenv(api.InternalMailSuspensionTokenPathEnv))
		if err != nil || sibling == token {
			return "", errors.New("apid: non-payment deletion requires a distinct service credential")
		}
	}
	return token, nil
}
