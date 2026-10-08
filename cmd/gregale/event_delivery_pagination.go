package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// The endpoint always fetches both streams. Freeze an exhausted stream's
// request cursor and ignore its subsequent rows instead of restarting it.
func collectEventDeliveryPages(ctx context.Context, before, fanoutBefore string, all bool, fetch func(context.Context, string, string) (api.EventDeliveryListResponse, error)) (api.EventDeliveryListResponse, error) {
	out := api.EventDeliveryListResponse{Deliveries: make([]api.EventDeliveryResponse, 0), FanoutFailures: make([]api.EventFanoutFailureResponse, 0)}
	deliveriesDone, fanoutDone := false, false
	seenDeliveries := map[string]bool{before: true}
	seenFanout := map[string]bool{fanoutBefore: true}
	for page := 0; page < maxCLIListPages; page++ {
		if err := ctx.Err(); err != nil {
			return api.EventDeliveryListResponse{}, err
		}
		current, err := fetch(ctx, before, fanoutBefore)
		if err != nil {
			return api.EventDeliveryListResponse{}, err
		}
		out.AppSlug = current.AppSlug
		if !deliveriesDone {
			if current.NextBefore != "" && seenDeliveries[current.NextBefore] {
				return api.EventDeliveryListResponse{}, fmt.Errorf("invocation delivery pagination cursor repeated; traversal stopped")
			}
			out.Deliveries = append(out.Deliveries, current.Deliveries...)
			out.NextBefore = current.NextBefore
			if current.NextBefore == "" {
				deliveriesDone = true
			} else {
				seenDeliveries[current.NextBefore] = true
				before = current.NextBefore
			}
		}
		if !fanoutDone {
			if current.NextFanoutBefore != "" && seenFanout[current.NextFanoutBefore] {
				return api.EventDeliveryListResponse{}, fmt.Errorf("fanout failure pagination cursor repeated; traversal stopped")
			}
			out.FanoutFailures = append(out.FanoutFailures, current.FanoutFailures...)
			out.NextFanoutBefore = current.NextFanoutBefore
			if current.NextFanoutBefore == "" {
				fanoutDone = true
			} else {
				seenFanout[current.NextFanoutBefore] = true
				fanoutBefore = current.NextFanoutBefore
			}
		}
		if !all || deliveriesDone && fanoutDone {
			return out, nil
		}
	}
	return api.EventDeliveryListResponse{}, fmt.Errorf("event delivery pagination exceeded %d pages; fetch smaller ranges with --before and --fanout-before", maxCLIListPages)
}
