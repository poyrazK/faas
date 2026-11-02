package agents

// Gregale's RAP toolset (ADR-20261030125501 §4). A stateless agent loads it
// like any RAP tool server: GET /v1/agent-tools/.well-known/rap-toolset
// describes the tools, and the runtime posts each invocation to
// /v1/agent-tools/invoke. The invocation's callback_url is its credential: it
// must be a callback URL apid issued for the invocation's own thread
// (callbacks.go), so a tool can only answer the thread that called it.
//
// gregale_sleep wakes the thread later. The wake-up is the call's tool result,
// held as a realtime schedule on the endpoint's agent inbox until it is due,
// so a sleeping agent holds nothing but that row. Revoking the thread's
// callback URLs cancels its pending sleeps.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// SleepSchedules is the realtime schedule state gregale_sleep needs;
// *state.PgStore implements it.
type SleepSchedules interface {
	PutManagedRealtimeSchedule(ctx context.Context, row state.ManagedRealtimeSchedule) (state.ManagedRealtimeSchedule, error)
	ListManagedRealtimeSchedules(ctx context.Context, endpointID, channel string) ([]state.ManagedRealtimeSchedule, error)
	UpdateManagedRealtimeSchedule(ctx context.Context, endpointID, channel, id string, version int64, deadline *time.Time) (state.ManagedRealtimeSchedule, error)
}

const (
	// ToolsetPath is the base URL path of the toolset; RAP runtimes append
	// /.well-known/rap-toolset to discover it.
	ToolsetPath = "/v1/agent-tools"
	toolSleep   = "gregale_sleep"
	// sleepSchedulePrefix names the inbox schedules that are sleeps.
	sleepSchedulePrefix = "agent-sleep-"
	// A wake-up that fails to publish is retried, so a database blip does
	// not lose it.
	sleepMaxAttempts = 5
	// toolInvocationMaxBytes bounds an invocation: arguments plus a
	// callback URL and RAP's envelope.
	toolInvocationMaxBytes = 8 << 10
)

// errCallbackGone marks a callback URL that reaches nothing any more; the
// routes answer it like an unknown route.
var errCallbackGone = api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "")

// rapInvocation is the part of a RAP tool invocation the toolset reads (RAP
// spec, "Tool Invocation").
type rapInvocation struct {
	Operation   string          `json:"operation"`
	Arguments   json.RawMessage `json:"arguments"`
	ID          string          `json:"id"`
	CallID      *string         `json:"call_id"`
	CallbackURL string          `json:"callback_url"`
	GroupID     string          `json:"group_id"`
}

// rapToolResultMessage is a RAP tool result (RAP spec, "Tool Result").
type rapToolResultMessage struct {
	Type    string  `json:"type"`
	GroupID string  `json:"group_id"`
	ID      string  `json:"id"`
	CallID  *string `json:"call_id,omitempty"`
	Text    string  `json:"text"`
}

// rapToolset is RAP's toolset manifest (RAP spec, "Toolsets").
type rapToolset struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Endpoint    string    `json:"endpoint"`
	Tools       []rapTool `json:"tools"`
}

type rapTool struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	InputSchema   map[string]any `json:"inputSchema"`
	DisplayScript string         `json:"displayScript,omitempty"`
}

// Toolset handles GET /v1/agent-tools/.well-known/rap-toolset. The manifest
// is static and names no account, like an API description.
func (h *Handler) Toolset(w http.ResponseWriter, _ *http.Request) {
	if !h.toolsConfigured() {
		api.WriteProblem(w, api.ErrCapacity("agent tools are unavailable on this control-plane host"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, rapToolset{
		Name:        "gregale",
		Description: "Gregale platform tools for stateless agents. Each result arrives at the call's callback URL.",
		Endpoint:    h.CallbackBaseURL + ToolsetPath + "/invoke",
		Tools: []rapTool{{
			Name: toolSleep,
			Description: "Wake this thread later, after a number of seconds or at a time, at most 30 days ahead. " +
				"The wake-up arrives as this tool's result. Nothing runs while the thread sleeps.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"seconds": map[string]any{"type": "number", "minimum": 1, "maximum": api.AgentSleepMaxSeconds, "description": "Seconds to sleep"},
					"until":   map[string]any{"type": "string", "format": "date-time", "description": "RFC 3339 time to wake at, instead of seconds"},
					"reason":  map[string]any{"type": "string", "description": "Why the thread sleeps"},
				},
				"additionalProperties": false,
			},
			DisplayScript: `if args.until != () { "Sleeping until " + args.until } else { "Sleeping " + args.seconds + "s" }`,
		}},
	})
}

// InvokeTool handles POST /v1/agent-tools/invoke. As RAP asks, it
// acknowledges an invocation with 200 and delivers every outcome, errors
// included, as the call's tool result. An invocation whose callback URL apid
// did not issue for its thread, or that no longer works, cannot be answered,
// so it is refused like an unknown route.
func (h *Handler) InvokeTool(w http.ResponseWriter, r *http.Request) {
	if !h.toolsConfigured() {
		api.WriteProblem(w, api.ErrCapacity("agent tools are unavailable on this control-plane host"))
		return
	}
	var raw json.RawMessage
	var inv rapInvocation
	if httpkit.DecodeJSONSized(r, &raw, toolInvocationMaxBytes) != nil || json.Unmarshal(raw, &inv) != nil {
		api.WriteProblem(w, api.ErrValidation("the body must be a JSON RAP tool invocation"))
		return
	}
	token, ok := strings.CutPrefix(inv.CallbackURL, h.CallbackBaseURL+"/v1/agent-callbacks/")
	if !ok || token == "" || strings.ContainsAny(token, "/?#") {
		http.NotFound(w, r)
		return
	}
	grant, ok := h.grantFor(w, r, token)
	if !ok {
		return
	}
	problem := checkInvocation(grant, inv)
	if problem == nil {
		problem = h.admit(r.Context(), grant)
	}
	if problem == nil {
		problem = h.runTool(r.Context(), grant, inv)
	}
	if problem != nil {
		writeCallbackProblem(w, r, problem)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// CloseThread handles POST /v1/agent-tools/close_thread, RAP's best-effort
// thread closure. Its body names only a thread and no credential backs it,
// so it changes nothing: a thread closes by revoking its callback URLs,
// which also cancels its sleeps.
func (h *Handler) CloseThread(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) toolsConfigured() bool {
	return h.callbacksConfigured() && h.Sleeps != nil
}

// checkInvocation checks an invocation against its callback URL: the URL
// must be for the invocation's thread and carry tool results.
func checkInvocation(grant CallbackToken, inv rapInvocation) *api.Problem {
	switch {
	case inv.GroupID != grant.ThreadID:
		return api.NewProblem(http.StatusForbidden, api.CodeValidation, "Thread mismatch", "group_id must be the thread of the callback URL")
	case !grant.Allows(api.AgentCallbackKindToolResult):
		return api.NewProblem(http.StatusForbidden, api.CodeValidation, "Kind not allowed", "the callback URL does not carry tool results")
	case inv.ID == "":
		return api.ErrValidation("a RAP tool invocation needs an id")
	}
	return nil
}

func (h *Handler) runTool(ctx context.Context, grant CallbackToken, inv rapInvocation) *api.Problem {
	if inv.Operation != toolSleep {
		return h.answerTool(ctx, grant, inv, fmt.Sprintf("unknown operation %q; the gregale toolset has %s", inv.Operation, toolSleep))
	}
	return h.sleep(ctx, grant, inv)
}

// answerTool publishes the call's tool result now.
func (h *Handler) answerTool(ctx context.Context, grant CallbackToken, inv rapInvocation, text string) *api.Problem {
	post, problem := toolResultPost(grant, inv, text)
	if problem == nil {
		_, problem = h.publish(ctx, grant, post)
	}
	return problem
}

// toolResultPost is the inbox message of a tool result, with the message ID
// a RAP tool posting the same result to the callback URL would get.
func toolResultPost(grant CallbackToken, inv rapInvocation, text string) (api.AgentCallbackPost, *api.Problem) {
	data, err := json.Marshal(rapToolResultMessage{Type: rapToolResult, GroupID: grant.ThreadID, ID: inv.ID, CallID: inv.CallID, Text: text})
	if err != nil {
		return api.AgentCallbackPost{}, api.ErrValidation("the tool result is not encodable")
	}
	post := api.AgentCallbackPost{MessageID: rapMessageID("rap-tool-result-", inv.ID), Kind: api.AgentCallbackKindToolResult, Data: data}
	return post, checkCallbackPost(grant, post)
}

// sleep schedules the call's tool result on the inbox for the wake-up time.
// A retried invocation finds its schedule already there.
func (h *Handler) sleep(ctx context.Context, grant CallbackToken, inv rapInvocation) *api.Problem {
	now := h.now()
	wake, text, err := sleepWake(inv.Arguments, now)
	if err != nil {
		return h.answerTool(ctx, grant, inv, toolSleep+": "+err.Error())
	}
	if !wake.After(now) {
		return h.answerTool(ctx, grant, inv, text)
	}
	post, problem := toolResultPost(grant, inv, text)
	if problem != nil {
		return problem
	}
	row, err := h.Sleeps.PutManagedRealtimeSchedule(ctx, state.ManagedRealtimeSchedule{EndpointID: grant.EndpointID, Channel: api.AgentInboxChannel,
		ID: sleepScheduleID(grant.ThreadID, inv.ID), Data: post.Data, Metadata: callbackMetadata(grant.ThreadID, post), DeliverAt: wake,
		MaxAttempts: sleepMaxAttempts})
	switch {
	case errors.Is(err, state.ErrConflict):
		return nil
	case errors.Is(err, state.ErrManagedRealtimeScheduleLimit):
		return h.answerTool(ctx, grant, inv, fmt.Sprintf("%s: the endpoint already holds %d realtime schedules, the most it can; try again later",
			toolSleep, state.ManagedRealtimeScheduleLimit))
	case errors.Is(err, state.ErrNotFound):
		return errRealtimeEndpointNotFound()
	case err != nil:
		return h.problem(state.Account{ID: grant.AccountID}, "schedule sleep", err)
	}
	return h.recheckRevocation(ctx, grant, row)
}

// recheckRevocation closes the race with a revocation that listed the
// thread's sleeps before this one existed: a thread revoked since the URL
// was issued cancels the new sleep.
func (h *Handler) recheckRevocation(ctx context.Context, grant CallbackToken, row state.ManagedRealtimeSchedule) *api.Problem {
	revokedAt, revoked, err := h.Revocations.CallbacksRevokedAt(ctx, grant.EndpointID, grant.ThreadID)
	if err != nil {
		return h.problem(state.Account{ID: grant.AccountID}, "check callback revocation", err)
	}
	if !revoked || grant.IssuedAt.After(revokedAt) {
		return nil
	}
	if _, err := h.Sleeps.UpdateManagedRealtimeSchedule(ctx, grant.EndpointID, api.AgentInboxChannel, row.ID, row.Version, nil); err != nil &&
		!errors.Is(err, state.ErrConflict) {
		return h.problem(state.Account{ID: grant.AccountID}, "cancel sleep", err)
	}
	return errCallbackGone
}

// sleepArguments are gregale_sleep's arguments: seconds, as Infinity's own
// sleep tool takes them, or an RFC 3339 time.
type sleepArguments struct {
	Seconds *float64 `json:"seconds"`
	Until   *string  `json:"until"`
	Reason  string   `json:"reason"`
}

// sleepWake returns when the thread wakes and the result it wakes with. An
// until that has passed wakes at once.
func sleepWake(raw json.RawMessage, now time.Time) (time.Time, string, error) {
	var args sleepArguments
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || string(trimmed) == "null" || dec.Decode(&args) != nil {
		return time.Time{}, "", errors.New("arguments must be an object with seconds or until, and an optional reason")
	}
	latest := now.Add(api.AgentSleepMaxSeconds * time.Second)
	switch {
	case (args.Seconds == nil) == (args.Until == nil):
		return time.Time{}, "", errors.New("give exactly one of seconds and until")
	case args.Seconds != nil:
		s := *args.Seconds
		if math.IsNaN(s) || s < 1 || s > api.AgentSleepMaxSeconds {
			return time.Time{}, "", fmt.Errorf("seconds must be 1-%d (AgentSleepMaxSeconds); got %s", api.AgentSleepMaxSeconds, strconv.FormatFloat(s, 'f', -1, 64))
		}
		return now.Add(time.Duration(math.Ceil(s)) * time.Second), "Slept for " + strconv.FormatFloat(s, 'f', -1, 64) + " seconds.", nil
	}
	until, err := time.Parse(time.RFC3339, *args.Until)
	switch {
	case err != nil:
		return time.Time{}, "", errors.New("until must be an RFC 3339 time, such as 2026-11-02T09:00:00Z")
	case until.After(latest):
		return time.Time{}, "", fmt.Errorf("until must be at most %d seconds ahead (AgentSleepMaxSeconds)", api.AgentSleepMaxSeconds)
	case !until.After(now):
		return now, until.UTC().Format(time.RFC3339) + " has passed; woke at once.", nil
	}
	return until, "Slept until " + until.UTC().Format(time.RFC3339) + ".", nil
}

// sleepScheduleID is one call's schedule: a retried invocation of the call
// finds it, and no other thread's call can name it.
func sleepScheduleID(threadID, callID string) string {
	sum := sha256.Sum256([]byte(threadID + "\x00" + callID))
	return sleepSchedulePrefix + hex.EncodeToString(sum[:16])
}

// cancelSleeps cancels the thread's pending sleeps, so a revoked thread does
// not wake. A wake-up the schedule worker publishes at this moment may still
// land, as a post racing the revocation would. A sleep whose version moves
// under the cancel (a failed publish rescheduled it) is read again.
func (h *Handler) cancelSleeps(ctx context.Context, endpointID, threadID string) error {
	if h.Sleeps == nil {
		return nil
	}
	for range 3 {
		rows, err := h.Sleeps.ListManagedRealtimeSchedules(ctx, endpointID, api.AgentInboxChannel)
		if err != nil {
			return fmt.Errorf("list sleeps: %w", err)
		}
		moved := false
		for _, row := range rows {
			if !strings.HasPrefix(row.ID, sleepSchedulePrefix) || row.Metadata["thread_id"] != threadID || row.Status != "pending" {
				continue
			}
			_, err := h.Sleeps.UpdateManagedRealtimeSchedule(ctx, endpointID, api.AgentInboxChannel, row.ID, row.Version, nil)
			switch {
			case errors.Is(err, state.ErrConflict):
				moved = true
			case err != nil:
				return fmt.Errorf("cancel sleep %s: %w", row.ID, err)
			}
		}
		if !moved {
			return nil
		}
	}
	return nil
}
