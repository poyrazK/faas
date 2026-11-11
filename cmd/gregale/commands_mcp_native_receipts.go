package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	errMCPNativeWorkerReceiptsMissing = errors.New("current candidate instances lack worker startup IDs")
	errMCPNativeDrainBudgetsMissing   = errors.New("current worker instances lack startup drain budgets")
)

// Retained logs are only a fast path: a healthy instance can outlive its log
// ring. Updated workers refresh receipts every ten seconds, so follow a bounded
// live window if a startup receipt has already been evicted.
func mcpNativeWorkerReceipts(ctx context.Context, c *Client, app, deployment string, instances []api.InstanceResponse, requireFence, requireBudget bool) ([]string, time.Duration, error) {
	wanted := false
	for _, instance := range instances {
		wanted = wanted || (instance.DeploymentID == deployment && instance.State == "running")
	}
	if !wanted {
		return nil, 0, errMCPNativeWorkerReceiptsMissing
	}
	check := func(body []byte) ([]string, time.Duration, error) {
		ids, idErr := mcpNativeWorkerLogs(bytes.NewReader(body), instances, deployment, requireFence)
		var budget time.Duration
		var budgetErr error
		if requireBudget {
			budget, budgetErr = mcpNativeDrainBudget(bytes.NewReader(body), instances, deployment)
		}
		// An incomplete inventory must not hide a conflicting or invalid receipt.
		if idErr != nil && !errors.Is(idErr, errMCPNativeWorkerReceiptsMissing) {
			return nil, 0, idErr
		}
		if budgetErr != nil && !errors.Is(budgetErr, errMCPNativeDrainBudgetsMissing) {
			return nil, 0, budgetErr
		}
		return ids, budget, errors.Join(idErr, budgetErr)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	reader, err := c.StreamAppLogs(ctx, app, deployment, false, api.LogFilter{Grep: "mcp_task_worker_started"})
	if err != nil {
		return nil, 0, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, 4<<20))
	_ = reader.Close()
	if readErr != nil {
		return nil, 0, readErr
	}
	ids, budget, err := check(body)
	if err == nil || (!errors.Is(err, errMCPNativeWorkerReceiptsMissing) && !errors.Is(err, errMCPNativeDrainBudgetsMissing)) {
		return ids, budget, err
	}
	reader, err = c.StreamAppLogs(ctx, app, deployment, true, api.LogFilter{Grep: "mcp_task_worker_started", Since: "30s"})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = reader.Close() }()
	// Use a fresh window rather than appending to a possibly full retained page.
	var fresh bytes.Buffer
	scanner := bufio.NewScanner(io.LimitReader(reader, 4<<20))
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "data: ") {
			continue
		}
		fresh.WriteString(scanner.Text())
		fresh.WriteByte('\n')
		ids, budget, err = check(fresh.Bytes())
		if err == nil || (!errors.Is(err, errMCPNativeWorkerReceiptsMissing) && !errors.Is(err, errMCPNativeDrainBudgetsMissing)) {
			return ids, budget, err
		}
	}
	if ctx.Err() != nil {
		return nil, 0, fmt.Errorf("waiting for refreshed worker receipts: %w", ctx.Err())
	}
	if scanner.Err() != nil {
		return nil, 0, scanner.Err()
	}
	return nil, 0, errors.New("current worker receipts unavailable; update workers to refresh identity and drain budgets")
}
