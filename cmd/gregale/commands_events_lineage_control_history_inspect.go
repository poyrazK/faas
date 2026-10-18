package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type lineageHistoryActorSummary struct {
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	CredentialType string `json:"credential_type"`
	Receipts       int    `json:"receipts"`
}
type lineageHistoryInspection struct {
	File               string                                          `json:"file"`
	Valid              bool                                            `json:"valid"`
	App                string                                          `json:"app"`
	AppID              string                                          `json:"app_id"`
	SubscriptionID     string                                          `json:"subscription_id"`
	JobRequestID       string                                          `json:"job_request_id"`
	ExpectedCreatedAt  time.Time                                       `json:"expected_created_at"`
	JobRetainUntil     time.Time                                       `json:"job_retain_until"`
	ObservedAt         time.Time                                       `json:"observed_at"`
	Filters            api.EventExecutionLineageRetryJobControlFilters `json:"filters"`
	ObservedReceipts   int                                             `json:"observed_receipts"`
	SelectedReceipts   int                                             `json:"selected_receipts"`
	ThroughSequence    int64                                           `json:"through_sequence"`
	Actions            map[string]int                                  `json:"actions"`
	Outcomes           map[string]int                                  `json:"outcomes"`
	Actors             []lineageHistoryActorSummary                    `json:"actors"`
	LegacyUnattributed int                                             `json:"legacy_unattributed_receipts"`
	Comparison         *lineageHistoryComparison                       `json:"comparison,omitempty"`
}

func cmdEventsLineageControlHistoryInspect(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("subscription-lineage-retry-job-controls-inspect", flag.ContinueOnError)
	file := fs.String("file", "", "saved control-history artifact; read locally only")
	compare := fs.String("compare", "", "earlier baseline artifact with the same scope and filters")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || rejectUnexpectedFlagArgs(fs) || *file == "" {
		PrintUsage(os.Stderr, "usage: gregale events subscription-lineage-retry-job-controls-inspect --file PATH [--compare BASELINE_PATH]", "events")
		return 1
	}
	artifact, err := readLineageControlHistoryArtifact(*file)
	if err != nil {
		return printErr("Invalid control-history artifact", err)
	}
	report := summarizeLineageControlHistory(artifact, *file)
	if *compare != "" {
		baseline, err := readLineageControlHistoryArtifact(*compare)
		if err != nil {
			return printErr("Invalid baseline history", err)
		}
		comparison, err := compareLineageControlHistory(baseline, artifact, *compare)
		if err != nil {
			return printErr("History files cannot be compared", err)
		}
		report.Comparison = &comparison
	}
	code := 0
	if report.Comparison != nil && !report.Comparison.Consistent {
		code = 1
	}
	if jsonOutput {
		if output := jsonOut(writeJSON(report)); output != 0 {
			return output
		}
		return code
	}
	printLineageControlHistoryInspection(report)
	return code
}

func summarizeLineageControlHistory(artifact lineageControlHistoryExport, file string) lineageHistoryInspection {
	out := lineageHistoryInspection{File: file, Valid: true, App: artifact.App, AppID: canonicalLineageHistoryUUID(artifact.AppID), SubscriptionID: canonicalLineageHistoryUUID(artifact.SubscriptionID), JobRequestID: canonicalLineageHistoryUUID(artifact.JobRequestID), ExpectedCreatedAt: artifact.ExpectedCreatedAt, JobRetainUntil: artifact.JobRetainUntil, ObservedAt: artifact.ObservedAt, Filters: artifact.Filters, ObservedReceipts: artifact.ObservedReceipts, SelectedReceipts: len(artifact.Controls), ThroughSequence: artifact.ThroughSequence, Actions: make(map[string]int), Outcomes: make(map[string]int), Actors: make([]lineageHistoryActorSummary, 0)}
	actors := make(map[lineageHistoryActorSummary]int)
	for _, receipt := range artifact.Controls {
		receipt = canonicalLineageControlReceipt(receipt)
		out.Actions[receipt.Action]++
		out.Outcomes[receipt.ResultStatus]++
		if receipt.ActorKind == "" {
			out.LegacyUnattributed++
			continue
		}
		actor := lineageHistoryActorSummary{Kind: receipt.ActorKind, ID: receipt.ActorID, CredentialType: receipt.CredentialType}
		actors[actor]++
	}
	for actor, count := range actors {
		actor.Receipts = count
		out.Actors = append(out.Actors, actor)
	}
	sort.Slice(out.Actors, func(i, j int) bool {
		a, b := out.Actors[i], out.Actors[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.CredentialType < b.CredentialType
	})
	return out
}

func printLineageControlHistoryInspection(report lineageHistoryInspection) {
	_, _ = fmt.Fprintf(osStdout, "Valid history %s | job %s | %d selected of %d observed | through sequence %d\n", oneLine(report.File), report.JobRequestID, report.SelectedReceipts, report.ObservedReceipts, report.ThroughSequence)
	for _, action := range []string{"pause", "resume", "cancel"} {
		if count := report.Actions[action]; count > 0 {
			_, _ = fmt.Fprintf(osStdout, "Action %s: %d\n", action, count)
		}
	}
	for _, outcome := range []string{"pending", "paused", "completed", "cancelled"} {
		if count := report.Outcomes[outcome]; count > 0 {
			_, _ = fmt.Fprintf(osStdout, "Recorded outcome %s: %d\n", outcome, count)
		}
	}
	for _, actor := range report.Actors {
		_, _ = fmt.Fprintf(osStdout, "Actor %s:%s (%s): %d\n", actor.Kind, actor.ID, actor.CredentialType, actor.Receipts)
	}
	_, _ = fmt.Fprintf(osStdout, "Legacy receipts without attribution: %d\n", report.LegacyUnattributed)
	if comparison := report.Comparison; comparison != nil {
		_, _ = fmt.Fprintf(osStdout, "Compared with %s | unchanged %d | added %d | conflicts %d\n", oneLine(comparison.BaselineFile), comparison.Unchanged, len(comparison.Added), len(comparison.Conflicts))
		for _, added := range comparison.Added {
			_, _ = fmt.Fprintf(osStdout, "Added sequence %d | %s | %s\n", added.Sequence, added.CommandID, added.Action)
		}
		for _, conflict := range comparison.Conflicts {
			_, _ = fmt.Fprintf(osStdout, "Conflict sequence %d | %s | %s\n", conflict.Sequence, conflict.CommandID, conflict.Reason)
		}
	}
}
