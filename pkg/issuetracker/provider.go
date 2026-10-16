// Package issuetracker creates provider tickets and retains local recovery evidence.
package issuetracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Request contains no credentials. RequestID must be reused for every retry.
type Request struct {
	RequestID   string `json:"request_id"`
	APIBase     string `json:"api_base"`
	AppID       string `json:"app_id"`
	AppSlug     string `json:"app_slug"`
	IssueID     string `json:"issue_id"`
	Provider    string `json:"provider"`
	Target      string `json:"target"`
	Title       string `json:"title"`
	Description string `json:"description"`
}
type Ticket struct {
	URL        string `json:"url"`
	ProviderID string `json:"provider_id"`
}

type Provider interface {
	Create(context.Context, Request, string) (Ticket, error)
	Recover(context.Context, Request, string, string) (Ticket, error)
}

// RejectedError means the provider explicitly rejected the write before creation.
// Other errors leave the outcome uncertain and must not trigger another create.
type RejectedError struct{ Status int }

func (e *RejectedError) Error() string {
	return fmt.Sprintf("tracker rejected ticket creation (HTTP %d)", e.Status)
}

type Client struct{ http *http.Client }

func NewClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copy.Timeout == 0 || copy.Timeout > time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second {
		copy.Timeout = time.Duration(api.IssueTrackerRequestTimeoutSeconds) * time.Second
	}
	return &Client{http: &copy}
}
func ValidateRequest(r Request) (Request, error) { return validateRequest(r, true) }

// ValidateInputs validates CLI inputs before the app identity is loaded.
func ValidateInputs(r Request) (Request, error) { return validateRequest(r, false) }
func validateRequest(r Request, requireApp bool) (Request, error) {
	if requireApp || r.AppID != "" {
		id, err := uuid.Parse(r.AppID)
		if err != nil || id == uuid.Nil {
			return r, errors.New("app identity must be a nonzero UUID")
		}
		r.AppID = id.String()
	}
	for _, id := range []*string{&r.RequestID, &r.IssueID} {
		parsed, err := uuid.Parse(*id)
		if err != nil || parsed == uuid.Nil {
			return r, errors.New("request, app and issue IDs must be nonzero UUIDs")
		}
		*id = parsed.String()
	}
	origin, err := url.Parse(r.APIBase)
	if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || len(r.APIBase) > api.IssueTrackerLinkURLMaxBytes || r.AppSlug == "" || len(r.AppSlug) > api.IssueTrackerLinkURLMaxBytes || !utf8.ValidString(r.AppSlug) || strings.IndexFunc(r.AppSlug, unicode.IsControl) >= 0 {
		return r, errors.New("gregale API and app identity are invalid")
	}
	r.APIBase = strings.TrimRight(r.APIBase, "/")
	r.Title = strings.TrimSpace(r.Title)
	if r.Title == "" || len(r.Title) > api.IssueTrackerLinkTitleMaxBytes || !utf8.ValidString(r.Title) || strings.IndexFunc(r.Title, unicode.IsControl) >= 0 {
		return r, errors.New("ticket title is invalid or oversized")
	}
	if len(r.Description) > api.IssueTrackerTicketBodyMaxBytes || !utf8.ValidString(r.Description) || strings.ContainsRune(r.Description, 0) {
		return r, errors.New("ticket description is invalid or oversized")
	}
	switch r.Provider {
	case "github":
		if len(strings.Split(r.Target, "/")) != 2 || strings.ContainsAny(r.Target, "?#\\") {
			return r, errors.New("GitHub target must be OWNER/REPO")
		}
		n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: "https://github.com/" + r.Target + "/issues/1"})
		if err != nil {
			return r, errors.New("GitHub target must be OWNER/REPO")
		}
		r.Target = strings.TrimSuffix(n.ExternalKey, "#1")
	case "linear":
		requestID, _ := uuid.Parse(r.RequestID)
		if requestID.Version() != 4 || requestID.Variant() != uuid.RFC4122 {
			return r, errors.New("linear request-id must be a UUID v4")
		}
		id, err := uuid.Parse(r.Target)
		if err != nil || id == uuid.Nil {
			return r, errors.New("linear target must be a team UUID")
		}
		r.Target = id.String()
	default:
		return r, errors.New("ticket provider must be github or linear")
	}
	return r, nil
}
func marker(r Request) string      { return "<!-- gregale-issue-operation: " + r.RequestID + " -->" }
func description(r Request) string { return r.Description + "\n\n" + marker(r) }
func validateToken(token string) error {
	if token == "" || len(token) > api.IssueTrackerCredentialMaxBytes || strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("provider credential is missing or invalid")
	}
	return nil
}
func validateProviderToken(provider, token string) error {
	if provider == "linear" {
		token = strings.TrimPrefix(token, "Bearer ")
	}
	return validateToken(token)
}
func (c *Client) request(ctx context.Context, provider, method, path, token string, body any, out any, write bool) error {
	if err := validateProviderToken(provider, token); err != nil {
		return err
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	origin := "https://api.github.com"
	if provider == "linear" {
		origin = "https://api.linear.app"
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid provider request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if provider == "linear" {
		req.Header.Set("Authorization", token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gregale-issues")
	if provider == "github" {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return errors.New("tracker request did not return a confirmed outcome")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if !write && response.StatusCode == 401 {
			return &ProviderUnauthorized{Provider: provider}
		}
		if write && (response.StatusCode == 400 || response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 404 || response.StatusCode == 422) {
			return &RejectedError{Status: response.StatusCode}
		}
		return fmt.Errorf("tracker returned HTTP %d; request outcome is unconfirmed", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, api.IssueTrackerResponseMaxBytes+1))
	if err != nil || len(payload) > api.IssueTrackerResponseMaxBytes {
		return errors.New("tracker response is unreadable or oversized")
	}
	if provider == "linear" && !write {
		var envelope struct {
			Errors []struct {
				Extensions struct {
					Code string `json:"code"`
				} `json:"extensions"`
			} `json:"errors"`
		}
		if json.Unmarshal(payload, &envelope) == nil {
			for _, e := range envelope.Errors {
				if e.Extensions.Code == "UNAUTHENTICATED" || e.Extensions.Code == "AUTHENTICATION_ERROR" {
					return &ProviderUnauthorized{Provider: provider}
				}
			}
		}
	}
	if json.Unmarshal(payload, out) != nil {
		return errors.New("tracker response is invalid")
	}
	return nil
}

type githubIssue struct {
	URL         string          `json:"html_url"`
	Number      int64           `json:"number"`
	Body        string          `json:"body"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func githubTicket(r Request, g githubIssue) (Ticket, error) {
	n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: g.URL})
	if err != nil || n.Provider != "github" || n.ExternalKey != r.Target+"#"+strconv.FormatInt(g.Number, 10) || g.Number <= 0 || len(g.PullRequest) > 0 && string(g.PullRequest) != "null" {
		return Ticket{}, errors.New("tracker returned an inconsistent GitHub issue")
	}
	return Ticket{URL: n.URL, ProviderID: strconv.FormatInt(g.Number, 10)}, nil
}

type linearIssue struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	Identifier string `json:"identifier"`
	Team       struct {
		ID string `json:"id"`
	} `json:"team"`
}
type linearResponse struct {
	Data struct {
		Create struct {
			Success bool         `json:"success"`
			Issue   *linearIssue `json:"issue"`
		} `json:"issueCreate"`
		Issue *linearIssue `json:"issue"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

func linearTicket(r Request, l *linearIssue) (Ticket, error) {
	if l == nil {
		return Ticket{}, errors.New("linear did not confirm an issue")
	}
	id, e1 := uuid.Parse(l.ID)
	team, e2 := uuid.Parse(l.Team.ID)
	n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: l.URL})
	if e1 != nil || e2 != nil || id.String() != r.RequestID || team.String() != r.Target || err != nil || n.Provider != "linear" || !strings.HasSuffix(n.URL, "/issue/"+l.Identifier) {
		return Ticket{}, errors.New("tracker returned an inconsistent Linear issue")
	}
	return Ticket{URL: n.URL, ProviderID: id.String()}, nil
}
func (c *Client) Create(ctx context.Context, r Request, token string) (Ticket, error) {
	var err error
	r, err = ValidateRequest(r)
	if err != nil {
		return Ticket{}, err
	}
	if err = validateProviderToken(r.Provider, token); err != nil {
		return Ticket{}, err
	}
	if r.Provider == "github" {
		var out githubIssue
		err = c.request(ctx, "github", "POST", "/repos/"+r.Target+"/issues", token, map[string]string{"title": r.Title, "body": description(r)}, &out, true)
		if err != nil {
			return Ticket{}, err
		}
		if !strings.Contains(out.Body, marker(r)) {
			return Ticket{}, errors.New("GitHub did not confirm the operation marker")
		}
		return githubTicket(r, out)
	}
	var out linearResponse
	err = c.request(ctx, "linear", "POST", "/graphql", token, map[string]any{"query": "mutation GregaleIssueCreate($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { id url identifier team { id } } } }", "variables": map[string]any{"input": map[string]string{"id": r.RequestID, "teamId": r.Target, "title": r.Title, "description": description(r)}}}, &out, true)
	if err != nil {
		return Ticket{}, err
	}
	if len(out.Errors) > 0 || !out.Data.Create.Success {
		return Ticket{}, errors.New("linear request outcome is unconfirmed")
	}
	return linearTicket(r, out.Data.Create.Issue)
}
func (c *Client) Recover(ctx context.Context, r Request, token, recoveryURL string) (Ticket, error) {
	var err error
	r, err = ValidateRequest(r)
	if err != nil {
		return Ticket{}, err
	}
	n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: recoveryURL})
	if (r.Provider != "linear" || recoveryURL != "") && (err != nil || n.Provider != r.Provider) {
		return Ticket{}, errors.New("recovery URL must match the selected provider")
	}
	if r.Provider == "github" {
		if !strings.HasPrefix(n.ExternalKey, r.Target+"#") {
			return Ticket{}, errors.New("recovery issue belongs to a different repository")
		}
		number := strings.TrimPrefix(n.ExternalKey, r.Target+"#")
		var out githubIssue
		if err = c.request(ctx, "github", "GET", "/repos/"+r.Target+"/issues/"+number, token, nil, &out, false); err != nil {
			return Ticket{}, err
		}
		if !strings.Contains(out.Body, marker(r)) {
			return Ticket{}, errors.New("recovery issue does not carry this request's operation marker")
		}
		return githubTicket(r, out)
	}
	var out linearResponse
	err = c.request(ctx, "linear", "POST", "/graphql", token, map[string]any{"query": "query GregaleIssueRecovery($id: String!) { issue(id: $id) { id url identifier team { id } } }", "variables": map[string]string{"id": r.RequestID}}, &out, false)
	if err != nil {
		return Ticket{}, err
	}
	ticket, err := linearTicket(r, out.Data.Issue)
	if len(out.Errors) > 0 || err != nil || recoveryURL != "" && ticket.URL != n.URL {
		return Ticket{}, errors.New("linear recovery issue does not match this request")
	}
	return ticket, nil
}
