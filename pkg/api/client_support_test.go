package api_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClientSupportCustomerRoutes(t *testing.T) {
	const key = "00000000-0000-4000-8000-000000000123"
	const after int64 = 9007199254740993
	ctx := api.ContextWithIdempotencyKey(t.Context(), key)
	options := api.SupportTicketListOptions{Cursor: "cursor-1", Status: "resolved", Search: "broken? &status=open +日本語"}
	create := api.CreateSupportTicketRequest{Subject: "Help", Category: "account", Body: "Question", Context: api.SupportContext{AppID: "app-1"}}
	reply := api.SupportMessageRequest{ExpectedRevision: 2, Body: "Reply"}
	closeRequest := api.CloseSupportTicketRequest{ExpectedRevision: 2}
	reopen := api.ReopenSupportTicketRequest{ExpectedRevision: 2, Body: "Still failing"}
	rating := api.SupportSatisfactionRequest{EpisodeID: "episode-1", Rating: 5}
	const ticket = "ticket/one"
	const org = "org/team"
	for _, scope := range []string{"personal", "organization"} {
		t.Run(scope, func(t *testing.T) {
			base := "/v1/support/tickets"
			if scope == "organization" {
				base = "/v1/orgs/org%2Fteam/support/tickets"
			}
			type call func(*api.Client) (any, error)
			var listCall, createCall, getCall, messagesCall, replyCall, closeCall, reopenCall, surveyCall, ratingCall call
			if scope == "personal" {
				listCall = func(c *api.Client) (any, error) { return c.ListSupportTickets(ctx, options) }
				createCall = func(c *api.Client) (any, error) { return c.CreateSupportTicket(ctx, create) }
				getCall = func(c *api.Client) (any, error) { return c.GetSupportTicket(ctx, ticket) }
				messagesCall = func(c *api.Client) (any, error) { return c.ListSupportMessages(ctx, ticket, after) }
				replyCall = func(c *api.Client) (any, error) { return c.ReplySupportTicket(ctx, ticket, reply) }
				closeCall = func(c *api.Client) (any, error) { return c.CloseSupportTicket(ctx, ticket, closeRequest) }
				reopenCall = func(c *api.Client) (any, error) { return c.ReopenSupportTicket(ctx, ticket, reopen) }
				surveyCall = func(c *api.Client) (any, error) { return c.GetSupportSatisfaction(ctx, ticket) }
				ratingCall = func(c *api.Client) (any, error) { return c.SetSupportSatisfaction(ctx, ticket, rating) }
			} else {
				listCall = func(c *api.Client) (any, error) { return c.ListOrgSupportTickets(ctx, org, options) }
				createCall = func(c *api.Client) (any, error) { return c.CreateOrgSupportTicket(ctx, org, create) }
				getCall = func(c *api.Client) (any, error) { return c.GetOrgSupportTicket(ctx, org, ticket) }
				messagesCall = func(c *api.Client) (any, error) { return c.ListOrgSupportMessages(ctx, org, ticket, after) }
				replyCall = func(c *api.Client) (any, error) { return c.ReplyOrgSupportTicket(ctx, org, ticket, reply) }
				closeCall = func(c *api.Client) (any, error) { return c.CloseOrgSupportTicket(ctx, org, ticket, closeRequest) }
				reopenCall = func(c *api.Client) (any, error) { return c.ReopenOrgSupportTicket(ctx, org, ticket, reopen) }
				surveyCall = func(c *api.Client) (any, error) { return c.GetOrgSupportSatisfaction(ctx, org, ticket) }
				ratingCall = func(c *api.Client) (any, error) { return c.SetOrgSupportSatisfaction(ctx, org, ticket, rating) }
			}
			submission := api.SupportSubmission{TicketID: "ticket-1", SubmissionID: "receipt-1", Revision: 3}
			const receiptJSON = `{"ticket_id":"ticket-1","submission_id":"receipt-1","revision":3}`
			for _, tc := range []struct {
				name, method, suffix, body, response string
				query                                url.Values
				call                                 call
				want                                 any
			}{
				{name: "list", method: "GET", query: url.Values{"cursor": {"cursor-1"}, "status": {"resolved"}, "search": {options.Search}}, call: listCall,
					response: `{"items":[{"id":"ticket-1","revision":2}],"next_cursor":"next-1"}`, want: api.SupportTicketPage{Items: []api.SupportTicket{{ID: "ticket-1", Revision: 2}}, NextCursor: "next-1"}},
				{name: "create", method: "POST", call: createCall, body: `{"subject":"Help","category":"account","body":"Question","context":{"app_id":"app-1"}}`, response: receiptJSON, want: submission},
				{name: "get", method: "GET", suffix: "/ticket%2Fone", call: getCall, response: `{"id":"ticket-1","revision":2}`, want: api.SupportTicket{ID: "ticket-1", Revision: 2}},
				{name: "messages", method: "GET", suffix: "/ticket%2Fone/messages", query: url.Values{"after": {"9007199254740993"}}, call: messagesCall,
					response: `{"items":[{"id":"message-1","sequence":9007199254740993,"body":"Answer"}],"next_cursor":9007199254740993}`,
					want:     api.SupportMessagePage{Items: []api.SupportMessage{{ID: "message-1", Sequence: after, Body: "Answer"}}, NextCursor: after}},
				{name: "reply", method: "POST", suffix: "/ticket%2Fone/messages", call: replyCall, body: `{"expected_revision":2,"body":"Reply"}`, response: receiptJSON, want: submission},
				{name: "close", method: "POST", suffix: "/ticket%2Fone/close", call: closeCall, body: `{"expected_revision":2}`, response: receiptJSON, want: submission},
				{name: "reopen", method: "POST", suffix: "/ticket%2Fone/reopen", call: reopenCall, body: `{"expected_revision":2,"body":"Still failing"}`, response: receiptJSON, want: submission},
				{name: "survey", method: "GET", suffix: "/ticket%2Fone/satisfaction", call: surveyCall, response: `{"episode_id":"episode-1","eligible":true}`, want: api.SupportSatisfaction{EpisodeID: "episode-1", Eligible: true}},
				{name: "rating", method: "POST", suffix: "/ticket%2Fone/satisfaction", call: ratingCall, body: `{"episode_id":"episode-1","rating":5}`, response: `{"episode_id":"episode-1","rating":5}`, want: api.SupportSatisfactionSubmission{EpisodeID: "episode-1", Rating: 5}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.Method != tc.method || r.URL.EscapedPath() != base+tc.suffix {
							t.Errorf("route = %s %s, want %s %s", r.Method, r.URL.EscapedPath(), tc.method, base+tc.suffix)
						}
						wantQuery := tc.query
						if wantQuery == nil {
							wantQuery = url.Values{}
						}
						if !reflect.DeepEqual(r.URL.Query(), wantQuery) || r.Header.Get("Authorization") != "Bearer token" {
							t.Errorf("unexpected query/auth: %v %q", r.URL.Query(), r.Header.Get("Authorization"))
						}
						data, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						w.Header().Set("Content-Type", "application/json")
						if tc.method == "POST" {
							var gotBody, wantBody any
							if json.Unmarshal(data, &gotBody) != nil || json.Unmarshal([]byte(tc.body), &wantBody) != nil || !reflect.DeepEqual(gotBody, wantBody) {
								t.Errorf("body = %s, want %s", data, tc.body)
							}
							if r.Header.Get("Idempotency-Key") != key || r.Header.Get("Content-Type") != "application/json" {
								t.Errorf("mutation headers = %v", r.Header)
							}
							w.WriteHeader(http.StatusAccepted)
						} else if len(data) != 0 || r.Header.Get("Idempotency-Key") != "" {
							t.Error("read request carried mutation data")
						}
						_, _ = io.WriteString(w, tc.response)
					}))
					defer server.Close()
					client := api.NewClient(server.URL, "token").WithProjectOrganization("unrelated-project-org")
					attempts := 1
					if tc.method == "POST" {
						attempts = 2 // An explicit caller retry must preserve the key and payload.
					}
					for range attempts {
						got, err := tc.call(client)
						if err != nil || !reflect.DeepEqual(got, tc.want) {
							t.Fatalf("result = %#v, %v; want %#v", got, err, tc.want)
						}
					}
					if calls.Load() != int32(attempts) {
						t.Fatalf("requests = %d, want %d", calls.Load(), attempts)
					}
				})
			}
		})
	}
}

func TestClientSupportOrganizationErrorsNeverFallBackToPersonal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/orgs//support/tickets/ticket-1" {
			t.Errorf("organization request changed scope: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"type":"about:blank","title":"Forbidden","status":403,"code":"support_forbidden","detail":"Current organization membership is required."}`)
	}))
	defer server.Close()
	_, err := api.NewClient(server.URL, "token").GetOrgSupportTicket(t.Context(), "", "ticket-1")
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != 403 || problem.Problem.Code != "support_forbidden" || calls.Load() != 1 {
		t.Fatalf("error = %v, requests = %d", err, calls.Load())
	}
}
