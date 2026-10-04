package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// enqueueVersionedInvocation captures the selected release when work is
// accepted. The drain validates it again at delivery, including after retries.
func (s *server) enqueueVersionedInvocation(ctx context.Context, requestHeaders http.Header, inv state.Invocation, capacityDetail string, work ...*api.InvokeWork) (state.Invocation, *api.Problem) {
	var err error
	inv.Headers, err = mergeInvocationVersionHeaders(inv.Headers, requestHeaders)
	if err != nil {
		return state.Invocation{}, api.ErrValidation("revision and release headers must be unique UUIDs")
	}
	// Replay retains the original invocation's scope and environment lifetime.
	// A first-class queue has already selected its immutable binding and scope.
	// Other shared producers remain pinned to their default environment.
	if inv.Source == state.InvocationReplay || inv.Source == state.InvocationQueue && inv.QueueBindingID != "" {
		inv, _, err = state.ResolveInvocationVersion(ctx, s.store, inv)
	} else {
		inv, _, err = state.ResolveInvocationVersionForEnvironment(ctx, s.store, inv, "")
	}
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
	var created state.Invocation
	if len(work) > 0 && work[0] != nil {
		policyStore, ok := s.store.(state.AppWorkPolicyStore)
		if !ok {
			return state.Invocation{}, api.ErrCapacity("work policy store unavailable")
		}
		record, lookupErr := policyStore.AppWorkPolicyByName(ctx, inv.AppID, work[0].Policy)
		if errors.Is(lookupErr, state.ErrNotFound) {
			return state.Invocation{}, api.ErrValidation("unknown work policy")
		}
		if lookupErr != nil {
			return state.Invocation{}, api.ErrCapacity("lookup work policy")
		}
		key, keyErr := workpolicy.CanonicalScalar(work[0].Key)
		if keyErr != nil {
			return state.Invocation{}, api.ErrValidation("work key must be a bounded string, number, or boolean")
		}
		inv.WorkPolicyRevision = record.Revision
		var fairnessKeys []string
		if len(work[0].FairnessKey) > 0 {
			fairnessKey, fairnessErr := workpolicy.CanonicalScalar(work[0].FairnessKey)
			if fairnessErr != nil {
				return state.Invocation{}, api.ErrValidation("work fairness_key must be a bounded string, number, or boolean")
			}
			fairnessKeys = append(fairnessKeys, fairnessKey)
		}
		created, err = s.store.EnqueueKeyedInvocation(ctx, inv, record.Policy, key, fairnessKeys...)
	} else {
		created, err = s.store.EnqueueInvocation(ctx, inv)
	}
	if errors.Is(err, state.ErrQueueBindingRetired) {
		return state.Invocation{}, api.NewProblem(http.StatusConflict, "queue_binding_retired", "Queue binding retired", "this queue is held for explicit recovery")
	}
	if errors.Is(err, state.ErrQueueBindingEnvironmentUnavailable) || s.queueAdmissionEnvironmentUnavailable(ctx, inv, err) {
		return state.Invocation{}, api.NewProblem(http.StatusConflict, "queue_binding_environment_unavailable", "Queue environment unavailable", "the captured queue environment is unavailable; review the queue selection before retrying")
	}
	if err != nil {
		if errors.Is(err, state.ErrInvalidArgument) && inv.PlatformTenantID != "" {
			return state.Invocation{}, api.ErrValidation("flag_context customer must be active in the app account")
		}
		if errors.Is(err, state.ErrPlatformTenantSuspended) {
			return state.Invocation{}, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Platform tenant suspended", "resume this customer before enqueueing work")
		}
		return state.Invocation{}, api.ErrCapacity(capacityDetail)
	}
	return created, nil
}

// Both stores can reject stale explicit binding IDs as invalid admission.
// Inspect the captured identity before classifying a tenant validation error;
// inherited flag context must not conceal a deleted/recreated environment.
func (s *server) queueAdmissionEnvironmentUnavailable(ctx context.Context, inv state.Invocation, err error) bool {
	if !errors.Is(err, state.ErrInvalidArgument) || inv.QueueBindingID == "" {
		return false
	}
	history, ok := s.store.(state.QueueBindingHistoryStore)
	if !ok {
		return false
	}
	binding, lookupErr := history.QueueBindingHistoryByID(ctx, inv.AccountID, inv.AppID, inv.QueueBindingID)
	if lookupErr != nil || binding.DeploymentScope == "" {
		return false
	}
	app, lookupErr := s.store.AppByID(ctx, inv.AppID)
	if lookupErr != nil {
		return false
	}
	available, problem := s.queueBindingEnvironmentAvailable(ctx, state.Account{ID: inv.AccountID}, app, binding)
	return problem == nil && !available
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
