package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildConsumerControlRequest validates the trial (ADR-1314) and overdue
// policy (ADR-1315) verbs' flags; handled is false for other verbs.
func buildConsumerControlRequest(verb string, f consumerFlags) (any, bool, error) {
	switch verb {
	case "trial-set":
		if (f.trialDays == 0) == (f.endsAt == "") {
			return nil, true, errors.New("set --days or --ends-at")
		}
		req := api.PutAPIConsumerTrialRequest{Days: f.trialDays}
		if f.endsAt != "" {
			at, err := time.Parse(time.RFC3339, f.endsAt)
			if err != nil {
				return nil, true, fmt.Errorf("--ends-at: %w", err)
			}
			req.EndsAt = &at
		}
		return req, true, nil
	case "trial", "trial-end", "billing-policy", "billing-policy-delete", "overdue-holds":
		return nil, true, nil
	case "billing-policy-set":
		if f.blockOverdueAfterDays < 0 {
			return nil, true, errors.New("--block-overdue-after-days is required (0 blocks as soon as an invoice is past due)")
		}
		days := f.blockOverdueAfterDays
		return api.PutAPIConsumerBillingPolicyRequest{BlockOverdueAfterDays: &days}, true, nil
	}
	return nil, false, nil
}

// callConsumerControls calls the trial and overdue policy endpoints;
// handled is false for other verbs.
func callConsumerControls(ctx context.Context, client *Client, verb string, args []string, request any) (any, bool, error) {
	var out any
	var err error
	switch verb {
	case "trial":
		out, err = client.GetAPIConsumerTrial(ctx, args[0], args[1])
	case "trial-set":
		out, err = client.PutAPIConsumerTrial(ctx, args[0], args[1], request.(api.PutAPIConsumerTrialRequest))
	case "trial-end":
		if err = client.EndAPIConsumerTrial(ctx, args[0], args[1]); err == nil {
			out = map[string]string{"status": "trial ended"}
		}
	case "billing-policy":
		out, err = client.GetAPIConsumerBillingPolicy(ctx, args[0])
	case "billing-policy-set":
		out, err = client.PutAPIConsumerBillingPolicy(ctx, args[0], request.(api.PutAPIConsumerBillingPolicyRequest))
	case "billing-policy-delete":
		if err = client.DeleteAPIConsumerBillingPolicy(ctx, args[0]); err == nil {
			out = map[string]string{"status": "billing policy removed"}
		}
	case "overdue-holds":
		out, err = client.ListAPIConsumerOverdueHolds(ctx, args[0])
	default:
		return nil, false, nil
	}
	return out, true, err
}
