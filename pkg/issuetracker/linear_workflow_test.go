package issuetracker

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestLinearWorkflowVerificationIsReadOnlyAndRejectsIncompleteEvidence(t *testing.T) {
	const team = "44444444-4444-4444-8444-444444444444"
	const state = "66666666-6666-4666-8666-666666666666"
	for _, kind := range []string{"started", "backlog", "unstarted", "completed", "wrong lifecycle", "canceled", "unknown", "missing archive", "archived", "wrong team", "wrong state", "partial errors", "missing workflow", "invalid input"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				var payload struct {
					Query     string
					Variables map[string]string
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if r.URL.Host != "api.linear.app" || !strings.HasPrefix(payload.Query, "query ") || payload.Variables["id"] != state {
					t.Fatal("unexpected provider operation", payload)
				}
				workflowType := kind
				switch kind {
				case "missing archive", "archived", "wrong team", "wrong state", "partial errors", "missing workflow":
					workflowType = "started"
				case "wrong lifecycle":
					workflowType = "completed"
				}
				w := map[string]any{"id": state, "team": map[string]string{"id": team}, "archivedAt": nil, "type": workflowType}
				switch kind {
				case "missing archive":
					delete(w, "archivedAt")
				case "archived":
					w["archivedAt"] = "2026-10-14T00:00:00Z"
				case "wrong team":
					w["team"] = map[string]string{"id": state}
				case "wrong state":
					w["id"] = team
				}
				body := map[string]any{"data": map[string]any{"workflowState": w}}
				if kind == "missing workflow" {
					body["data"] = map[string]any{"workflowState": nil}
				}
				if kind == "partial errors" {
					body["errors"] = []any{map[string]string{"message": "secret"}}
				}
				out, _ := json.Marshal(body)
				return response(200, string(out)), nil
			})})
			wanted := "open"
			if kind == "completed" {
				wanted = "resolved"
			}
			selected := state
			if kind == "invalid input" {
				selected = "invalid"
			}
			err := client.VerifyLinearWorkflowState(t.Context(), "secret", team, selected, wanted)
			valid := kind == "started" || kind == "backlog" || kind == "unstarted" || kind == "completed"
			if (err == nil) != valid {
				t.Fatal("wrong validation outcome", err)
			}
			if kind == "invalid input" && calls != 0 {
				t.Fatal("invalid UUID contacted provider")
			}
		})
	}
}
