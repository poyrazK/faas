package main

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
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
	retry := flags.Bool("retry-unconfirmed", false, "retry recorded submissions after a Task-not-found lookup")
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
	return runMCPTaskGroupSubmit(ctx, client, *group, plan, *retry)
}

func runMCPTaskGroupSubmit(ctx context.Context, client *mcphosting.Client, path string, plan []mcpTaskGroupSubmission, retry bool) int {
	dir, manifest, err := openMCPTaskGroupSubmission(ctx, client, path, plan)
	if err != nil {
		return printErr("MCP Task group submit", err)
	}
	defer func() { _ = dir.Close() }()
	admission := mcpTaskGroupAdmission{client: client, dir: dir, manifest: manifest}
	if err := checkMCPTaskGroupOpen(dir, manifest); err != nil {
		return printErr("MCP Task group submit", err)
	}
	entries, err := admission.submit(ctx, plan, retry)
	if err != nil {
		return printErr("MCP Task group submit", err)
	}
	if code := jsonOut(writeJSON(entries)); code != 0 {
		return code
	}
	exitCode := 0
	for _, entry := range entries {
		switch entry.Outcome {
		case "accepted":
		case "unconfirmed":
			exitCode = exitCodeForStatus(http.StatusServiceUnavailable)
		default:
			exitCode = max(exitCode, 1)
		}
	}
	return exitCode
}

func (a mcpTaskGroupAdmission) submit(ctx context.Context, plan []mcpTaskGroupSubmission, retry bool) ([]mcpTaskReceiptWatchEvent, error) {
	entries := make([]mcpTaskReceiptWatchEvent, len(plan))
	attempts := make([]*mcpTaskGroupReceipt, len(plan))
	for i := range entries {
		current, attempt, err := a.inspect(ctx, i)
		if err != nil {
			return nil, fmt.Errorf("read Task group member %d: %w", i, err)
		}
		entries[i] = mcpTaskReceiptWatchEvent{ReceiptIndex: i, mcpTaskReceiptStatusResult: current}
		if retry {
			attempts[i] = attempt
		}
	}
	for i, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if entry.Outcome == "prepared" || attempts[i] != nil {
			current, err := a.send(ctx, i, plan[i], attempts[i])
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

func (a mcpTaskGroupAdmission) inspect(ctx context.Context, index int) (mcpTaskReceiptStatusResult, *mcpTaskGroupReceipt, error) {
	receipt, err := a.manifest.readMemberReceipt(a.dir, index)
	if errors.Is(err, fs.ErrNotExist) {
		return mcpTaskReceiptStatusResult{Outcome: "prepared"}, nil, nil
	}
	if err != nil {
		return mcpTaskReceiptStatusResult{}, nil, errInvalidMCPTaskGroup
	}
	task, exchange, err := readMCPTaskGroupReference(ctx, a.client, receipt.mcpTaskGroupReference)
	switch {
	case err == nil:
		return mcpTaskGroupAccepted(task), nil, nil
	case receipt.TaskID == "" && mcphosting.IsTaskSubmissionNotFound(err):
		return mcpTaskReceiptStatusResult{Outcome: "unconfirmed"}, &receipt, nil
	case receipt.TaskID == "" && taskGroupAdmissionUnconfirmed(exchange.HTTPStatus, err):
		return mcpTaskReceiptStatusResult{Outcome: "unconfirmed"}, nil, nil
	default:
		return mcpTaskReceiptStatusResult{Outcome: "unavailable"}, nil, nil
	}
}

func (a mcpTaskGroupAdmission) send(ctx context.Context, index int, item mcpTaskGroupSubmission, attempt *mcpTaskGroupReceipt) (mcpTaskReceiptStatusResult, error) {
	owner := mcphosting.TaskGroupOwner{GroupID: a.manifest.ID, Binding: a.manifest.OwnerBinding}
	if err := checkMCPTaskGroupOpen(a.dir, a.manifest); err != nil {
		return mcpTaskReceiptStatusResult{}, err
	}
	if attempt != nil {
		savedProof, err := hex.DecodeString(attempt.CredentialProof)
		if err != nil || !hmac.Equal(savedProof, mcpTaskGroupCredentialProof(*attempt, a.client.Token)) {
			return mcpTaskReceiptStatusResult{Outcome: "credential_mismatch"}, nil
		}
		if !mcpTaskGroupKeyWithinWindow(attempt.SubmissionKey) {
			return mcpTaskReceiptStatusResult{Outcome: "retry_window_closed"}, nil
		}
	}
	if err := a.client.VerifyTaskGroupTool(ctx, owner, item.tool); err != nil {
		return mcpTaskReceiptStatusResult{Outcome: "unavailable"}, nil
	}
	if attempt == nil {
		receipt, err := newMCPTaskGroupAttempt(a.manifest, index, a.client.Token)
		if err != nil {
			return mcpTaskReceiptStatusResult{}, err
		}
		if err := recordMCPTaskGroupAttempt(a.dir, index, receipt); err != nil {
			if errors.Is(err, fs.ErrExist) {
				current, _, err := a.inspect(ctx, index)
				return current, err
			}
			return mcpTaskReceiptStatusResult{}, errors.New("could not durably record Task submission attempt")
		}
		attempt = &receipt
	}
	// A stop that wins this check can discover the already durable receipt.
	if err := checkMCPTaskGroupOpen(a.dir, a.manifest); err != nil {
		return mcpTaskReceiptStatusResult{}, err
	}
	if !mcpTaskGroupKeyWithinWindow(attempt.SubmissionKey) || ctx.Err() != nil {
		return mcpTaskReceiptStatusResult{Outcome: "unconfirmed"}, nil
	}
	task, x, err := a.client.SubmitBoundTask(ctx, owner, attempt.SubmissionKey, item.tool, item.args, item.deferFormInput)
	if err != nil {
		if x.HTTPStatus >= 400 && x.HTTPStatus < 500 && x.HTTPStatus != http.StatusRequestTimeout {
			return mcpTaskReceiptStatusResult{Outcome: "rejected"}, nil
		}
		return mcpTaskReceiptStatusResult{Outcome: "unconfirmed"}, nil
	}
	return mcpTaskGroupAccepted(task), nil
}

func mcpTaskGroupKeyWithinWindow(key string) bool {
	age := mcpTaskGroupKeyAge(uuid.MustParse(key))
	return age >= 0 && age < mcpTaskGroupSubmissionWindow
}

func mcpTaskGroupAccepted(task mcphosting.Task) mcpTaskReceiptStatusResult {
	snapshot := mcpTaskSnapshot(task)
	return mcpTaskReceiptStatusResult{Outcome: "accepted", Task: &snapshot}
}
