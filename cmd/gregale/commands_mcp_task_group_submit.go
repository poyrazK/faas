package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

var mcpTaskGroupBearer = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)

type mcpTaskGroupAdmission struct {
	client   *mcphosting.Client
	dir      *os.Root
	manifest mcpTaskGroupManifest
}

func cmdMCPTaskGroupSubmit(args []string) int {
	flags := newFlagSet("mcp-task-group-submit", flag.ContinueOnError)
	endpoint := flags.String("url", "", "full MCP endpoint URL for the group")
	tokenEnv := flags.String("token-env", "", "environment variable containing a current MCP client token")
	group := flags.String("group", "", "private interoperable Task group directory")
	planPath := flags.String("plan", "", "JSON file with the original tool calls in order")
	timeout := flags.Duration("timeout", 30*time.Second, "total group submission deadline")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 || *timeout <= 0 || strings.TrimSpace(*group) == "" || *planPath == "" || *tokenEnv == "" {
		return printErr("Invalid MCP Task group flags", errors.New("provide --url, --token-env, --group and --plan with a positive --timeout"))
	}
	plan, err := readMCPTaskGroupPlan(*planPath)
	if err != nil {
		return printErr("MCP Task group plan", err)
	}
	selected, err := mcpTaskGroupEndpoint(*endpoint)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil || !mcpTaskGroupBearer.MatchString(token) {
		return printErr("MCP client token", errors.New("--token-env must contain a valid bearer token"))
	}
	client, err := mcphosting.NewClient(selected, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	return runMCPTaskGroupSubmit(ctx, client, *group, plan)
}

func runMCPTaskGroupSubmit(ctx context.Context, client *mcphosting.Client, path string, plan []mcpTaskGroupSubmission) int {
	dir, manifest, err := openMCPTaskGroupSubmission(ctx, client, path, plan)
	if err != nil {
		return printErr("MCP Task group submit", err)
	}
	defer func() { _ = dir.Close() }()
	admission := mcpTaskGroupAdmission{client: client, dir: dir, manifest: manifest}
	if err := checkMCPTaskGroupOpen(dir, manifest); err != nil {
		return printErr("MCP Task group submit", err)
	}
	entries, err := admission.submit(ctx, plan)
	if err != nil {
		return printErr("MCP Task group submit", err)
	}
	return jsonOut(writeJSON(entries))
}

func (a mcpTaskGroupAdmission) submit(ctx context.Context, plan []mcpTaskGroupSubmission) ([]mcpTaskReceiptWatchEvent, error) {
	entries := make([]mcpTaskReceiptWatchEvent, len(plan))
	for i := range entries {
		current, err := a.inspect(ctx, i)
		if err != nil {
			return nil, fmt.Errorf("read Task group member %d: %w", i, err)
		}
		entries[i] = mcpTaskReceiptWatchEvent{ReceiptIndex: i, mcpTaskReceiptStatusResult: current}
	}
	for i, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if entry.Outcome == "prepared" {
			current, err := a.send(ctx, i, plan[i])
			if err != nil {
				return nil, fmt.Errorf("submit Task group member %d: %w", i, err)
			}
			entries[i].mcpTaskReceiptStatusResult = current
		}
		if entries[i].Outcome != "accepted" {
			break
		}
	}
	return entries, nil
}

func (a mcpTaskGroupAdmission) inspect(ctx context.Context, index int) (mcpTaskReceiptStatusResult, error) {
	receipt, err := a.manifest.readMember(a.dir, index)
	if errors.Is(err, fs.ErrNotExist) {
		return mcpTaskReceiptStatusResult{Outcome: "prepared"}, nil
	}
	if err != nil {
		return mcpTaskReceiptStatusResult{}, errInvalidMCPTaskGroup
	}
	task, exchange, err := readMCPTaskGroupReference(ctx, a.client, receipt)
	switch {
	case err == nil:
		return mcpTaskGroupAccepted(task), nil
	case receipt.TaskID == "" && taskGroupAdmissionUnconfirmed(exchange.HTTPStatus, err):
		return mcpTaskReceiptStatusResult{Outcome: "unconfirmed"}, nil
	default:
		return mcpTaskReceiptStatusResult{Outcome: "unavailable"}, nil
	}
}

func (a mcpTaskGroupAdmission) send(ctx context.Context, index int, item mcpTaskGroupSubmission) (mcpTaskReceiptStatusResult, error) {
	owner := mcphosting.TaskGroupOwner{GroupID: a.manifest.ID, Binding: a.manifest.OwnerBinding}
	if err := checkMCPTaskGroupOpen(a.dir, a.manifest); err != nil {
		return mcpTaskReceiptStatusResult{}, err
	}
	if err := a.client.VerifyTaskGroupTool(ctx, owner, item.tool); err != nil {
		return mcpTaskReceiptStatusResult{Outcome: "unavailable"}, nil
	}
	receipt, err := newMCPTaskGroupAttempt(a.manifest, index, a.client.Token)
	if err != nil {
		return mcpTaskReceiptStatusResult{}, err
	}
	if err := recordMCPTaskGroupAttempt(a.dir, index, receipt); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return a.inspect(ctx, index)
		}
		return mcpTaskReceiptStatusResult{}, errors.New("could not durably record Task submission attempt")
	}
	// A stop that wins this check can discover the already durable receipt.
	if err := checkMCPTaskGroupOpen(a.dir, a.manifest); err != nil {
		return mcpTaskReceiptStatusResult{}, err
	}
	key := uuid.MustParse(receipt.SubmissionKey)
	if age := mcpTaskGroupKeyAge(key); age < 0 || age >= mcpTaskGroupSubmissionWindow || ctx.Err() != nil {
		return mcpTaskReceiptStatusResult{Outcome: "unconfirmed"}, nil
	}
	task, x, err := a.client.SubmitBoundTask(ctx, owner, receipt.SubmissionKey, item.tool, item.args, item.deferFormInput)
	if err != nil {
		if x.HTTPStatus >= 400 && x.HTTPStatus < 500 && x.HTTPStatus != http.StatusRequestTimeout {
			return mcpTaskReceiptStatusResult{Outcome: "rejected"}, nil
		}
		return mcpTaskReceiptStatusResult{Outcome: "unconfirmed"}, nil
	}
	return mcpTaskGroupAccepted(task), nil
}

func mcpTaskGroupAccepted(task mcphosting.Task) mcpTaskReceiptStatusResult {
	snapshot := mcpTaskSnapshot(task)
	return mcpTaskReceiptStatusResult{Outcome: "accepted", Task: &snapshot}
}
