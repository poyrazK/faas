package operation

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func OperationHistoryOptions(r *http.Request) (api.OperationListOptions, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return api.OperationListOptions{}, state.ErrInvalidArgument
		}
		switch key {
		case "app_id", "scope", "name", "state", "limit", "cursor", "subject_type", "subject_id":
		default:
			return api.OperationListOptions{}, state.ErrInvalidArgument
		}
	}
	opts := api.OperationListOptions{SubjectType: query.Get("subject_type"), SubjectID: query.Get("subject_id"), AppID: query.Get("app_id"), Scope: query.Get("scope"), Name: query.Get("name"), State: api.OperationState(query.Get("state")), Cursor: query.Get("cursor")}
	if query.Has("limit") {
		opts.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || opts.Limit < 1 {
			return opts, state.ErrInvalidArgument
		}
	}
	return opts, nil
}

func (s *Service) ListPlatformTenantSelfOperations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.OperationStore(w)
	if !ok {
		return
	}
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if !ok {
		return
	}
	opts, err := OperationHistoryOptions(r)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	page, err := store.ListPlatformTenantOperations(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}
