package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"net/url"
	"time"
)

type WorkflowOutboundExecutor interface {
	ExecuteOutboundStep(context.Context, string, string, int, api.WorkflowOutboundSpec, []byte, time.Duration) (int, []byte, time.Time, error)
}
type WorkflowOutboundMinter func(outbound.WorkflowIdentity, outbound.WorkflowOutboundRequest, []byte) (string, error)

func (o *WorkflowOrchestrator) WithOutboundExecutor(executor WorkflowOutboundExecutor) *WorkflowOrchestrator {
	o.outbound = executor
	return o
}

type workflowOutboundExecutor struct {
	store  state.WorkflowOutboundStore
	mint   WorkflowOutboundMinter
	client *http.Client
}

// NewWorkflowOutboundExecutor shares the private, proxy-free loopback transport
// used by Runs. Workflow assertions have their own identity and authorization.
func NewWorkflowOutboundExecutor(store state.WorkflowOutboundStore, mint WorkflowOutboundMinter) WorkflowOutboundExecutor {
	relay := NewLoopbackExecutionOutboundRelay().(*loopbackExecutionOutboundRelay)
	// Keep retries in the durable workflow ledger, including transport retries
	// that net/http otherwise permits for Idempotency-Key requests.
	transport := relay.client.Transport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	relay.client.Transport = transport
	return &workflowOutboundExecutor{store: store, mint: mint, client: relay.client}
}

func (e *workflowOutboundExecutor) ExecuteOutboundStep(ctx context.Context, runID, stepName string, attempt int, spec api.WorkflowOutboundSpec, input []byte, timeout time.Duration) (int, []byte, time.Time, error) {
	var zero time.Time
	if e == nil || e.store == nil || e.mint == nil || e.client == nil {
		return 0, nil, zero, errors.New("workflow outbound executor unavailable")
	}
	if spec.Method == "GET" || spec.Method == "HEAD" {
		input = nil
	}
	if int64(len(input)) > api.WorkflowOutboundBodyMaxBytes {
		return 0, nil, zero, errors.New("workflow outbound input exceeds the limit")
	}
	lease, err := e.store.GetWorkflowOutboundAttempt(ctx, runID, stepName, attempt)
	if err != nil {
		return 0, nil, zero, state.ErrWorkflowOutboundAttemptExpired
	}
	identity := outbound.WorkflowIdentity{AccountID: lease.AccountID, AppID: lease.AppID, PlatformTenantID: lease.PlatformTenantID, RunID: runID, StepName: stepName, Attempt: attempt, AttemptToken: lease.Token}
	pathTemplate := spec.PathTemplate
	if pathTemplate == "" {
		pathTemplate = spec.Path
	}
	queryTemplate := spec.QueryTemplate
	if queryTemplate == nil {
		queryTemplate = spec.Query
	}
	rawQuery := spec.RawQuery
	if rawQuery == "" && len(spec.Query) > 0 {
		values := make(url.Values, len(spec.Query))
		for key, value := range spec.Query {
			values.Set(key, value)
		}
		rawQuery = values.Encode()
	}
	authorization := outbound.WorkflowOutboundRequest{IntegrationID: spec.IntegrationID, Method: spec.Method, Path: spec.Path, RawQuery: rawQuery, PathTemplate: pathTemplate, QueryTemplate: queryTemplate}
	assertion, err := e.mint(identity, authorization, input)
	if err != nil {
		return 0, nil, zero, errors.New("workflow outbound assertion unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-callCtx.Done():
				return
			case <-ticker.C:
				current, err := e.store.GetWorkflowOutboundAttempt(callCtx, runID, stepName, attempt)
				if err != nil || current.Token != lease.Token {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	requestURL := "http://" + executionOutboundGatewayAddress + outbound.Prefix + spec.IntegrationID + spec.Path
	if rawQuery != "" {
		requestURL += "?" + rawQuery
	}
	request, err := http.NewRequestWithContext(callCtx, spec.Method, requestURL, bytes.NewReader(input))
	if err != nil {
		return 0, nil, zero, errors.New("workflow outbound request invalid")
	}
	request.Header.Set(outbound.WorkflowIdentityHeader, assertion)
	request.Header.Set("Idempotency-Key", workflowStepIdempotencyKey(runID, stepName))
	if len(input) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := e.client.Do(request)
	if err != nil {
		return 0, nil, zero, errors.New("workflow outbound request failed")
	}
	defer func() { _ = response.Body.Close() }()
	retryAt := workflowOutboundRetryAfter(response.Header.Get("Retry-After"), time.Now().UTC())
	body, err := io.ReadAll(io.LimitReader(response.Body, api.WorkflowOutboundBodyMaxBytes+1))
	if err != nil || int64(len(body)) > api.WorkflowOutboundBodyMaxBytes {
		return response.StatusCode, nil, retryAt, errors.New("workflow outbound response incomplete or exceeds the limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, nil, retryAt, nil
	}
	var value any
	if len(body) > 0 {
		if !json.Valid(body) {
			value = string(body)
		} else {
			value = json.RawMessage(body)
		}
	}
	output, err := json.Marshal(struct {
		Status int `json:"status"`
		Body   any `json:"body"`
	}{response.StatusCode, value})
	return response.StatusCode, output, retryAt, err
}

func workflowOutboundRetryAfter(raw string, now time.Time) time.Time {
	return api.WorkflowRetryAfter(raw, now)
}

func workflowHasOutbound(steps []api.WorkflowStepSpec) bool {
	for _, step := range steps {
		if step.Outbound != nil || (step.ForEach != nil && step.ForEach.Action.Outbound != nil) {
			return true
		}
	}
	return false
}
