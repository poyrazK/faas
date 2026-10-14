// Package mcpadmission exposes account-authorized retained admission evidence.
package mcpadmission

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func Handler(store state.Store, receipt bool) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		reader, ok := store.(state.MCPAdmissionReader)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("MCP admission evidence unavailable"))
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("id must be a UUID"))
			return
		}
		if receipt {
			e, err := reader.GetMCPAdmissionReceipt(r.Context(), acct.ID, id.String())
			respond(w, e, err)
			return
		}
		d, err := store.DeploymentByID(r.Context(), id.String())
		if errors.Is(err, state.ErrNotFound) {
			d, err = store.DeploymentByID(r.Context(), strings.ReplaceAll(id.String(), "-", ""))
		}
		if err != nil {
			respond(w, nil, state.ErrNotFound)
			return
		}
		a, err := store.AppByID(r.Context(), d.AppID)
		if err != nil || a.AccountID != acct.ID {
			respond(w, nil, state.ErrNotFound)
			return
		}
		limit, before, outcome, err := parameters(r)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
		page, err := reader.ListMCPAdmissionHistory(r.Context(), acct.ID, id.String(), before, outcome, limit)
		respond(w, page, err)
	}
}
func parameters(r *http.Request) (int, int64, string, error) {
	limit := 50
	var before int64
	var err error
	q := r.URL.Query()
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			return 0, 0, "", errors.New("limit must be 1–50")
		}
	}
	if raw := q.Get("cursor"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			return 0, 0, "", errors.New("invalid cursor")
		}
	}
	outcome := q.Get("outcome")
	if outcome != "" && outcome != "accepted" && outcome != "rejected" {
		return 0, 0, "", errors.New("outcome must be accepted or rejected")
	}
	return limit, before, outcome, nil
}
func respond(w http.ResponseWriter, value any, err error) {
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Admission evidence not found", "no matching evidence belongs to this account"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read MCP admission evidence"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, value)
}

func DeploymentAuditResponse(r state.DeploymentAudit) api.DeploymentAuditResponse {
	out := api.DeploymentAuditResponse{
		At:    r.At.UTC().Format(time.RFC3339Nano),
		Kind:  string(r.Kind),
		Actor: r.Actor,
		Data:  r.Data,
	}
	if r.AccountID != nil {
		out.AccountID = r.AccountID.String()
	}
	return out
}

func ListDeploymentAudit(store state.Store, w http.ResponseWriter, r *http.Request, acct state.Account, defaultLimit, maxLimit int) {
	id := r.PathValue("id")
	if id == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid id", "id path segment is required"))
		return
	}
	// IDOR check — same two-step pattern as getDeployment
	// (handlers_ext.go:1320). Cross-account 404s the same way an
	// unknown id does (we never reveal whether the id exists in a
	// different account).
	d, err := store.DeploymentByID(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Deployment not found", "no deployment with that id belongs to this account"))
		return
	}
	app, err := store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Deployment not found", "no deployment with that id belongs to this account"))
		return
	}
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}
	rows, err := store.ListDeploymentAudit(r.Context(), id, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list deployment audit"))
		return
	}
	items := make([]api.DeploymentAuditResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, DeploymentAuditResponse(row))
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ListDeploymentAuditResponse{
		Items: items,
		Limit: limit,
	})
}
