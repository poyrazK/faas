package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	orgActivityLimitDefault = 50
	orgActivityLimitMax     = 100
)

// listOrgActivity serves the organization-wide customer timeline. LoadOrg
// establishes membership and this handler pins every store query to that org.
func (s *server) listOrgActivity(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionView) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	activityStore, ok := s.store.(state.OrgActivityStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("organization activity is unavailable"))
		return
	}
	filter, limit, ok := parseOrgActivityQuery(w, r, mem.OrgID)
	if !ok {
		return
	}
	rows, err := activityStore.ListOrgActivity(r.Context(), filter)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list organization activity"))
		return
	}
	writeOrgActivityPage(w, rows, limit)
}

func parseOrgActivityQuery(w http.ResponseWriter, r *http.Request, orgID string) (state.OrgActivityFilter, int, bool) {
	id, err := uuid.Parse(orgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("organization id is invalid"))
		return state.OrgActivityFilter{}, 0, false
	}
	filter := state.OrgActivityFilter{OrgID: id, KindPrefix: r.URL.Query().Get("kind_prefix")}
	limit, ok := parseOrgActivityLimit(w, r.URL.Query().Get("limit"))
	if !ok || !parseOrgActivityCursor(w, r.URL.Query().Get("before"), &filter) ||
		!parseOrgActivityActor(w, r.URL.Query().Get("actor_type"), &filter) ||
		!parseOrgActivityApp(w, r.URL.Query().Get("app_id"), &filter) {
		return state.OrgActivityFilter{}, 0, false
	}
	filter.Limit = limit + 1
	return filter, limit, true
}

func parseOrgActivityLimit(w http.ResponseWriter, raw string) (int, bool) {
	if raw == "" {
		return orgActivityLimitDefault, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid limit", "limit must be a positive integer"))
		return 0, false
	}
	if limit > orgActivityLimitMax {
		limit = orgActivityLimitMax
	}
	return limit, true
}

func parseOrgActivityCursor(w http.ResponseWriter, raw string, filter *state.OrgActivityFilter) bool {
	if raw == "" {
		return true
	}
	key, err := cursor.Decode(raw)
	id, idErr := strconv.ParseInt(key.ID, 10, 64)
	if err != nil || idErr != nil || id < 1 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid before", "before must be an opaque cursor returned as next_before"))
		return false
	}
	filter.Before = &state.OrgActivityCursor{OccurredAt: key.CreatedAt, ID: id}
	return true
}

func parseOrgActivityActor(w http.ResponseWriter, raw string, filter *state.OrgActivityFilter) bool {
	if raw == "" {
		return true
	}
	actor := state.OrgActivityActorType(raw)
	switch actor {
	case state.OrgActivityActorUser, state.OrgActivityActorAPIKey,
		state.OrgActivityActorGitHub, state.OrgActivityActorSystem, state.OrgActivityActorOperator:
		filter.ActorType = actor
		return true
	default:
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid actor_type", "actor_type is not supported"))
		return false
	}
}

func parseOrgActivityApp(w http.ResponseWriter, raw string, filter *state.OrgActivityFilter) bool {
	if raw == "" {
		return true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid app_id", "app_id must be a UUID"))
		return false
	}
	filter.AppID = &id
	return true
}

func writeOrgActivityPage(w http.ResponseWriter, rows []state.OrgActivity, limit int) {
	hasNext := len(rows) > limit
	if hasNext {
		rows = rows[:limit]
	}
	items := make([]api.OrgActivityResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, orgActivityResponse(row))
	}
	response := api.ListOrgActivityResponse{Items: items}
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		response.NextBefore = cursor.Encode(cursor.Key{CreatedAt: last.OccurredAt, ID: strconv.FormatInt(last.ID, 10)})
	}
	writeJSON(w, http.StatusOK, response)
}

func orgActivityResponse(row state.OrgActivity) api.OrgActivityResponse {
	response := api.OrgActivityResponse{
		ID: strconv.FormatInt(row.ID, 10), OccurredAt: row.OccurredAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Kind: row.Kind, Summary: orgActivitySummary(row), Data: row.Data,
		Actor:    api.ActivityActorResponse{Type: string(row.ActorType), Label: row.ActorLabel},
		Resource: api.ActivityResourceResponse{Type: row.ResourceType, ID: row.ResourceID, Label: row.ResourceLabel},
	}
	if row.ActorAccountID != nil {
		response.Actor.AccountID = row.ActorAccountID.String()
	}
	if row.AppID != nil {
		response.AppID = row.AppID.String()
	}
	if row.ProjectID != nil {
		response.ProjectID = row.ProjectID.String()
	}
	if row.DeploymentID != nil {
		response.DeploymentID = row.DeploymentID.String()
	}
	return response
}

func orgActivitySummary(row state.OrgActivity) string {
	switch row.Kind {
	case "app.deployed":
		return fmt.Sprintf("%s deployed %s", row.ActorLabel, row.ResourceLabel)
	case "env.set":
		return fmt.Sprintf("%s changed %s", row.ActorLabel, row.ResourceLabel)
	case "env.deleted":
		return fmt.Sprintf("%s removed %s", row.ActorLabel, row.ResourceLabel)
	case "domain.added":
		return fmt.Sprintf("%s added", row.ResourceLabel)
	case "domain.tls_issued":
		return fmt.Sprintf("%s issued TLS certificate", row.ActorLabel)
	case "deploy.rolled_back":
		return fmt.Sprintf("%s rolled back %s", row.ActorLabel, row.ResourceLabel)
	default:
		return strings.TrimSpace(fmt.Sprintf("%s %s %s", row.ActorLabel, row.Kind, row.ResourceLabel))
	}
}
