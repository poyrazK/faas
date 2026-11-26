package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpTaskGroupStatusEntry struct {
	ReceiptIndex int                     `json:"receiptIndex"`
	Outcome      string                  `json:"outcome"`
	Task         *mcpTaskGroupTaskStatus `json:"task,omitempty"`
}

type mcpTaskGroupTaskStatus struct {
	mcpTaskStatusMetadata
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func cmdMCPTaskGroup(action string, args []string) int {
	fs := newFlagSet("mcp-task-group-"+action, flag.ContinueOnError)
	endpoint := fs.String("url", "", "full MCP endpoint URL used to save the group")
	tokenEnv := fs.String("token-env", "", "environment variable containing a current MCP client token")
	group := fs.String("group", "", "private group directory saved by the Node SDK")
	var includeResults bool
	if action == "status" {
		fs.BoolVar(&includeResults, "include-results", false, "include retained results and execution errors")
	}
	timeout := fs.Duration("timeout", 30*time.Second, "total group operation deadline")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *timeout <= 0 || strings.TrimSpace(*group) == "" || *tokenEnv == "" {
		return printErr("Invalid MCP Task group flags", errors.New("provide --url, --token-env and --group with a positive --timeout"))
	}
	selected, err := mcpTaskGroupEndpoint(*endpoint)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	client, err := mcphosting.NewClient(selected, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	dir, manifest, err := loadMCPTaskGroup(*group, selected)
	if err != nil {
		return printErr("MCP Task group", err)
	}
	defer func() { _ = dir.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if action == "stop" {
		return runMCPTaskGroupStop(ctx, client, manifest, dir)
	}
	entries := make([]mcpTaskGroupStatusEntry, len(manifest.Members))
	for i := range entries {
		entries[i] = mcpTaskGroupMemberStatus(ctx, client, manifest, dir, i, includeResults)
	}
	return jsonOut(writeJSON(entries))
}

func mcpTaskGroupEndpoint(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || strings.ContainsAny(endpoint, "?#") {
		return "", errors.New("MCP URL must be an absolute endpoint without credentials, query or fragment")
	}
	// The SDK fingerprints URL.href. Match its host casing, default ports and
	// root path, and use the same URL for both the binding and the requests.
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func mcpTaskGroupMemberStatus(ctx context.Context, client *mcphosting.Client, manifest mcpTaskGroupManifest, dir *os.Root, index int, includeResults bool) mcpTaskGroupStatusEntry {
	entry := mcpTaskGroupStatusEntry{ReceiptIndex: index}
	receipt, err := manifest.readMember(dir, index)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		entry.Outcome = "not_submitted"
	case err != nil:
		entry.Outcome = "invalid_receipt"
	default:
		entry.Task, entry.Outcome = readMCPTaskGroupStatus(ctx, client, receipt, includeResults)
	}
	return entry
}

func readMCPTaskGroupStatus(ctx context.Context, client *mcphosting.Client, receipt mcpTaskGroupReference, includeResults bool) (*mcpTaskGroupTaskStatus, string) {
	task, exchange, err := readMCPTaskGroupReference(ctx, client, receipt)
	if err == nil && receipt.TaskID == "" && includeResults && (task.Status == "completed" || task.Status == "failed") {
		task, exchange, err = client.GetTask(ctx, task.TaskID)
	}
	if err != nil {
		outcome, _ := mcpTaskReceiptLookupOutcome(task, exchange, err)
		if outcome.Task != nil {
			return &mcpTaskGroupTaskStatus{mcpTaskStatusMetadata: *outcome.Task}, outcome.Outcome
		}
		return nil, outcome.Outcome
	}
	status := &mcpTaskGroupTaskStatus{mcpTaskStatusMetadata: mcpTaskSnapshot(task)}
	if includeResults {
		switch task.Status {
		case "completed":
			status.Result = task.Result
		case "failed":
			status.Error = task.Error
		}
	}
	return status, "available"
}

func readMCPTaskGroupReference(ctx context.Context, client *mcphosting.Client, receipt mcpTaskGroupReference) (mcphosting.Task, mcphosting.Exchange, error) {
	if receipt.TaskID != "" {
		return client.GetTask(ctx, receipt.TaskID)
	}
	return client.GetTaskBySubmissionKey(ctx, receipt.SubmissionKey)
}
