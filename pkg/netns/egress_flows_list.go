//go:build !metal

package netns

import "context"

// ListEgressFlowsInNetns is the non-metal stub: no namespaces, no flows.
func ListEgressFlowsInNetns(context.Context, string) ([]EgressFlow, error) {
	return nil, nil
}
