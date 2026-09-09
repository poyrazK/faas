package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const appLogDrainSecretSealLabel = "APP_LOG_DRAIN_AUTH_HEADER"

const defaultAppLogDrainEnabled = true

func validAppLogDrainKind(kind string) bool {
	for _, allowed := range api.AllowedAppLogDrainKinds {
		if kind == allowed {
			return true
		}
	}
	return false
}

func validateAppLogDrainKind(kind string) *api.Problem {
	if !validAppLogDrainKind(kind) {
		return api.ErrAppLogDrainInvalid(fmt.Sprintf("kind %q is not supported; allowed: %s", kind, strings.Join(api.AllowedAppLogDrainKinds, ", ")))
	}
	return nil
}

func validateAppLogDrainURL(rawURL string) *api.Problem {
	if rawURL == "" {
		return api.ErrAppLogDrainInvalid("target_url is required")
	}
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return api.ErrAppLogDrainInvalid("target_url must start with http:// or https://")
	}
	if len(rawURL) < 8 || len(rawURL) > 2048 {
		return api.ErrAppLogDrainInvalid(fmt.Sprintf("target_url length %d out of bounds [8, 2048]", len(rawURL)))
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.User != nil {
		return api.ErrAppLogDrainInvalid("target_url must be a valid http(s) URL without userinfo")
	}
	return nil
}

func validateAppLogDrainAuthHeader(raw string) *api.Problem {
	if raw == "" {
		return nil
	}
	if len(raw) > api.AppLogDrainAuthHeaderMaxBytes || strings.ContainsAny(raw, "\r\n\x00") || strings.Count(raw, ":") != 1 {
		return api.ErrAppLogDrainInvalid("auth_header must be one non-empty Name: value pair within the size limit")
	}
	name, value, _ := strings.Cut(raw, ":")
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" || value == "" || http.CanonicalHeaderKey(name) == "" {
		return api.ErrAppLogDrainInvalid("auth_header must be one non-empty Name: value pair within the size limit")
	}
	if http.CanonicalHeaderKey(name) == "Content-Length" || http.CanonicalHeaderKey(name) == "Host" {
		return api.ErrAppLogDrainInvalid("auth_header cannot set Host or Content-Length")
	}
	return nil
}

func appLogDrainResponse(row state.AppLogDrain) api.AppLogDrainResponse {
	return api.AppLogDrainResponseFromRow(api.AppLogDrainRow{
		ID:            row.ID,
		AppID:         row.AppID,
		AccountID:     row.AccountID,
		Kind:          string(row.Kind),
		TargetURL:     row.TargetURL,
		HasAuthHeader: len(row.AuthHeaderSealed) > 0,
		Enabled:       row.Enabled,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	})
}

func (s *server) logDrainsAllowed(w http.ResponseWriter, acct state.Account) (api.Limits, bool) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.LogDrainPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanLogDrainsNotAllowed(acct.Plan))
		return api.Limits{}, false
	}
	return limits, true
}

func (s *server) listAppLogDrains(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.logDrainsAllowed(w, acct); !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rows, err := s.store.ListAppLogDrainsForApp(r.Context(), app.ID)
	if err != nil {
		s.log.WarnContext(r.Context(), "list app log drains", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not list log drains"))
		return
	}
	out := make([]api.AppLogDrainResponse, 0, len(rows))
	for _, row := range rows {
		if row.AccountID == acct.ID {
			out = append(out, appLogDrainResponse(row))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createAppLogDrain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateAppLogDrainRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeAppLogDrainInvalid, "Bad request", err.Error()))
		return
	}
	limits, ok := s.logDrainsAllowed(w, acct)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.TargetURL = strings.TrimSpace(req.TargetURL)
	if prob := validateAppLogDrainKind(req.Kind); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateAppLogDrainURL(req.TargetURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateAppLogDrainAuthHeader(req.AuthHeader); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := resolveAndCheckEgress(r.Context(), req.TargetURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	sealed, err := sealAppLogDrainAuthHeader(req.AuthHeader)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not seal log drain credentials"))
		return
	}
	enabled := defaultAppLogDrainEnabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := s.store.CreateAppLogDrainIfUnderQuota(r.Context(), state.AppLogDrain{
		AppID: app.ID, AccountID: acct.ID, Kind: state.AppLogDrainKind(req.Kind),
		TargetURL: req.TargetURL, AuthHeaderSealed: sealed, Enabled: enabled,
	}, limits)
	if err != nil {
		var quotaErr *state.AppLogDrainQuotaError
		if errors.As(err, &quotaErr) {
			api.WriteProblem(w, api.ErrPlanLogDrainQuota(acct.Plan, string(quotaErr.Scope), quotaErr.Limit, quotaErr.Observed))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrAppLogDrainInvalid("a log drain for this app already targets this URL"))
			return
		}
		s.log.WarnContext(r.Context(), "create app log drain", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not create log drain"))
		return
	}
	s.audit.Emit(r.Context(), "app.log_drain_created", &acct.ID, map[string]any{
		"log_drain_id": row.ID, "app_id": app.ID, "kind": string(row.Kind), "enabled": row.Enabled,
	})
	writeJSON(w, http.StatusCreated, appLogDrainResponse(row))
}

func (s *server) getAppLogDrain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.logDrainsAllowed(w, acct); !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	row, err := s.store.AppLogDrainByID(r.Context(), r.PathValue("id"))
	if err != nil || row.AppID != app.ID || row.AccountID != acct.ID {
		s.notFound(w, "log drain not found")
		return
	}
	writeJSON(w, http.StatusOK, appLogDrainResponse(row))
}

func (s *server) updateAppLogDrain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateAppLogDrainRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeAppLogDrainInvalid, "Bad request", err.Error()))
		return
	}
	if _, ok := s.logDrainsAllowed(w, acct); !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.AppLogDrainByID(r.Context(), id)
	if err != nil || existing.AppID != app.ID || existing.AccountID != acct.ID {
		s.notFound(w, "log drain not found")
		return
	}
	params := state.UpdateAppLogDrainParams{}
	if req.Kind != nil {
		kind := strings.TrimSpace(*req.Kind)
		if prob := validateAppLogDrainKind(kind); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		kindValue := state.AppLogDrainKind(kind)
		params.Kind = &kindValue
	}
	if req.TargetURL != nil {
		targetURL := strings.TrimSpace(*req.TargetURL)
		if prob := validateAppLogDrainURL(targetURL); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		if prob := resolveAndCheckEgress(r.Context(), targetURL); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.TargetURL = &targetURL
	}
	if req.AuthHeader != nil {
		if prob := validateAppLogDrainAuthHeader(*req.AuthHeader); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		sealed, sealErr := sealAppLogDrainAuthHeader(*req.AuthHeader)
		if sealErr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not seal log drain credentials"))
			return
		}
		params.AuthHeaderSealed = &sealed
	}
	params.Enabled = req.Enabled
	row, err := s.store.UpdateAppLogDrain(r.Context(), id, params)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "log drain not found")
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrAppLogDrainInvalid("a log drain for this app already targets this URL"))
			return
		}
		s.log.WarnContext(r.Context(), "update app log drain", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not update log drain"))
		return
	}
	s.audit.Emit(r.Context(), "app.log_drain_updated", &acct.ID, map[string]any{
		"log_drain_id": row.ID, "app_id": app.ID, "kind": string(row.Kind), "enabled": row.Enabled,
	})
	writeJSON(w, http.StatusOK, appLogDrainResponse(row))
}

func (s *server) deleteAppLogDrain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.logDrainsAllowed(w, acct); !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.AppLogDrainByID(r.Context(), id)
	if err != nil || existing.AppID != app.ID || existing.AccountID != acct.ID {
		s.notFound(w, "log drain not found")
		return
	}
	if err := s.store.DeleteAppLogDrain(r.Context(), id); err != nil {
		s.log.WarnContext(r.Context(), "delete app log drain", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not delete log drain"))
		return
	}
	s.audit.Emit(r.Context(), "app.log_drain_deleted", &acct.ID, map[string]any{
		"log_drain_id": id, "app_id": app.ID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func sealAppLogDrainAuthHeader(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	if setSecretRecipient == nil {
		return nil, errors.New("host age recipient not loaded")
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, errors.New("host age recipient not loaded")
	}
	return secretbox.SealBytes(recipient, appLogDrainSecretSealLabel, []byte(plaintext), api.AppLogDrainAuthHeaderMaxBytes)
}
