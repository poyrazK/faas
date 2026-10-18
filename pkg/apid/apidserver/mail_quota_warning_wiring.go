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

func (s *server) mountInternalMailQuotaWarning(mux *http.ServeMux, listenerAddr string, getenv func(string) string) error {
	if getenv(internalmail.QuotaWarningOutboxEnableEnv) != "1" {
		return nil
	}
	if !loopbackListenAddr(listenerAddr) || s.deletionMailOutbox == nil {
		return errors.New("apid: quota warning mail requires its outbox and existing loopback operator listener")
	}
	token, err := quotaWarningServiceToken(getenv)
	if err != nil {
		return err
	}
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		return internalSafeDeployAuth(token, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if len(r.Header.Values("Authorization")) != 1 || s.quotaWarningOutboxRequested == nil || !s.quotaWarningOutboxRequested() || !internalmail.QuotaWarningOutboxEnabled(getenv) {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET /v1/internal/billing/quota-warnings/readyz", wrap(s.internalMailQuotaWarningReady))
	mux.HandleFunc("POST /v1/internal/billing/accounts/{id}/quota-warning", wrap(s.internalMailScheduleQuotaWarning))
	mux.HandleFunc("POST /v1/internal/billing/accounts/{id}/quota-warning/complete", wrap(s.internalMailQuotaWarningComplete))
	return nil
}

func (s *server) internalMailQuotaWarningReady(w http.ResponseWriter, r *http.Request) {
	if err := s.deletionMailOutbox.QuotaWarningReady(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) internalMailScheduleQuotaWarning(w http.ResponseWriter, r *http.Request) {
	var input api.InternalMailQuotaWarningRequest
	if !decodeInternalMailQuotaWarning(w, r, &input) {
		return
	}
	result, err := s.deletionMailOutbox.WarnQuotaWithNotice(r.Context(), r.PathValue("id"), input.Day, input.Plan, input.RequestID)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *server) internalMailQuotaWarningComplete(w http.ResponseWriter, r *http.Request) {
	var input api.InternalMailQuotaWarningCompleteRequest
	if !decodeInternalMailQuotaWarning(w, r, &input) {
		return
	}
	if err := s.deletionMailOutbox.CompleteQuotaWarningFollowup(r.Context(), r.PathValue("id"), input.IntentID, input.LeaseID); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeInternalMailQuotaWarning(w http.ResponseWriter, r *http.Request, output any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.InternalMailQuotaWarningCommandMaxBytes))
	d.DisallowUnknownFields()
	if d.Decode(output) != nil || d.Decode(new(any)) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

func quotaWarningServiceToken(getenv func(string) string) (string, error) {
	token, err := api.LoadInternalMailQuotaWarningToken(getenv(api.InternalMailQuotaWarningTokenPathEnv))
	if err != nil {
		return "", err
	}
	for _, name := range []string{"FAAS_CANARY_PROGRESSION_TOKEN", "FAAS_SAFEDEPLOY_TOKEN", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN"} {
		if token == strings.TrimSpace(getenv(name)) {
			return "", errors.New("apid: quota warning command requires a distinct service credential")
		}
	}
	if internalmail.AccountSuspendedOutboxEnabled(getenv) {
		sibling, err := api.LoadInternalMailSuspensionToken(getenv(api.InternalMailSuspensionTokenPathEnv))
		if err != nil || sibling == token {
			return "", errors.New("apid: quota warning requires a distinct service credential")
		}
	}
	if internalmail.NonpaymentDeletionOutboxEnabled(getenv) {
		sibling, err := api.LoadInternalMailNonpaymentDeletionToken(getenv(api.InternalMailNonpaymentDeletionTokenPathEnv))
		if err != nil || sibling == token {
			return "", errors.New("apid: quota warning requires a distinct service credential")
		}
	}
	return token, nil
}
