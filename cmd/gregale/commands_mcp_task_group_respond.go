package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpTaskGroupResponseEntry struct {
	ReceiptIndex *int                   `json:"receiptIndex,omitempty"`
	ReviewFile   string                 `json:"reviewFile"`
	Outcome      string                 `json:"outcome"`
	Task         *mcpTaskStatusMetadata `json:"task,omitempty"`
}

type mcpTaskGroupResponse struct {
	entry     mcpTaskGroupResponseEntry
	review    mcpTaskInputReview
	responses map[string]mcphosting.InputResponse
}

func runMCPTaskGroupRespond(ctx context.Context, client *mcphosting.Client, manifest mcpTaskGroupManifest, dir *os.Root, paths []string) int {
	selected := readMCPTaskGroupResponses(paths, client.Endpoint)
	members, unconfirmed := resolveMCPTaskGroupResponseMembers(ctx, client, manifest, dir, selected)
	ordered := make([]*mcpTaskGroupResponse, len(manifest.Members))
	unmatched := make([]mcpTaskGroupResponseEntry, 0)
	for i := range selected {
		item := &selected[i]
		if item.entry.Outcome != "" {
			unmatched = append(unmatched, item.entry)
			continue
		}
		index, matched := members[item.review.TaskID]
		switch {
		case matched && index >= 0:
			item.entry.ReceiptIndex = &index
			ordered[index] = item
			continue
		case matched:
			item.entry.Outcome = "ambiguous_member"
		case unconfirmed:
			item.entry.Outcome = "membership_unconfirmed"
		default:
			item.entry.Outcome = "not_in_group"
		}
		unmatched = append(unmatched, item.entry)
	}
	entries := make([]mcpTaskGroupResponseEntry, 0, len(paths))
	for _, item := range ordered {
		if item != nil {
			respondToMCPTaskGroupMember(ctx, client, item)
			entries = append(entries, item.entry)
		}
	}
	entries = append(entries, unmatched...)
	if code := jsonOut(writeJSON(entries)); code != 0 {
		return code
	}
	return mcpTaskGroupResponseExitCode(entries)
}

func readMCPTaskGroupResponses(paths []string, endpoint string) []mcpTaskGroupResponse {
	selected := make([]mcpTaskGroupResponse, len(paths))
	first := make(map[string]int, len(paths))
	for i, path := range paths {
		item := &selected[i]
		item.entry.ReviewFile = path
		review, err := readMCPTaskInputReview(path, endpoint)
		if err != nil {
			item.entry.Outcome = "invalid_review"
			continue
		}
		item.review = review
		// Conflicting selections cannot choose the answer by argument order,
		// even when one of the selected files still needs a response.
		if prior, duplicate := first[review.TaskID]; duplicate {
			selected[prior].entry.Outcome = "duplicate_review"
			item.entry.Outcome = "duplicate_review"
			continue
		}
		first[review.TaskID] = i
		item.responses, err = reviewedMCPTaskInputResponses(review)
		if err != nil {
			item.entry.Outcome = "invalid_review"
		}
	}
	return selected
}

func resolveMCPTaskGroupResponseMembers(ctx context.Context, client *mcphosting.Client, manifest mcpTaskGroupManifest, dir *os.Root, selected []mcpTaskGroupResponse) (map[string]int, bool) {
	wanted := make(map[string]bool, len(selected))
	for _, item := range selected {
		if item.entry.Outcome == "" {
			wanted[item.review.TaskID] = true
		}
	}
	members := make(map[string]int, len(wanted))
	if len(wanted) == 0 {
		return members, false
	}
	var unconfirmed bool
	for i := range manifest.Members {
		receipt, err := manifest.readMember(dir, i)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			unconfirmed = true
			continue
		}
		id := receipt.TaskID
		if id == "" {
			task, _, err := client.GetTaskBySubmissionKey(ctx, receipt.SubmissionKey)
			if err != nil {
				unconfirmed = true
				continue
			}
			id = task.TaskID
		}
		if wanted[id] {
			if _, duplicate := members[id]; duplicate {
				members[id] = -1
			} else {
				members[id] = i
			}
		}
	}
	return members, unconfirmed
}

func respondToMCPTaskGroupMember(ctx context.Context, client *mcphosting.Client, item *mcpTaskGroupResponse) {
	draft, exchange, err := client.GetTaskInputReview(ctx, item.review.TaskID)
	if err != nil {
		result, _ := mcpTaskReceiptLookupOutcome(mcphosting.Task{}, exchange, err)
		item.entry.Outcome = result.Outcome
		if errors.Is(err, mcphosting.ErrTaskInputReviewUnsupported) {
			item.entry.Outcome = "review_unsupported"
		}
		return
	}
	current, err := applyMCPTaskReviewedInput(ctx, client, draft, item.review, item.responses)
	snapshot := mcpTaskSnapshot(draft.Task)
	var unconfirmed *mcphosting.TaskInputUpdateUnconfirmedError
	switch {
	case err == nil:
		item.entry.Outcome, snapshot = "applied", mcpTaskSnapshot(current)
	case errors.Is(err, mcphosting.ErrTaskInputReviewStale), errors.Is(err, errMCPTaskInputReviewMismatch):
		item.entry.Outcome = "stale"
	case errors.As(err, &unconfirmed):
		item.entry.Outcome, snapshot = "unconfirmed", mcpTaskSnapshot(unconfirmed.Task)
		if snapshot.Status == "" {
			snapshot.Status = "unknown"
		}
		snapshot.NextAction = "retry_status"
	default:
		item.entry.Outcome = "failed"
	}
	item.entry.Task = &snapshot
}

func mcpTaskGroupResponseExitCode(entries []mcpTaskGroupResponseEntry) int {
	exitCode := 0
	for _, entry := range entries {
		switch entry.Outcome {
		case "applied":
		case "unconfirmed", "retry":
			exitCode = max(exitCode, exitCodeForStatus(http.StatusServiceUnavailable))
		default:
			exitCode = max(exitCode, 1)
		}
	}
	return exitCode
}
