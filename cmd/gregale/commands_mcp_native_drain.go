package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func mcpNativeDrainBudget(reader io.Reader, instances []api.InstanceResponse, deployment string) (time.Duration, error) {
	wanted := map[string]bool{}
	for _, instance := range instances {
		if instance.DeploymentID == deployment && instance.State == "running" {
			wanted[instance.ID] = true
		}
	}
	budgets := map[string]int{}
	scanner := bufio.NewScanner(io.LimitReader(reader, 4<<20))
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		var envelope struct{ Instance, Line string }
		if !strings.HasPrefix(scanner.Text(), "data: ") || json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &envelope) != nil || !wanted[envelope.Instance] {
			continue
		}
		var event struct {
			Event             string          `json:"event"`
			WorkerID          string          `json:"workerID"`
			ShutdownTimeoutMS json.RawMessage `json:"shutdownTimeoutMs"`
		}
		if json.Unmarshal([]byte(envelope.Line), &event) != nil || event.Event != "mcp_task_worker_started" || !mcpWorkerUUID.MatchString(event.WorkerID) {
			continue
		}
		var deadline int
		if json.Unmarshal(event.ShutdownTimeoutMS, &deadline) != nil || deadline < 1000 || deadline > 300000 || (budgets[envelope.Instance] != 0 && budgets[envelope.Instance] != deadline) {
			return 0, errors.New("worker startup drain budget is missing, invalid or ambiguous; update the worker receipt before release")
		}
		budgets[envelope.Instance] = deadline
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if len(wanted) == 0 || len(budgets) != len(wanted) {
		return 0, errMCPNativeDrainBudgetsMissing
	}
	deadline := 0
	for _, budget := range budgets {
		deadline = max(deadline, budget)
	}
	return time.Duration(deadline) * time.Millisecond, nil
}

// Read the running generation's own budget; candidate configuration cannot
// establish how long an older worker needs to drain.
func mcpNativeDrainableWorkerIDs(ctx context.Context, c *Client, app api.AppResponse, slug, deployment string, allowStopped bool) ([]string, error) {
	instances, err := c.ListInstances(ctx, slug)
	if err != nil {
		return nil, err
	}
	if allowStopped && len(instances) == 0 {
		return nil, nil
	}
	for _, instance := range instances {
		if instance.DeploymentID != deployment {
			return nil, errors.New("worker app contains another generation; refusing app-wide drain")
		}
	}
	ids, deadline, err := mcpNativeWorkerReceipts(ctx, c, slug, deployment, instances, false, true)
	if err != nil {
		return nil, err
	}
	if app.Manifest.StopGracePeriod <= deadline {
		return nil, errors.New("worker stop grace must exceed its reported Task shutdown deadline")
	}
	return ids, nil
}
