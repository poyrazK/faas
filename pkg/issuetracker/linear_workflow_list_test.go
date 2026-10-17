package issuetracker

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func workflowCatalogBody(team string, nodes []map[string]any, next bool, cursor string) map[string]any {
	return map[string]any{"data": map[string]any{"team": map[string]any{"id": team, "archivedAt": nil, "states": map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": next, "endCursor": cursor}}}}}
}
func workflowCatalogNode(team, name, kind string) map[string]any {
	return map[string]any{"id": uuid.NewString(), "team": map[string]string{"id": team}, "name": name, "type": kind, "archivedAt": nil}
}
func TestLinearWorkflowCatalogPaginatesFiltersAndNeverMutates(t *testing.T) {
	team := uuid.NewString()
	calls := 0
	open := workflowCatalogNode(team, "In progress", "started")
	done := workflowCatalogNode(team, "Done", "completed")
	cancel := workflowCatalogNode(team, "Canceled", "canceled")
	archived := workflowCatalogNode(team, "Old", "started")
	archived["archivedAt"] = "2026-10-14T00:00:00Z"
	client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var payload struct {
			Query     string
			Variables map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if r.URL.Host != "api.linear.app" || !strings.HasPrefix(payload.Query, "query ") || payload.Variables["id"] != team || payload.Variables["first"] != float64(api.IssueTrackerLinearWorkflowPageSize) {
			t.Fatal("wrong read scope", payload)
		}
		body := workflowCatalogBody(team, []map[string]any{done, cancel, archived}, true, "page-two")
		if calls == 1 {
			if payload.Variables["after"] != nil {
				t.Fatal("unexpected first cursor")
			}
		} else {
			if calls != 2 || payload.Variables["after"] != "page-two" {
				t.Fatal("wrong pagination")
			}
			body = workflowCatalogBody(team, []map[string]any{open}, false, "")
		}
		out, _ := json.Marshal(body)
		return response(200, string(out)), nil
	})})
	catalog, err := client.ListLinearWorkflowStates(t.Context(), "secret", team)
	if err != nil || calls != 2 || len(catalog) != 2 || catalog[0].Name != "In progress" || catalog[1].Name != "Done" {
		t.Fatal(catalog, err, calls)
	}
}
func TestLinearWorkflowCatalogRejectsIncompleteForeignAndUnboundedEvidence(t *testing.T) {
	for _, kind := range []string{"team", "state team", "missing archive", "archived team", "partial error", "nil nodes", "missing pagination", "missing next flag", "empty cursor", "repeated cursor", "duplicate state", "oversized name", "control name", "oversized page", "page bound", "invalid team"} {
		t.Run(kind, func(t *testing.T) {
			team := uuid.NewString()
			node := workflowCatalogNode(team, "In progress", "started")
			calls := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				nodes := []map[string]any{node}
				next := false
				cursor := ""
				switch kind {
				case "state team":
					node["team"] = map[string]string{"id": uuid.NewString()}
				case "missing archive":
					delete(node, "archivedAt")
				case "oversized name":
					node["name"] = strings.Repeat("x", api.IssueTrackerLinearWorkflowNameMaxBytes+1)
				case "control name":
					node["name"] = "Done\nsecret"
				case "duplicate state":
					nodes = append(nodes, node)
				case "oversized page":
					nodes = nil
					for i := 0; i < api.IssueTrackerLinearWorkflowPageSize+1; i++ {
						nodes = append(nodes, workflowCatalogNode(team, "State", "started"))
					}
				case "empty cursor":
					next = true
				case "repeated cursor":
					next = true
					cursor = "same"
					nodes = []map[string]any{workflowCatalogNode(team, "State", "started")}
				case "page bound":
					next = true
					cursor = uuid.NewString()
					nodes = []map[string]any{workflowCatalogNode(team, "State", "started")}
				}
				body := workflowCatalogBody(team, nodes, next, cursor)
				row := body["data"].(map[string]any)["team"].(map[string]any)
				states := row["states"].(map[string]any)
				switch kind {
				case "team":
					row["id"] = uuid.NewString()
				case "archived team":
					row["archivedAt"] = "2026-10-14T00:00:00Z"
				case "partial error":
					body["errors"] = []any{map[string]string{"message": "secret provider diagnostic"}}
				case "nil nodes":
					states["nodes"] = nil
				case "missing pagination":
					delete(states, "pageInfo")
				case "missing next flag":
					states["pageInfo"] = map[string]string{"endCursor": "cursor"}
				}
				out, _ := json.Marshal(body)
				return response(200, string(out)), nil
			})})
			selected := team
			if kind == "invalid team" {
				selected = "invalid"
			}
			catalog, err := client.ListLinearWorkflowStates(t.Context(), "secret", selected)
			if err == nil || catalog != nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("partial/invalid catalog accepted", catalog, err)
			}
			if calls > api.IssueTrackerLinearWorkflowMax/api.IssueTrackerLinearWorkflowPageSize {
				t.Fatal("unbounded pagination")
			}
			if kind == "invalid team" && calls != 0 {
				t.Fatal("invalid scope contacted provider")
			}
		})
	}
}
