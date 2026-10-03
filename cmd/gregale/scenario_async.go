package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type testAsyncRouteClient interface {
	CreateEdgeRule(context.Context, string, api.CreateEdgeRuleRequest) (api.EdgeRuleResponse, error)
}

func (policy testRetryPolicy) dto() *api.RetryPolicyDTO {
	return &api.RetryPolicyDTO{
		MaxAttempts: policy.MaxAttempts, BaseSeconds: policy.BaseSeconds,
		MaxSeconds: policy.MaxSeconds, JitterSeconds: policy.JitterSeconds,
	}
}

func createTestAsyncRoutes(ctx context.Context, client testAsyncRouteClient, slug, appURL string, routes []testAsyncRoute) ([]string, error) {
	parsed, err := url.Parse(appURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid service URL %q", appURL)
	}
	created := make([]string, 0, len(routes))
	for _, route := range routes {
		action := api.EdgeRuleAsyncAction{}
		if route.RetryPolicy != nil {
			action.RetryPolicy = route.RetryPolicy.dto()
		}
		actionJSON, err := json.Marshal(action)
		if err != nil {
			return created, err
		}
		response, err := client.CreateEdgeRule(ctx, slug, api.CreateEdgeRuleRequest{
			MatchHost: parsed.Hostname(), MatchPath: route.Path, MatchMethods: route.Methods,
			Kind: "async", Action: actionJSON,
		})
		if err != nil {
			return created, fmt.Errorf("%s %v: %w", route.Path, route.Methods, err)
		}
		created = append(created, response.ID)
	}
	return created, nil
}

func readTestTriggerOutput(path string) (map[string]string, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 64*1024 {
		return nil, errors.New("trigger output exceeds 64 KiB")
	}
	var values map[string]string
	if err := json.Unmarshal(body, &values); err != nil {
		return nil, fmt.Errorf("expected a JSON object of output names and string values: %w", err)
	}
	if len(values) == 0 {
		return nil, errors.New("trigger wrote no output values")
	}
	return values, nil
}

type testInvocationClient interface {
	GetInvocation(context.Context, string) (api.Invocation, error)
}

func waitForTestInvocation(ctx context.Context, client testInvocationClient, condition testInvocationOutput, appID, invocationID string) (testInvocationEvidence, error) {
	evidence := testInvocationEvidence{Service: condition.Service, ID: invocationID}
	if _, err := uuid.Parse(invocationID); err != nil {
		return evidence, fmt.Errorf("trigger supplied invalid invocation id %q", invocationID)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		invocation, err := client.GetInvocation(ctx, invocationID)
		if err != nil {
			return evidence, fmt.Errorf("read invocation %s: %w", invocationID, err)
		}
		if invocation.AppID != appID {
			return evidence, fmt.Errorf("invocation %s belongs to another workload", invocationID)
		}
		evidence.State = invocation.State
		evidence.Attempts = invocation.Attempts
		switch invocation.State {
		case "completed":
			if invocation.Attempts < condition.MinAttempts {
				return evidence, fmt.Errorf("invocation %s completed after %d attempts; expected at least %d", invocationID, invocation.Attempts, condition.MinAttempts)
			}
			return evidence, nil
		case "failed", "dead_letter", "cancelled":
			return evidence, fmt.Errorf("invocation %s reached %s after %d attempts", invocationID, invocation.State, invocation.Attempts)
		}
		select {
		case <-ctx.Done():
			return evidence, fmt.Errorf("invocation %s remained %s: %w", invocationID, invocation.State, ctx.Err())
		case <-ticker.C:
		}
	}
}
