package agents

// Agent callbacks (ADR-20261030125501 §3). A stateless agent's handler asks
// for a callback URL for one thread when it dispatches an asynchronous tool
// call; the tool posts its result there; apid publishes the result into that
// thread's lane of the endpoint's agent-inbox channel, which the app's
// realtime trigger delivers as the thread's next slice. The URL's token is
// the only credential, and it cannot address another thread, endpoint or
// account. A repeated message_id lands once.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// CallbackInbox is the realtime state callbacks need; *state.PgStore
// implements it.
type CallbackInbox interface {
	ManagedRealtimeEndpointByID(ctx context.Context, id string) (state.ManagedRealtimeEndpoint, error)
	state.ManagedRealtimeMetadataStore
}

// callbackRequestMaxBytes bounds an issue request; a post is bounded by its
// payload limit plus room for its envelope.
const (
	callbackRequestMaxBytes  = 4 << 10
	callbackEnvelopeMaxBytes = 1 << 10
)

// CreateCallback handles POST /v1/agent-callbacks.
func (h *Handler) CreateCallback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if h.Callbacks == nil || h.Inbox == nil {
		api.WriteProblem(w, api.ErrCapacity("agent callbacks are unavailable on this control-plane host"))
		return
	}
	var req api.CreateAgentCallbackRequest
	if err := httpkit.DecodeJSONSized(r, &req, callbackRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("the body must be a JSON callback request"))
		return
	}
	grant, problem := h.callbackGrant(r.Context(), acct, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	token, err := h.Callbacks.Sign(grant)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "issue callback", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusCreated, api.AgentCallbackResponse{URL: h.CallbackBaseURL + "/v1/agent-callbacks/" + token,
		ThreadID: grant.ThreadID, Kinds: grant.Kinds, ExpiresAt: grant.ExpiresAt})
}

// callbackGrant checks an issue request against the account's endpoints and
// returns the token it grants.
func (h *Handler) callbackGrant(ctx context.Context, acct state.Account, req api.CreateAgentCallbackRequest) (CallbackToken, *api.Problem) {
	kinds, ttl := req.Kinds, req.TTLSeconds
	if len(kinds) == 0 {
		kinds = []string{api.AgentCallbackKindToolResult}
	}
	if ttl == 0 {
		ttl = api.AgentCallbackTokenTTLSec
	}
	switch {
	case !ValidCallbackID(req.ThreadID, api.AgentCallbackThreadIDMaxBytes):
		return CallbackToken{}, api.ErrValidation(fmt.Sprintf("thread_id must be 1-%d bytes of letters, digits, '.', '_', ':' and '-'", api.AgentCallbackThreadIDMaxBytes))
	case !ValidCallbackKinds(kinds):
		return CallbackToken{}, api.ErrValidation("kinds must be distinct values of tool_result and event")
	case ttl < 1 || ttl > api.AgentCallbackTokenTTLSec:
		return CallbackToken{}, api.ErrValidation(fmt.Sprintf("ttl_seconds must be 1-%d; got %d", api.AgentCallbackTokenTTLSec, ttl))
	}
	endpoint, err := h.Inbox.ManagedRealtimeEndpointByID(ctx, req.EndpointID)
	if err != nil || endpoint.AccountID != acct.ID {
		return CallbackToken{}, errRealtimeEndpointNotFound()
	}
	now := h.now().UTC().Truncate(time.Second)
	return CallbackToken{AccountID: acct.ID, EndpointID: endpoint.ID, ThreadID: req.ThreadID, Kinds: kinds, IssuedAt: now,
		ExpiresAt: now.Add(time.Duration(ttl) * time.Second)}, nil
}

// ReceiveCallback handles POST /v1/agent-callbacks/{token}. A token that is
// invalid, expired, for an account without the agents capability or for a
// deleted endpoint answers like an unknown route.
func (h *Handler) ReceiveCallback(w http.ResponseWriter, r *http.Request) {
	if h.Callbacks == nil || h.Inbox == nil {
		api.WriteProblem(w, api.ErrCapacity("agent callbacks are unavailable on this control-plane host"))
		return
	}
	grant, err := h.Callbacks.Verify(r.PathValue("token"), h.now())
	if err != nil || h.AgentsEnabled == nil || !h.AgentsEnabled(r.Context(), grant.AccountID) {
		http.NotFound(w, r)
		return
	}
	var post api.AgentCallbackPost
	if err := httpkit.DecodeJSONSized(r, &post, api.AgentCallbackPayloadMaxBytes+callbackEnvelopeMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("the body must be a JSON callback of at most the payload limit"))
		return
	}
	if problem := checkCallbackPost(grant, post); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	endpoint, err := h.Inbox.ManagedRealtimeEndpointByID(r.Context(), grant.EndpointID)
	if err != nil || endpoint.AccountID != grant.AccountID {
		http.NotFound(w, r)
		return
	}
	message, err := h.Inbox.AppendManagedRealtimeChannelMetadata(r.Context(), grant.EndpointID, api.AgentInboxChannel, post.Data, false,
		callbackIdempotencyKey(grant.ThreadID, post.MessageID), callbackMetadata(grant.ThreadID, post))
	if err != nil {
		api.WriteProblem(w, h.callbackPublishProblem(grant, err))
		return
	}
	httpkit.WriteJSON(w, http.StatusAccepted, api.AgentCallbackReceipt{Sequence: strconv.FormatInt(message.Sequence, 10), ThreadID: grant.ThreadID})
}

// checkCallbackPost checks one post against its token.
func checkCallbackPost(grant CallbackToken, post api.AgentCallbackPost) *api.Problem {
	switch {
	case !ValidCallbackID(post.MessageID, api.AgentCallbackMessageIDMaxBytes):
		return api.ErrValidation(fmt.Sprintf("message_id must be 1-%d bytes of letters, digits, '.', '_', ':' and '-'", api.AgentCallbackMessageIDMaxBytes))
	case !grant.Allows(post.Kind):
		return api.NewProblem(http.StatusForbidden, api.CodeValidation, "Kind not allowed", "this callback URL does not carry messages of kind "+strconv.Quote(post.Kind))
	case len(post.Data) == 0 || !json.Valid(post.Data) || string(bytes.TrimSpace(post.Data)) == "null":
		return api.ErrValidation("data must be a JSON value other than null")
	case len(post.Data) > api.AgentCallbackPayloadMaxBytes:
		return api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeValidation, "Callback too large",
			fmt.Sprintf("data is %d bytes; a callback carries at most AgentCallbackPayloadMaxBytes (%d)", len(post.Data), api.AgentCallbackPayloadMaxBytes))
	}
	return nil
}

// callbackIdempotencyKey makes a repeated post of one message land once in
// the thread; the realtime limit on keys is 128 bytes.
func callbackIdempotencyKey(threadID, messageID string) string {
	return "callback:" + threadID + ":" + messageID
}

// callbackMetadata is the inbox message's metadata: the thread, which the
// inbox's consumer group orders by, and what the message is.
func callbackMetadata(threadID string, post api.AgentCallbackPost) map[string]string {
	return map[string]string{"thread_id": threadID, "kind": post.Kind, "message_id": post.MessageID}
}

func (h *Handler) callbackPublishProblem(grant CallbackToken, err error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Message already posted",
			"this message_id was already posted to the thread with different data")
	case errors.Is(err, state.ErrNotFound):
		return errRealtimeEndpointNotFound()
	}
	return h.problem(state.Account{ID: grant.AccountID}, "publish callback", err)
}

func errRealtimeEndpointNotFound() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Realtime endpoint not found", "the account has no such realtime endpoint")
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
