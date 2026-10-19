// Package apprecovery owns account-scoped deleted inventory and exact-ID restore.
package apprecovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Handler receives accounts from existing MFA/scope/origin middleware.
// Existing lifecycle hooks preserve atomic restore and notification behavior.
type Handler struct {
	Store               state.Store
	RestoreWithActivity func(context.Context, *http.Request, state.Account, state.App, api.Limits) (state.App, error)
	OnRestored          func(context.Context, *http.Request, state.Account, state.App)
	Response            func(context.Context, state.App, api.Plan) api.AppResponse
	ParkedRef           func(context.Context, api.AppResponse, state.App) api.AppResponse
}

type deletedAppCursor struct {
	AccountID string    `json:"account_id"`
	At        time.Time `json:"at"`
	ID        string    `json:"id"`
}

func validDeletedAppID(id string) bool {
	// MemStore's historical IDs are 32 hex characters; PostgreSQL returns UUIDs.
	if len(id) != 32 && len(id) != 36 {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

func decodeDeletedAppCursor(token, accountID string) (deletedAppCursor, error) {
	var cursor deletedAppCursor
	if token == "" {
		return cursor, nil
	}
	if len(token) > 1024 {
		return cursor, errors.New("cursor exceeds its bound")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return cursor, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return cursor, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return cursor, errors.New("cursor must contain a single value")
	}
	if cursor.AccountID != accountID || cursor.At.IsZero() || !validDeletedAppID(cursor.ID) {
		return cursor, errors.New("cursor is not valid for this account")
	}
	return cursor, nil
}

func encodeDeletedAppCursor(accountID string, row state.DeletedAppRecord) string {
	raw, _ := json.Marshal(deletedAppCursor{AccountID: accountID, At: row.SortAt, ID: row.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func deletedAppSummary(row state.DeletedAppRecord, accountID string, now time.Time) api.DeletedAppSummary {
	out := api.DeletedAppSummary{
		AccountID: accountID, ID: row.ID, Slug: row.Slug, DeletedAt: row.DeletedAt,
		DeleteGraceUntil: row.DeleteGraceUntil, EvaluatedAt: now,
	}
	switch {
	case row.PurgeStarted:
		out.RestoreBlocker = "purge_started"
	case row.DeletedAt == nil || row.DeleteGraceUntil == nil:
		out.RestoreBlocker = "metadata_unavailable"
	case !row.DeleteGraceUntil.After(now):
		out.RestoreBlocker = "grace_expired"
	default:
		out.RestoreEligible = true
	}
	return out
}

func (s Handler) deletedAppsStore(w http.ResponseWriter) (state.AccountDeletedAppsStore, bool) {
	store, ok := s.Store.(state.AccountDeletedAppsStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("account-owned deleted-app inventory is unavailable"))
	}
	return store, ok
}

func validDeletedAppQuery(r *http.Request, pagination bool) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range query {
		if !pagination || (key != "limit" && key != "cursor") || len(values) != 1 {
			return false
		}
	}
	return true
}

func (s Handler) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !validDeletedAppQuery(r, true) {
		api.WriteProblem(w, api.ErrValidation("only one limit and cursor are supported; environment scope is not supported"))
		return
	}
	limit := 25
	if value, present := r.URL.Query()["limit"]; present {
		parsed, err := strconv.Atoi(value[0])
		if err != nil || parsed < 1 || parsed > 50 {
			api.WriteProblem(w, api.ErrValidation("limit must be 1..50"))
			return
		}
		limit = parsed
	}
	cursor, err := decodeDeletedAppCursor(r.URL.Query().Get("cursor"), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid deleted-app cursor; restart this account's inventory"))
		return
	}
	store, ok := s.deletedAppsStore(w)
	if !ok {
		return
	}
	rows, err := store.ListAccountDeletedApps(r.Context(), acct.ID, cursor.At, cursor.ID, limit+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read deleted-app inventory"))
		return
	}
	now := time.Now().UTC()
	out := api.DeletedAppListResponse{AccountID: acct.ID, Apps: make([]api.DeletedAppSummary, 0, limit), EvaluatedAt: now}
	if len(rows) > limit {
		out.NextCursor = encodeDeletedAppCursor(acct.ID, rows[limit-1])
		rows = rows[:limit]
	}
	for _, row := range rows {
		out.Apps = append(out.Apps, deletedAppSummary(row, acct.ID, now))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s Handler) readDeletedApp(w http.ResponseWriter, r *http.Request, acct state.Account) (state.DeletedAppRecord, bool) {
	if !validDeletedAppID(r.PathValue("id")) {
		api.WriteProblem(w, api.ErrValidation("invalid deleted-app ID"))
		return state.DeletedAppRecord{}, false
	}
	store, ok := s.deletedAppsStore(w)
	if !ok {
		return state.DeletedAppRecord{}, false
	}
	row, err := store.AccountDeletedAppByID(r.Context(), acct.ID, r.PathValue("id"))
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such deleted app")
		return state.DeletedAppRecord{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read deleted app"))
		return state.DeletedAppRecord{}, false
	}
	return row, true
}

func (s Handler) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !validDeletedAppQuery(r, false) {
		api.WriteProblem(w, api.ErrValidation("deleted-app detail does not accept environment or other query scope"))
		return
	}
	row, ok := s.readDeletedApp(w, r, acct)
	if ok {
		httpkit.WriteJSON(w, http.StatusOK, deletedAppSummary(row, acct.ID, time.Now().UTC()))
	}
}

func (s Handler) Restore(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !validDeletedAppQuery(r, false) {
		api.WriteProblem(w, api.ErrValidation("restore does not accept environment or other query scope"))
		return
	}
	// Only an absent body or an empty object represents this one action. Never
	// interpret an unknown intent (for example dry_run) as a real restoration.
	var body *struct{}
	err := httpkit.DecodeJSONSized(r, &body, 4096)
	if !errors.Is(err, io.EOF) && (err != nil || body == nil) {
		api.WriteProblem(w, api.ErrValidation("restore accepts no fields; use an empty body"))
		return
	}
	row, ok := s.readDeletedApp(w, r, acct)
	if !ok {
		return
	}
	lifecycle := deletedAppSummary(row, acct.ID, time.Now().UTC())
	if !lifecycle.RestoreEligible {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeAppNotRestorable,
			"App not restorable", "the lifecycle review is blocked: "+lifecycle.RestoreBlocker))
		return
	}
	app, err := s.Store.AppByID(r.Context(), row.ID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && app.AccountID != acct.ID) {
		httpkit.NotFound(w, "no such deleted app")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not recheck deleted app"))
		return
	}
	if app.ID != row.ID || app.Slug != row.Slug || app.Status != state.AppDeleted {
		writeRestoreIdentityConflict(w)
		return
	}
	current, err := s.Store.AppBySlug(r.Context(), row.Slug)
	if err == nil && current.ID != row.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"App slug is in use", "the retained app's slug belongs to an active app; inspect the inventory before a new restore review"))
		return
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not recheck app slug"))
		return
	}
	// RestoreApp/RestoreAppWithActivity conditionally mutate this exact ID and
	// recheck grace, purge claim, quota and capacity at the mutation boundary.
	s.restoreOwnedApp(w, r, acct, app)
}

func writeRestoreIdentityConflict(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
		"App identity changed", "the reviewed deleted app changed; refresh its exact ID and review again"))
}

func readLegacyRestoreIdentity(r *http.Request) (string, error) {
	var request *struct {
		ExpectedAppID json.RawMessage `json:"expected_app_id"`
	}
	if err := httpkit.DecodeJSONSized(r, &request, 4096); err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	if request == nil {
		return "", errors.New("restore body must be an object")
	}
	if request.ExpectedAppID == nil {
		return "", nil
	}
	var id string
	if err := json.Unmarshal(request.ExpectedAppID, &id); err != nil || !validDeletedAppID(id) {
		return "", errors.New("expected_app_id must be a nonempty app ID")
	}
	return id, nil
}

func (s Handler) LegacyRestore(w http.ResponseWriter, r *http.Request, acct state.Account) {
	expectedID, err := readLegacyRestoreIdentity(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("restore accepts only a valid optional expected_app_id"))
		return
	}
	app, err := s.Store.AppBySlugIncludingDeleted(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		httpkit.NotFound(w, "no such app")
		return
	}
	if expectedID != "" && app.ID != expectedID {
		writeRestoreIdentityConflict(w)
		return
	}
	s.restoreOwnedApp(w, r, acct, app)
}

func (s Handler) restoreOwnedApp(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	limits := api.MustLimitsFor(acct.Plan)
	restored, err := s.RestoreWithActivity(r.Context(), r, acct, app, limits)
	if err != nil {
		var qe *state.QuotaError
		if errors.As(err, &qe) {
			api.WriteProblem(w, appaccess.QuotaProblem(qe, limits))
			return
		}
		if errors.Is(err, state.ErrOrgAppAdmissionUnavailable) {
			api.WriteProblem(w, appaccess.AdmissionUnavailableProblem())
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict,
				api.CodeAppNotRestorable, "App not restorable",
				"the deletion window is closed or the app cannot be restored in its current state"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not restore app"))
		return
	}
	s.OnRestored(r.Context(), r, acct, restored)
	resp := s.Response(r.Context(), restored, acct.Plan)
	// RestoreApp persists the storage state as active, but the customer-facing
	// read model calls an app with no deployments "undeployed". Keep this
	// mutation response consistent with GET /v1/apps/{slug} and listApps so
	// automation does not observe active and undeployed for the same row.
	if _, latestErr := s.Store.LatestDeployment(r.Context(), restored.ID); errors.Is(latestErr, state.ErrNotFound) && resp.Status == string(state.AppActive) {
		resp.Status = api.AppStatusUndeployed
	} else if latestErr != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve restored app deployment state"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, s.ParkedRef(r.Context(), resp, restored))
}
