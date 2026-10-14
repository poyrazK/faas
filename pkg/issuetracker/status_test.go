package issuetracker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func githubStateJSON(state, reason string) string {
	data, _ := json.Marshal(map[string]any{"html_url": "https://github.com/owner/repo/issues/42", "number": 42, "state": state, "state_reason": reason})
	return string(data)
}
func TestGitHubStatusUpdateChecksSnapshotAndConfirmsLifecycle(t *testing.T) {
	state, reason := "open", ""
	writes := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" || r.URL.Path != "/repos/owner/repo/issues/42" {
			t.Fatal("wrong issue destination")
		}
		if r.Method == "PATCH" {
			writes++
			var input map[string]any
			if e := json.NewDecoder(r.Body).Decode(&input); e != nil || input["state"] != "closed" || input["state_reason"] != "completed" {
				t.Fatal("wrong lifecycle mutation", e)
			}
			state, reason = "closed", "completed"
		}
		return response(200, githubStateJSON(state, reason)), nil
	})})
	observed, err := client.ReadStatus(context.Background(), "https://github.com/owner/repo/issues/42", "secret")
	if err != nil {
		t.Fatal(err)
	}
	final, err := client.SetStatus(context.Background(), observed, "secret", "resolved", "")
	if err != nil || final.State != "resolved" || writes != 1 {
		t.Fatal("status update not confirmed", final, err)
	}
	if _, err := client.SetStatus(context.Background(), final, "secret", "resolved", ""); err != nil || writes != 1 {
		t.Fatal("idempotent status was rewritten")
	}
	if _, err := client.SetStatus(context.Background(), observed, "secret", "open", ""); !errors.Is(err, ErrStatusChanged) || writes != 1 {
		t.Fatal("stale snapshot was overwritten", err)
	}
}
func TestGitHubNotPlannedAndForeignTicketsCannotSync(t *testing.T) {
	for _, body := range []string{githubStateJSON("closed", "not_planned"), strings.Replace(githubStateJSON("open", ""), "owner/repo", "other/repo", 1), strings.TrimSuffix(githubStateJSON("open", ""), "}") + `,"pull_request":{}}`} {
		client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })})
		if _, err := client.ReadStatus(context.Background(), "https://github.com/owner/repo/issues/42", "secret"); err == nil {
			t.Fatal("unsupported or foreign ticket accepted")
		}
	}
}
func linearStatusJSON(kind, stateID string, update bool) string {
	issue := map[string]any{"id": "11111111-1111-4111-8111-111111111111", "url": "https://linear.app/acme/issue/PAY-42", "identifier": "PAY-42", "archivedAt": nil, "team": map[string]string{"id": "44444444-4444-4444-8444-444444444444"}, "state": map[string]string{"id": stateID, "type": kind}}
	data := map[string]any{"issue": issue}
	if update {
		data = map[string]any{"issueUpdate": map[string]any{"success": true, "issue": issue}}
	}
	out, _ := json.Marshal(map[string]any{"data": data})
	return string(out)
}
func TestLinearStatusUpdateVerifiesWorkflowStateTeamAndType(t *testing.T) {
	for _, wrong := range []string{"", "team", "type", "archived"} {
		t.Run(wrong, func(t *testing.T) {
			currentID := "55555555-5555-4555-8555-555555555555"
			targetID := "66666666-6666-4666-8666-666666666666"
			writes := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var payload struct {
					Query     string                     `json:"query"`
					Variables map[string]json.RawMessage `json:"variables"`
				}
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if strings.Contains(payload.Query, "workflowState(") {
					team, kind := "44444444-4444-4444-8444-444444444444", "completed"
					var archived any
					if wrong == "team" {
						team = "77777777-7777-4777-8777-777777777777"
					}
					if wrong == "type" {
						kind = "canceled"
					}
					if wrong == "archived" {
						archived = "2026-10-14T00:00:00Z"
					}
					out, _ := json.Marshal(map[string]any{"data": map[string]any{"workflowState": map[string]any{"id": targetID, "type": kind, "archivedAt": archived, "team": map[string]string{"id": team}}}})
					return response(200, string(out)), nil
				}
				if strings.Contains(payload.Query, "issueUpdate(") {
					writes++
					var input map[string]string
					_ = json.Unmarshal(payload.Variables["input"], &input)
					if input["stateId"] != targetID {
						t.Fatal("wrong selected state")
					}
					return response(200, linearStatusJSON("completed", targetID, true)), nil
				}
				return response(200, linearStatusJSON("started", currentID, false)), nil
			})})
			observed, err := client.ReadStatus(context.Background(), "https://linear.app/acme/issue/PAY-42", "secret")
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.SetStatus(context.Background(), observed, "secret", "resolved", targetID)
			if wrong == "" {
				if err != nil || result.State != "resolved" || writes != 1 {
					t.Fatal("Linear update not confirmed", err)
				}
			} else if err == nil || writes != 0 {
				t.Fatal("invalid workflow state caused a write", err)
			}
		})
	}
}
func TestLinearCanceledArchivedAndWrongWorkspaceRemainUntouched(t *testing.T) {
	for _, body := range []string{linearStatusJSON("canceled", "55555555-5555-4555-8555-555555555555", false), strings.Replace(linearStatusJSON("completed", "55555555-5555-4555-8555-555555555555", false), "linear.app/acme/", "linear.app/other/", 1), strings.Replace(linearStatusJSON("completed", "55555555-5555-4555-8555-555555555555", false), `"archivedAt":null`, `"archivedAt":"2026-10-14T00:00:00Z"`, 1)} {
		client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })})
		if _, err := client.ReadStatus(context.Background(), "https://linear.app/acme/issue/PAY-42", "secret"); err == nil {
			t.Fatal("unsupported or foreign Linear issue accepted")
		}
	}
}

func TestGitHubReopenClearsCloseReason(t *testing.T) {
	state, reason := "closed", "completed"
	writes := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PATCH" {
			writes++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			value, exists := body["state_reason"]
			if body["state"] != "open" || !exists || value != nil {
				t.Fatal("reopen retained a close reason")
			}
			state, reason = "open", ""
		}
		return response(200, githubStateJSON(state, reason)), nil
	})})
	observed, err := client.ReadStatus(context.Background(), "https://github.com/owner/repo/issues/42", "secret")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.SetStatus(context.Background(), observed, "secret", "open", "")
	if err != nil || result.State != "open" || writes != 1 {
		t.Fatal("reopen not confirmed", err)
	}
}
