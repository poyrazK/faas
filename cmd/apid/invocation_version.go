package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// enqueueVersionedInvocation captures the selected release when work is
// accepted. The drain validates it again at delivery, including after retries.
func (s *server) enqueueVersionedInvocation(ctx context.Context, requestHeaders http.Header, inv state.Invocation, capacityDetail string) (state.Invocation, *api.Problem) {
	var err error
	inv.Headers, err = mergeInvocationVersionHeaders(inv.Headers, requestHeaders)
	if err != nil {
		return state.Invocation{}, api.ErrValidation("revision and release headers must be unique UUIDs")
	}
	inv, _, err = state.ResolveInvocationVersion(ctx, s.store, inv)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrInvalidArgument):
			return state.Invocation{}, api.ErrValidation("invalid invocation revision or release header")
		case errors.Is(err, state.ErrNotFound):
			return state.Invocation{}, api.NewProblem(http.StatusGone, "invocation_version_unavailable", "Invocation version unavailable", "the requested revision or release is unavailable or expired")
		case errors.Is(err, state.ErrConflict):
			return state.Invocation{}, api.NewProblem(http.StatusConflict, "invocation_version_conflict", "Invocation version conflict", "the requested revision and release conflict or the release graph is incomplete")
		default:
			return state.Invocation{}, api.ErrCapacity("resolve invocation version")
		}
	}
	created, err := s.store.EnqueueInvocation(ctx, inv)
	if err != nil {
		return state.Invocation{}, api.ErrCapacity(capacityDetail)
	}
	return created, nil
}

// The HTTP control headers and JSON invocation headers share one namespace.
// Reject ambiguous duplicate values, including mixed-case JSON keys.
func mergeInvocationVersionHeaders(raw json.RawMessage, requestHeaders http.Header) (json.RawMessage, error) {
	if requestHeaders == nil {
		return raw, nil
	}
	values := map[string][]string{}
	for key, entries := range requestHeaders {
		if strings.EqualFold(key, api.RevisionHeader) || strings.EqualFold(key, api.ReleaseHeader) {
			canonical := api.RevisionHeader
			if strings.EqualFold(key, api.ReleaseHeader) {
				canonical = api.ReleaseHeader
			}
			values[canonical] = append(values[canonical], entries...)
		}
	}
	if len(values) == 0 {
		return raw, nil
	}
	headers := map[string]string{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &headers); err != nil {
			return nil, state.ErrInvalidArgument
		}
		if headers == nil {
			headers = map[string]string{}
		}
	}
	for canonical, entries := range values {
		if len(entries) != 1 {
			return nil, state.ErrInvalidArgument
		}
		for key := range headers {
			if strings.EqualFold(key, canonical) {
				return nil, state.ErrInvalidArgument
			}
		}
		headers[canonical] = entries[0]
	}
	return json.Marshal(headers)
}

func setInvocationVersionResponseHeaders(w http.ResponseWriter, inv state.Invocation) {
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		return
	}
	if releaseID := headers[api.ReleaseHeader]; releaseID != "" {
		w.Header().Set(api.ReleaseHeader, releaseID)
	}
	if deploymentID := headers[api.RevisionHeader]; deploymentID != "" {
		w.Header().Set(api.RevisionHeader, deploymentID)
	}
}
