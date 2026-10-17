package issuetracker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func requestFixture() Request {
	return Request{RequestID: "11111111-1111-4111-8111-111111111111", APIBase: "https://gregale.example", AppID: "22222222-2222-4222-8222-222222222222", AppSlug: "payments", IssueID: "33333333-3333-4333-8333-333333333333", Provider: "github", Target: "owner/repo", Title: "Fix checkout", Description: "Investigate checkout failures."}
}
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func githubResponse(r Request) string {
	out, _ := json.Marshal(map[string]any{"html_url": "https://github.com/owner/repo/issues/42", "number": 42, "body": description(r)})
	return string(out)
}
func linearResponseBody(r Request, create bool) string {
	issue := map[string]any{"id": r.RequestID, "team": map[string]string{"id": r.Target}, "url": "https://linear.app/acme/issue/PAY-42/fix-checkout", "identifier": "PAY-42"}
	data := map[string]any{"issue": issue}
	if create {
		data = map[string]any{"issueCreate": map[string]any{"success": true, "issue": issue}}
	}
	out, _ := json.Marshal(map[string]any{"data": data})
	return string(out)
}
func TestGitHubCreateAndRecoverBoundToOperationAndRepository(t *testing.T) {
	r := requestFixture()
	calls := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Host != "api.github.com" || req.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("wrong provider destination or authentication")
		}
		if calls == 1 {
			if req.Method != "POST" || req.URL.Path != "/repos/owner/repo/issues" {
				t.Fatal("wrong create request")
			}
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["title"] != r.Title || body["body"] != description(r) {
				t.Fatal("operation marker missing", err)
			}
		} else if req.Method != "GET" || req.URL.Path != "/repos/owner/repo/issues/42" {
			t.Fatal("wrong recovery request")
		}
		return response(200, githubResponse(r)), nil
	})})
	ticket, err := client.Create(context.Background(), r, "secret")
	if err != nil || ticket.ProviderID != "42" {
		t.Fatalf("create: %+v %v", ticket, err)
	}
	recovered, err := client.Recover(context.Background(), r, "secret", ticket.URL+"#comment")
	if err != nil || recovered != ticket {
		t.Fatalf("recover: %+v %v", recovered, err)
	}
	if _, err := client.Recover(context.Background(), r, "secret", "https://github.com/other/repo/issues/42"); err == nil || calls != 2 {
		t.Fatal("cross-repository recovery sent a request")
	}
}
func TestLinearCreateUsesStableUUIDAndRecoveryVerifiesTeam(t *testing.T) {
	r := requestFixture()
	r.Provider = "linear"
	r.Target = "44444444-4444-4444-8444-444444444444"
	calls := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != "https://api.linear.app/graphql" || req.Header.Get("Authorization") != "lin_api_secret" {
			t.Fatal("wrong Linear destination or authentication")
		}
		var body struct {
			Query     string                     `json:"query"`
			Variables map[string]json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if calls == 1 {
			var input map[string]string
			_ = json.Unmarshal(body.Variables["input"], &input)
			if input["id"] != r.RequestID || input["teamId"] != r.Target || input["description"] != description(r) {
				t.Fatal("missing stable identity or team")
			}
		} else {
			var id string
			_ = json.Unmarshal(body.Variables["id"], &id)
			if id != r.RequestID {
				t.Fatal("recovery searched a different operation")
			}
		}
		return response(200, linearResponseBody(r, calls == 1)), nil
	})})
	ticket, err := client.Create(context.Background(), r, "lin_api_secret")
	if err != nil || ticket.ProviderID != r.RequestID || ticket.URL != "https://linear.app/acme/issue/PAY-42" {
		t.Fatalf("create: %+v %v", ticket, err)
	}
	if _, err := client.Recover(context.Background(), r, "lin_api_secret", ticket.URL); err != nil {
		t.Fatal(err)
	}
	wrong := r
	wrong.Target = "55555555-5555-4555-8555-555555555555"
	if _, err := client.Recover(context.Background(), wrong, "lin_api_secret", ticket.URL); err == nil {
		t.Fatal("wrong-team ticket accepted")
	}
}
func TestProviderFailuresNeverExposeBodiesOrCredentials(t *testing.T) {
	r := requestFixture()
	for _, status := range []int{401, 422, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(status, "secret credential and provider diagnostics"), nil
			})})
			_, err := client.Create(context.Background(), r, "secret")
			var rejected *RejectedError
			if errors.As(err, &rejected) != (status == 401 || status == 422) {
				t.Fatalf("unsafe retry classification: %v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("provider body leaked")
			}
		})
	}
	for _, body := range []string{"not JSON", strings.Repeat("x", api.IssueTrackerResponseMaxBytes+1), `{"number":42,"html_url":"https://github.com/other/repo/issues/42"}`} {
		client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })})
		if _, err := client.Create(context.Background(), r, "secret"); err == nil {
			t.Fatal("unconfirmed response accepted")
		}
	}
	calls := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		out := response(302, "")
		out.Header.Set("Location", "https://evil.example/collect")
		return out, nil
	})})
	if _, err := client.Create(context.Background(), r, "secret"); err == nil || calls != 1 {
		t.Fatal("provider redirect was followed")
	}
}
func TestRecoveryRejectsUnmarkedIssuesAndPullRequests(t *testing.T) {
	r := requestFixture()
	for _, body := range []string{`{"number":42,"html_url":"https://github.com/owner/repo/issues/42","body":"unrelated"}`, strings.TrimSuffix(githubResponse(r), "}") + `,"pull_request":{}}`} {
		client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })})
		if _, err := client.Recover(context.Background(), r, "secret", "https://github.com/owner/repo/issues/42"); err == nil {
			t.Fatal("unrelated issue or pull request accepted")
		}
	}
}

func TestTicketInputValidationBoundsIdentityAndProviderTargets(t *testing.T) {
	for _, change := range []func(*Request){
		func(r *Request) { r.Target = "owner/repo/issues/1?ignored=" },
		func(r *Request) { r.Target = "../repo" },
		func(r *Request) { r.APIBase = "https://credential@gregale.example" },
		func(r *Request) { r.APIBase = "https://gregale.example?token=secret" },
		func(r *Request) { r.AppSlug = strings.Repeat("x", api.IssueTrackerLinkURLMaxBytes+1) },
		func(r *Request) { r.Title = strings.Repeat("x", api.IssueTrackerLinkTitleMaxBytes+1) },
		func(r *Request) { r.Description = strings.Repeat("x", api.IssueTrackerTicketBodyMaxBytes+1) },
		func(r *Request) { r.RequestID = "../../operation" },
	} {
		r := requestFixture()
		change(&r)
		if _, err := ValidateRequest(r); err == nil {
			t.Fatal("invalid ticket inputs accepted")
		}
	}
}

func TestLinearRejectsUnsupportedIssueUUIDVersionBeforeDispatch(t *testing.T) {
	r := requestFixture()
	r.Provider = "linear"
	r.Target = "44444444-4444-4444-8444-444444444444"
	r.RequestID = "11111111-1111-7111-8111-111111111111"
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid UUID reached the provider")
		return nil, nil
	})})
	if _, err := client.Create(context.Background(), r, "secret"); err == nil {
		t.Fatal("unsupported UUID accepted")
	}
}

func TestLinearRecoveryWithoutURLVerifiesStableIDTeamAndGraphQLErrors(t *testing.T) {
	r := requestFixture()
	r.Provider = "linear"
	r.Target = "44444444-4444-4444-8444-444444444444"
	for _, kind := range []string{"valid", "id", "team", "partial"} {
		t.Run(kind, func(t *testing.T) {
			body := linearResponseBody(r, false)
			switch kind {
			case "id":
				body = strings.ReplaceAll(body, r.RequestID, "55555555-5555-4555-8555-555555555555")
			case "team":
				body = strings.ReplaceAll(body, r.Target, "55555555-5555-4555-8555-555555555555")
			case "partial":
				body = strings.Replace(body, `{"data":`, `{"errors":[{"message":"secret diagnostic"}],"data":`, 1)
			}
			client := NewClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				var input struct {
					Variables map[string]string `json:"variables"`
				}
				if err := json.NewDecoder(req.Body).Decode(&input); err != nil || input.Variables["id"] != r.RequestID {
					t.Fatal("recovery lost request UUID", err)
				}
				return response(200, body), nil
			})})
			ticket, err := client.Recover(t.Context(), r, "lin_api_secret", "")
			if (err == nil) != (kind == "valid") || err == nil && ticket.ProviderID != r.RequestID {
				t.Fatal("recovery identity not verified", err)
			}
		})
	}
}
