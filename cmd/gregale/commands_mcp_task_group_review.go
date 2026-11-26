package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpTaskGroupReviewEntry struct {
	ReceiptIndex int    `json:"receiptIndex"`
	Outcome      string `json:"outcome"`
	TaskID       string `json:"taskId,omitempty"`
	Status       string `json:"status,omitempty"`
	TemplatePath string `json:"templatePath,omitempty"`
}

type mcpTaskGroupReviewOutput struct {
	dir  *os.Root
	path string
}

func runMCPTaskGroupReview(ctx context.Context, client *mcphosting.Client, manifest mcpTaskGroupManifest, dir *os.Root, path string) int {
	output, err := openMCPTaskGroupReviewOutput(path)
	if err != nil {
		return printErr("MCP Task group review output", err)
	}
	defer func() { _ = output.dir.Close() }()
	entries := make([]mcpTaskGroupReviewEntry, len(manifest.Members))
	for i := range entries {
		entries[i] = reviewMCPTaskGroupMember(ctx, client, manifest, dir, output, i)
	}
	return jsonOut(writeJSON(entries))
}

func openMCPTaskGroupReviewOutput(path string) (mcpTaskGroupReviewOutput, error) {
	var output mcpTaskGroupReviewOutput
	path, err := filepath.Abs(path)
	if err != nil || filepath.Dir(path) == path {
		return output, errors.New("invalid review output directory")
	}
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return output, errors.New("review output requires an existing parent directory")
	}
	defer func() { _ = parent.Close() }()
	if info, err := parent.Stat("."); err != nil || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
		return output, errors.New("review output requires a private or sticky parent directory")
	}
	name := filepath.Base(path)
	if err := parent.Mkdir(name, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return output, errors.New("could not create private review output directory")
	}
	dir, err := openMCPTaskGroupDirectory(parent, name)
	if err != nil {
		return output, errors.New("review output must be a private directory owned by the current user")
	}
	if err := syncPrivateReceiptDirectory(parent); err != nil {
		_ = dir.Close()
		return output, errors.New("could not sync review output directory")
	}
	return mcpTaskGroupReviewOutput{dir: dir, path: path}, nil
}

func reviewMCPTaskGroupMember(ctx context.Context, client *mcphosting.Client, manifest mcpTaskGroupManifest, group *os.Root, output mcpTaskGroupReviewOutput, index int) mcpTaskGroupReviewEntry {
	entry := mcpTaskGroupReviewEntry{ReceiptIndex: index}
	receipt, err := manifest.readMember(group, index)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		entry.Outcome = "not_submitted"
		return entry
	case err != nil:
		entry.Outcome = "invalid_receipt"
		return entry
	}
	draft, exchange, err := readMCPTaskGroupReview(ctx, client, receipt)
	if err != nil {
		result, _ := mcpTaskReceiptLookupOutcome(mcphosting.Task{}, exchange, err)
		entry.Outcome = result.Outcome
		if errors.Is(err, mcphosting.ErrTaskInputReviewUnsupported) {
			entry.Outcome = "review_unsupported"
		}
		return entry
	}
	entry.TaskID, entry.Status = draft.Task.TaskID, draft.Task.Status
	switch {
	case draft.Task.Status != "input_required":
		entry.Outcome = "not_pending"
	case draft.Revision == "":
		entry.Outcome = "not_reviewable"
	default:
		name := fmt.Sprintf("member-%d-input.json", index)
		entry.Outcome = exportMCPTaskGroupReview(output.dir, name, client.Endpoint, draft)
		switch entry.Outcome {
		case "written", "existing", "stale_template", "template_conflict":
			entry.TemplatePath = filepath.Join(output.path, name)
		}
	}
	return entry
}

func readMCPTaskGroupReview(ctx context.Context, client *mcphosting.Client, receipt mcpTaskGroupReference) (mcphosting.TaskInputReview, mcphosting.Exchange, error) {
	if receipt.TaskID != "" {
		return client.GetTaskInputReview(ctx, receipt.TaskID)
	}
	task, exchange, err := client.GetTaskBySubmissionKey(ctx, receipt.SubmissionKey)
	if err != nil || task.Status != "input_required" {
		return mcphosting.TaskInputReview{Task: task}, exchange, err
	}
	return client.GetTaskInputReview(ctx, task.TaskID)
}

func exportMCPTaskGroupReview(dir *os.Root, name, endpoint string, draft mcphosting.TaskInputReview) string {
	review, err := newMCPTaskInputTemplate(endpoint, draft.Task, draft.Revision)
	if err != nil {
		return "not_reviewable"
	}
	body, err := encodeMCPTaskInputTemplate(review)
	if err != nil {
		return "write_failed"
	}
	if err := linkPrivateReceiptAt(dir, name, body); err != nil {
		if errors.Is(err, fs.ErrExist) {
			outcome := existingMCPTaskGroupReview(dir, name, endpoint, review)
			if outcome == "existing" && syncPrivateReceiptDirectory(dir) != nil {
				return "write_failed"
			}
			return outcome
		}
		return "write_failed"
	}
	if err := syncPrivateReceiptDirectory(dir); err != nil {
		return "write_failed"
	}
	return "written"
}

func existingMCPTaskGroupReview(dir *os.Root, name, endpoint string, current mcpTaskInputReview) string {
	body, err := readMCPTaskGroupFile(dir, name, mcpTaskInputReviewMaxBytes)
	if err != nil {
		return "template_conflict"
	}
	draft, err := decodeMCPTaskInputReview(body, endpoint)
	if err != nil || draft.TaskID != current.TaskID {
		return "template_conflict"
	}
	if draft.Revision != current.Revision {
		return "stale_template"
	}
	if len(draft.Forms) != len(current.Forms) {
		return "template_conflict"
	}
	for id, form := range current.Forms {
		if draft.Forms[id].FormSHA256 != form.FormSHA256 {
			return "template_conflict"
		}
	}
	return "existing"
}
