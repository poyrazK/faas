package mcpmanagement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Secrets reach Gregale without passing through the agent
// (ADR-20261026174727). The agent asks for one; the owner types it on a
// one-time Gregale page that URL-mode elicitation opens. This connection keeps
// the private status material and tells the agent only pending, set or
// expired. Requests live in connection memory: a restart forgets them.
const (
	maxSecretRequests     = 64
	defaultSecretWait     = 20
	maxSecretWait         = 45
	secretPollingInterval = 2 * time.Second
)

type secretRequest struct {
	ID, App, Key, Scope, EntryURL string
	ticket, exchangeCode          string
	expiresAt                     time.Time
	set                           bool
}

type requestSecretArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	Key    string `json:"key" jsonschema:"Secret name, for example DATABASE_URL"`
	Reason string `json:"reason" jsonschema:"Why the app needs it, shown to the user; at most 280 characters"`
}

type getSecretRequestArgs struct {
	RequestID   string `json:"request_id" jsonschema:"request_id from gregale_request_secret"`
	WaitSeconds *int   `json:"wait_seconds,omitempty" jsonschema:"Wait from 0 to 45 seconds for the user; default 20, 0 checks once"`
}

// toolRequestKey carries the MCP call into handlers that ask the user
// something through multi round-trip input requests (SEP-2322).
type toolRequestKey struct{}

// needsInput is a handler result the tool wrapper turns into an input-required
// call result. state is echoed back on the retry.
type needsInput struct {
	requests mcp.InputRequestMap
	state    string
}

const secretInputID = "secret_entry"

func registerSecretEntry(s *mcp.Server, m *management) {
	registerTool(s, m, "gregale_request_secret", "Ask the user to set a secret for an app on a one-time Gregale page; the value never passes through you. Opens the page in the client when it supports URL elicitation; otherwise give the user entry_url. Then call gregale_get_secret_request. Never ask the user to paste a secret into the conversation.", false, m.requestSecret)
	register(s, m, "gregale_get_secret_request", "Check whether the user has set a secret requested with gregale_request_secret: pending, set or expired. Never returns the value.", m.getSecretRequest)
}

func (m *management) requestSecret(ctx context.Context, args requestSecretArgs) (any, error) {
	call, _ := ctx.Value(toolRequestKey{}).(*mcp.CallToolRequest)
	if call != nil && call.Params != nil && call.Params.RequestState != "" {
		// The retry after the client opened the page: report what the user did.
		m.mu.Lock()
		req := m.secretRequests[call.Params.RequestState]
		m.mu.Unlock()
		if req == nil || req.App != args.App || req.Key != args.Key {
			return nil, invalid("unknown_request", "the secret request expired or belongs to another call; request the secret again")
		}
		action := "cancel"
		if answer, ok := call.Params.InputResponses[secretInputID].(*mcp.ElicitResult); ok {
			action = answer.Action
		}
		return secretRequestResult(req, action), nil
	}
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	if prob := api.ValidateSecretKey(args.Key); prob != nil {
		return nil, invalid("invalid_argument", prob.Detail)
	}
	if args.Reason == "" || !utf8.ValidString(args.Reason) || utf8.RuneCountInString(args.Reason) > 280 {
		return nil, invalid("invalid_argument", "reason must contain between 1 and 280 valid Unicode characters")
	}
	entry, err := m.client.RequestSecretEntry(ctx, args.App, api.SecretEntryRequest{Key: args.Key, Reason: args.Reason})
	if err != nil {
		return nil, err
	}
	expires, err := time.Parse(time.RFC3339Nano, entry.ExpiresAt)
	if err != nil || entry.EntryURL == "" || len(entry.ExchangeCode) != 43 {
		return nil, invalid("invalid_response", "API returned an incomplete secret entry")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	req := &secretRequest{ID: hex.EncodeToString(id), App: args.App, Key: args.Key, Scope: api.DefaultEnvScope, EntryURL: entry.EntryURL, ticket: entry.Ticket, exchangeCode: entry.ExchangeCode, expiresAt: expires}
	if err := m.saveSecretRequest(req); err != nil {
		return nil, err
	}
	if !clientOpensURLs(call) {
		return secretRequestResult(req, "unsupported"), nil
	}
	// The client opens the page and retries this call with the user's answer.
	return &needsInput{state: req.ID, requests: mcp.InputRequestMap{secretInputID: &mcp.ElicitParams{
		Mode:          "url",
		Message:       fmt.Sprintf("Set %s for %s on Gregale. The value goes straight to Gregale; the agent never sees it.", req.Key, req.App),
		URL:           req.EntryURL,
		ElicitationID: req.ID,
	}}}, nil
}

func clientOpensURLs(call *mcp.CallToolRequest) bool {
	if call == nil || call.Session == nil {
		return false
	}
	params := call.Session.InitializeParams()
	return params != nil && params.Capabilities != nil && params.Capabilities.Elicitation != nil && params.Capabilities.Elicitation.URL != nil
}

func secretRequestResult(req *secretRequest, action string) map[string]any {
	out := map[string]any{"request_id": req.ID, "app": req.App, "key": req.Key, "scope": req.Scope, "status": api.SecretEntryPending, "expires_at": req.expiresAt.Format(time.RFC3339)}
	switch action {
	case "accept":
		out["elicitation"] = "opened"
		out["next"] = "Call gregale_get_secret_request once the user has saved the secret."
	case "unsupported":
		// The client can't open pages: the user must, and the link works only
		// in their signed-in Gregale session.
		out["elicitation"], out["entry_url"] = action, req.EntryURL
		out["next"] = "Give the user entry_url to open in a browser where they are signed in to Gregale, then call gregale_get_secret_request."
	default:
		out["elicitation"], out["entry_url"] = action, req.EntryURL
		out["next"] = "The user declined the page. Ask whether to try again; the link stays valid until expires_at."
	}
	return out
}

func (m *management) saveSecretRequest(req *secretRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, old := range m.secretRequests {
		if time.Now().After(old.expiresAt) {
			delete(m.secretRequests, id)
		}
	}
	if len(m.secretRequests) >= maxSecretRequests {
		return invalid("request_capacity", "too many open secret requests on this connection; wait for them to expire")
	}
	m.secretRequests[req.ID] = req
	return nil
}

func (m *management) getSecretRequest(ctx context.Context, args getSecretRequestArgs) (any, error) {
	wait := defaultSecretWait
	if args.WaitSeconds != nil {
		wait = *args.WaitSeconds
	}
	if wait < 0 || wait > maxSecretWait {
		return nil, invalid("invalid_argument", fmt.Sprintf("wait_seconds must be between 0 and %d", maxSecretWait))
	}
	m.mu.Lock()
	req := m.secretRequests[args.RequestID]
	m.mu.Unlock()
	if req == nil {
		return nil, invalid("unknown_request", "request_id is unknown, expired, or belongs to another connection; request the secret again")
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	if outer, ok := ctx.Deadline(); ok && outer.Before(deadline) {
		deadline = outer.Add(-100 * time.Millisecond)
	}
	for {
		status, err := m.secretRequestStatus(ctx, req)
		if err != nil || status != api.SecretEntryPending || !time.Now().Add(secretPollingInterval).Before(deadline) {
			if err != nil {
				return nil, err
			}
			return map[string]any{"request_id": req.ID, "app": req.App, "key": req.Key, "scope": req.Scope, "status": status}, nil
		}
		timer := time.NewTimer(secretPollingInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// secretRequestStatus is pending, set or expired. A set secret stays set even
// after the request itself expires.
func (m *management) secretRequestStatus(ctx context.Context, req *secretRequest) (string, error) {
	m.mu.Lock()
	set := req.set
	m.mu.Unlock()
	if set {
		return api.SecretEntrySet, nil
	}
	status, err := m.client.GetSecretEntryStatus(ctx, req.App, api.SecretEntryStatusRequest{Ticket: req.ticket, ExchangeCode: req.exchangeCode})
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == 410 {
		return "expired", nil
	}
	if err != nil {
		return "", err
	}
	if status.App != req.App || status.Key != req.Key {
		return "", invalid("invalid_response", "API returned a different secret request")
	}
	if status.Status == api.SecretEntrySet {
		m.mu.Lock()
		req.set = true
		m.mu.Unlock()
	}
	return status.Status, nil
}
