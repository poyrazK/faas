package agents

// Stored callback results (ADR-20261030125501 §3 amendment 2026-11-22). A
// tool result is one inbox message, and a realtime message carries at most
// AgentCallbackPayloadMaxBytes. A larger result, up to
// AgentCallbackResultMaxBytes, is kept in the artifact store for
// AgentCallbackResultRetentionSec, and the inbox message carries a preview,
// the result's size and digest, and result_url, which the endpoint's app
// reads with its own credential (GET /v1/agent-callbacks/results/{id}).
// Both callback posts and Gregale's own tool results (gregale_run) take this
// path.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

// CallbackResultStore records stored results; *agentstore.Store implements
// it. The bodies live in Handler.Files.
type CallbackResultStore interface {
	ReserveCallbackResult(ctx context.Context, r agentstore.CallbackResult, now time.Time, maxBytes int64) (agentstore.CallbackResult, bool, error)
	CallbackResultByID(ctx context.Context, accountID, id string) (agentstore.CallbackResult, error)
	DeleteCallbackResult(ctx context.Context, id string) error
	ExpiredCallbackResults(ctx context.Context, before time.Time, limit int) ([]agentstore.CallbackResult, error)
}

var _ CallbackResultStore = (*agentstore.Store)(nil)

// callbackResultSweepRows bounds one sweep of expired results, and
// callbackResultSweepInterval spaces sweeps.
const (
	callbackResultSweepRows     = 200
	callbackResultSweepInterval = 5 * time.Minute
)

// callbackResultsConfigured reports whether this apid can store large
// results; without it a post over AgentCallbackPayloadMaxBytes is refused
// as before.
func (h *Handler) callbackResultsConfigured() bool {
	return h.CallbackResults != nil && h.Files != nil
}

// callbackDataMax is the largest callback data this apid accepts.
func (h *Handler) callbackDataMax() int {
	if h.callbackResultsConfigured() {
		return api.AgentCallbackResultMaxBytes
	}
	return api.AgentCallbackPayloadMaxBytes
}

// callbackResultKey is a stored result's artifact store key.
func callbackResultKey(r agentstore.CallbackResult) string {
	return "agents/callback-results/" + r.AccountID + "/" + r.EndpointID + "/" + r.ID
}

// storeLargePost returns post unchanged when it fits one inbox message.
// Otherwise it stores the data and returns the post with a stub in its
// place. A retried post of the same message finds its result; the same
// message with other data is a conflict, as a publish would be.
func (h *Handler) storeLargePost(ctx context.Context, grant CallbackToken, post api.AgentCallbackPost) (api.AgentCallbackPost, *api.Problem) {
	if len(post.Data) <= api.AgentCallbackPayloadMaxBytes {
		return post, nil
	}
	if !h.callbackResultsConfigured() || len(post.Data) > api.AgentCallbackResultMaxBytes {
		return post, callbackTooLarge(len(post.Data), h.callbackDataMax())
	}
	sum := sha256.Sum256(post.Data)
	digest := hex.EncodeToString(sum[:])
	now := h.now()
	result, existing, err := h.CallbackResults.ReserveCallbackResult(ctx, agentstore.CallbackResult{AccountID: grant.AccountID,
		EndpointID: grant.EndpointID, ThreadID: grant.ThreadID, MessageID: post.MessageID, SHA256: digest,
		SizeBytes: len(post.Data), ExpiresAt: now.Add(api.AgentCallbackResultRetentionSec * time.Second)},
		now, api.AgentCallbackResultsPerEndpointMaxBytes)
	var limit *agentstore.LimitError
	switch {
	case errors.As(err, &limit):
		return post, api.ErrAgentCallbackResultsFull(int64(limit.Max), int64(limit.Observed))
	case errors.Is(err, agentstore.ErrNotFound):
		return post, errRealtimeEndpointNotFound()
	case err != nil:
		return post, h.problem(state.Account{ID: grant.AccountID}, "store callback result", err)
	case existing && result.SHA256 != digest:
		return post, api.NewProblem(http.StatusConflict, api.CodeConflict, "Message already posted",
			"this message_id was already posted to the thread with different data")
	}
	// A retry writes the same bytes again, which also heals a body an
	// earlier attempt failed to store.
	if err := h.Files.Put(ctx, callbackResultKey(result), bytes.NewReader(post.Data)); err != nil {
		if !existing {
			_ = h.CallbackResults.DeleteCallbackResult(context.WithoutCancel(ctx), result.ID)
		}
		return post, h.problem(state.Account{ID: grant.AccountID}, "store callback result body", err)
	}
	stub, err := resultStub(post.Data, callbackResultRef{URL: h.CallbackBaseURL + "/v1/agent-callbacks/results/" + result.ID,
		Bytes: len(post.Data), SHA256: digest})
	if err != nil {
		return post, h.problem(state.Account{ID: grant.AccountID}, "build callback result stub", err)
	}
	post.Data = stub
	return post, nil
}

func callbackTooLarge(size, max int) *api.Problem {
	return api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeValidation, "Callback too large",
		fmt.Sprintf("data is %d bytes; a callback carries at most %d", size, max)).WithLimit(int64(max), int64(size))
}

// callbackResultRef is what the stub says about the stored result.
type callbackResultRef struct {
	URL    string `json:"result_url"`
	Bytes  int    `json:"result_bytes"`
	SHA256 string `json:"result_sha256"`
}

// rapStubFields are the fields of a RAP callback a stub keeps: they route
// the message to its call. The text becomes a preview.
var rapStubFields = []string{"type", "group_id", "id", "call_id", "tool_call_id", "subscription", "is_error"}

// resultStub is the inbox message that stands for a stored result. A RAP
// callback keeps its routing fields, so the agent's runtime still matches
// it to its call, and its text becomes a preview; any other data becomes
// {preview, ...}. Both say truncated and carry the result's URL, size and
// digest. The stub always fits one inbox message.
func resultStub(data []byte, ref callbackResultRef) ([]byte, error) {
	var obj map[string]json.RawMessage
	isObject := json.Unmarshal(data, &obj) == nil && obj != nil
	source := string(data)
	if isObject {
		var text string
		if raw, ok := obj["text"]; ok && json.Unmarshal(raw, &text) == nil {
			source = text
		}
	}
	_, isRAP := obj["type"]
	for n := api.AgentCallbackResultPreviewMaxBytes; ; n /= 2 {
		stub := map[string]any{"truncated": true, "result_url": ref.URL, "result_bytes": ref.Bytes, "result_sha256": ref.SHA256}
		preview := truncateUTF8(source, n)
		if isObject && isRAP {
			for _, field := range rapStubFields {
				if raw, ok := obj[field]; ok {
					stub[field] = raw
				}
			}
			stub["text"] = preview
		} else {
			stub["preview"] = preview
		}
		out, err := json.Marshal(stub)
		if err != nil {
			return nil, err
		}
		if len(out) <= api.AgentCallbackPayloadMaxBytes || n == 0 {
			if len(out) > api.AgentCallbackPayloadMaxBytes {
				return nil, fmt.Errorf("stub is %d bytes without a preview", len(out))
			}
			return out, nil
		}
	}
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// GetCallbackResult handles GET /v1/agent-callbacks/results/{id}: the
// stored result, for the app of the endpoint it was posted to.
func (h *Handler) GetCallbackResult(w http.ResponseWriter, r *http.Request, caller CallbackCaller) {
	if !h.callbackResultsConfigured() || h.Inbox == nil {
		api.WriteProblem(w, api.ErrCapacity("stored tool results are unavailable on this control-plane host"))
		return
	}
	result, err := h.CallbackResults.CallbackResultByID(r.Context(), caller.Account.ID, r.PathValue("id"))
	if errors.Is(err, agentstore.ErrNotFound) || err == nil && !result.ExpiresAt.After(h.now()) {
		api.WriteProblem(w, errCallbackResultNotFound())
		return
	}
	if err != nil {
		api.WriteProblem(w, h.problem(caller.Account, "read callback result", err))
		return
	}
	if _, problem := caller.endpoint(r.Context(), h.Inbox, result.EndpointID); problem != nil {
		api.WriteProblem(w, errCallbackResultNotFound())
		return
	}
	result.AccountID = caller.Account.ID
	body, err := h.Files.Get(r.Context(), callbackResultKey(result))
	if errors.Is(err, storage.ErrNotFound) {
		api.WriteProblem(w, errCallbackResultNotFound())
		return
	}
	if err != nil {
		api.WriteProblem(w, h.problem(caller.Account, "read callback result body", err))
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(result.SizeBytes))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", strconv.Quote("sha256:"+result.SHA256))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(body, int64(result.SizeBytes)))
}

func errCallbackResultNotFound() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Result not found",
		"no stored tool result with this ID is readable by this caller; stored results expire after AgentCallbackResultRetentionSec")
}

// sweepCallbackResults deletes expired results: each body first, then its
// row, so a row never outlives a body it no longer guards. A body already
// gone is fine; a body that cannot be deleted keeps its row for the next
// sweep.
func (h *Handler) sweepCallbackResults(ctx context.Context) {
	if !h.callbackResultsConfigured() {
		return
	}
	expired, err := h.CallbackResults.ExpiredCallbackResults(ctx, h.now(), callbackResultSweepRows)
	if err != nil {
		if ctx.Err() == nil && h.Log != nil {
			h.Log.Warn("agent callback results sweep", "err", err)
		}
		return
	}
	for _, result := range expired {
		if err := h.Files.Delete(ctx, callbackResultKey(result)); err != nil && !errors.Is(err, storage.ErrNotFound) {
			if h.Log != nil {
				h.Log.Warn("agent callback results sweep: delete body", "result_id", result.ID, "err", err)
			}
			continue
		}
		if err := h.CallbackResults.DeleteCallbackResult(ctx, result.ID); err != nil && h.Log != nil {
			h.Log.Warn("agent callback results sweep: delete row", "result_id", result.ID, "err", err)
		}
	}
}
