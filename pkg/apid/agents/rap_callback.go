package agents

// RAP callbacks (ADR-20261030125501 §3). A tool built for the Reactive Agent
// Protocol posts RAP's own messages to its callback URL, so Infinity's RAP
// tools work unchanged: the post is published into the thread as one inbox
// message whose data is the RAP message itself, and the agent converts it
// back into runtime input.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
)

// RAP callback message types (RAP spec, "Request Transport").
const (
	rapToolResult        = "tool_result"
	rapSubscriptionEvent = "subscription_event"
	rapViewUpdate        = "view_update"
	rapOAuth             = "oauth"
	rapUserChoice        = "user_choice"
)

// rapCallback is the part of a RAP callback the inbox needs. Every RAP
// callback names its thread in group_id; a tool result names its call in id.
type rapCallback struct {
	Type    string `json:"type"`
	GroupID string `json:"group_id"`
	ID      string `json:"id"`
}

// decodeCallbackPost reads a post in either shape: Gregale's
// {message_id, kind, data}, or a RAP callback, which has a type. A RAP view
// update is a display side channel with no agent input, so it is
// acknowledged and not published: ok is false with no problem.
func decodeCallbackPost(r *http.Request, grant CallbackToken) (post api.AgentCallbackPost, ok bool, problem *api.Problem) {
	var raw json.RawMessage
	if err := httpkit.DecodeJSONSized(r, &raw, api.AgentCallbackPayloadMaxBytes+callbackEnvelopeMaxBytes); err != nil {
		return post, false, api.ErrValidation("the body must be a JSON callback of at most the payload limit")
	}
	var shape struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &shape) == nil && shape.Type != "" {
		return rapPost(grant, raw)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&post); err != nil {
		return post, false, api.ErrValidation("the body must be a JSON callback of at most the payload limit")
	}
	return post, true, nil
}

// rapPost turns a RAP callback into an inbox post. Its group_id must be the
// URL's thread. A tool result's message_id derives from its call ID, so a
// retried result lands once. A subscription event carries no identity of its
// own, so each post is a new message, as in Infinity's receiver.
func rapPost(grant CallbackToken, raw json.RawMessage) (api.AgentCallbackPost, bool, *api.Problem) {
	var msg rapCallback
	if err := json.Unmarshal(raw, &msg); err != nil {
		return api.AgentCallbackPost{}, false, api.ErrValidation("the body must be a RAP callback: " + err.Error())
	}
	if msg.GroupID != grant.ThreadID {
		return api.AgentCallbackPost{}, false, api.NewProblem(http.StatusForbidden, api.CodeValidation, "Thread mismatch",
			fmt.Sprintf("this callback URL posts to thread %s; the callback's group_id is %s", strconv.Quote(grant.ThreadID), strconv.Quote(msg.GroupID)))
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return api.AgentCallbackPost{}, false, api.ErrValidation("the body must be a RAP callback: " + err.Error())
	}
	post := api.AgentCallbackPost{Data: compact.Bytes()}
	switch msg.Type {
	case rapToolResult:
		if msg.ID == "" {
			return post, false, api.ErrValidation("a RAP tool_result must name its tool call in id")
		}
		post.Kind, post.MessageID = api.AgentCallbackKindToolResult, rapMessageID("rap-tool-result-", msg.ID)
	case rapSubscriptionEvent:
		post.Kind, post.MessageID = api.AgentCallbackKindEvent, "rap-event-"+randomHex(16)
	case rapViewUpdate:
		return post, false, nil
	case rapOAuth, rapUserChoice:
		return post, false, api.ErrValidation(fmt.Sprintf("RAP %s callbacks are not supported yet; post a tool_result or subscription_event", msg.Type))
	default:
		return post, false, api.ErrValidation("unknown RAP callback type " + strconv.Quote(msg.Type))
	}
	return post, true, nil
}

// rapMessageID is prefix+id when that is a valid message ID, and otherwise
// prefix plus a digest of id, so any tool call ID maps to one message ID.
func rapMessageID(prefix, id string) string {
	if ValidCallbackID(prefix+id, api.AgentCallbackMessageIDMaxBytes) {
		return prefix + id
	}
	sum := sha256.Sum256([]byte(id))
	return prefix + "h" + hex.EncodeToString(sum[:16])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
