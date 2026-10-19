package mcpprotocol

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Coverage struct {
	State                                string
	Expected, Reporting, Starting, Stale int
	Deployments                          int
}

// FetchCoverage uses the current host inventory, never protocol request volume.
// Empty inventory is unavailable: scale-to-zero requires scheduler evidence.
func FetchCoverage(ctx context.Context, reader Reader, appID string, now time.Time, inventory ...[]string) (Coverage, error) {
	result := Coverage{State: "Unavailable"}
	if len(inventory) > 0 {
		result.Expected = len(inventory[0])
	}
	if _, err := uuid.Parse(appID); err != nil || reader == nil {
		return result, fmt.Errorf("MCP coverage unavailable")
	}
	// One joined instant query ensures both measurements share an evaluation time.
	query := fmt.Sprintf(`(max by (app_id,deployment_id,instance_id) (vmmd_mcp_telemetry_heartbeat_timestamp_seconds{app_id=%q}) >= bool %d) + 2 * (max by (app_id,deployment_id,instance_id) (vmmd_mcp_telemetry_instance_started_timestamp_seconds{app_id=%q}) > bool %d)`, appID, now.Add(-HeartbeatStaleAfter).Unix(), appID, now.Add(-HeartbeatStartupGrace).Unix())
	rows, err := reader.QueryVector(ctx, query)
	if err != nil {
		return result, err
	}
	if len(rows) == 0 && (len(inventory) == 0 || len(inventory[0]) != 0) {
		return result, nil
	}
	if len(rows) > MaxCoverageInstances {
		return result, fmt.Errorf("MCP coverage exceeds bounds")
	}
	expected := map[string]bool{}
	if len(inventory) > 0 {
		for _, id := range inventory[0] {
			expected[id] = true
		}
		if len(expected) == 0 {
			return Coverage{State: "Idle (no executing instances)"}, nil
		}
	}
	result.Expected = 0
	seen := map[string]bool{}
	deployments := map[string]bool{}
	for _, r := range rows {
		if !finite(r.Value) || r.Value < 0 || r.Value > 3 || r.Value != float64(int(r.Value)) {
			return Coverage{State: "Unavailable"}, fmt.Errorf("invalid MCP coverage")
		}
		if len(inventory) > 0 {
			id := r.Labels["instance_id"]
			if seen[id] {
				return Coverage{State: "Unavailable"}, fmt.Errorf("ambiguous MCP coverage lifecycle")
			}
			if !expected[id] {
				continue
			}
			seen[id] = true
		}
		result.Expected++
		if id := r.Labels["deployment_id"]; id != "" {
			deployments[id] = true
		}
		switch int(r.Value) {
		case 1, 3:
			result.Reporting++
		case 2:
			result.Starting++
		default:
			result.Stale++
		}
	}
	if len(inventory) > 0 && len(seen) != len(expected) {
		result.Expected = len(expected)
		result.State = "Unavailable (instance coverage incomplete)"
		return result, nil
	}
	result.Deployments = len(deployments)
	switch {
	case result.Reporting == result.Expected:
		result.State = "Reporting"
	case result.Reporting > 0:
		result.State = "Partially reporting"
	case result.Starting == result.Expected:
		result.State = "Starting"
	default:
		result.State = "Stale"
	}
	return result, nil
}
