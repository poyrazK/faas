package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

type lineageHistoryRecordReference struct {
	Sequence  int64  `json:"sequence"`
	CommandID string `json:"command_id"`
	Action    string `json:"action"`
}
type lineageHistoryConflict struct {
	Sequence         int64  `json:"sequence"`
	CommandID        string `json:"command_id"`
	Reason           string `json:"reason"`
	PreviousSequence int64  `json:"previous_sequence,omitempty"`
}
type lineageHistoryComparison struct {
	BaselineFile            string                          `json:"baseline_file"`
	BaselineThroughSequence int64                           `json:"baseline_through_sequence"`
	ThroughSequence         int64                           `json:"through_sequence"`
	Consistent              bool                            `json:"consistent"`
	Unchanged               int                             `json:"unchanged_receipts"`
	Added                   []lineageHistoryRecordReference `json:"added"`
	Conflicts               []lineageHistoryConflict        `json:"conflicts"`
}

func compareLineageControlHistory(baseline, target lineageControlHistoryExport, baselineFile string) (lineageHistoryComparison, error) {
	out := lineageHistoryComparison{BaselineFile: baselineFile, BaselineThroughSequence: baseline.ThroughSequence, ThroughSequence: target.ThroughSequence, Consistent: true, Added: make([]lineageHistoryRecordReference, 0), Conflicts: make([]lineageHistoryConflict, 0)}
	if !sameBindingDeployment(baseline.AppID, target.AppID) || !sameBindingDeployment(baseline.SubscriptionID, target.SubscriptionID) || !sameBindingDeployment(baseline.JobRequestID, target.JobRequestID) || !baseline.ExpectedCreatedAt.Equal(target.ExpectedCreatedAt) || !baseline.JobRetainUntil.Equal(target.JobRetainUntil) {
		return out, fmt.Errorf("history files must describe the same job incarnation and original retention")
	}
	if baseline.Filters != target.Filters {
		return out, fmt.Errorf("history comparison requires identical normalized filters")
	}
	if target.ThroughSequence < baseline.ThroughSequence {
		return out, fmt.Errorf("target sequence boundary precedes the baseline; swap the files")
	}
	oldSequences := make(map[int64]api.EventExecutionLineageRetryJobControlReceipt)
	oldCommands := make(map[string]int64)
	newSequences := make(map[int64]bool)
	for _, receipt := range baseline.Controls {
		receipt = canonicalLineageControlReceipt(receipt)
		oldSequences[receipt.Sequence] = receipt
		oldCommands[receipt.CommandID] = receipt.Sequence
	}
	for _, receipt := range target.Controls {
		receipt = canonicalLineageControlReceipt(receipt)
		newSequences[receipt.Sequence] = true
		old, present := oldSequences[receipt.Sequence]
		if present {
			if receipt == old {
				out.Unchanged++
			} else {
				out.Conflicts = append(out.Conflicts, lineageHistoryConflict{Sequence: receipt.Sequence, CommandID: receipt.CommandID, Reason: "receipt_changed"})
			}
		} else if receipt.Sequence <= baseline.ThroughSequence {
			out.Conflicts = append(out.Conflicts, lineageHistoryConflict{Sequence: receipt.Sequence, CommandID: receipt.CommandID, Reason: "baseline_receipt_missing"})
		}
		previous, known := oldCommands[receipt.CommandID]
		if known && previous != receipt.Sequence {
			out.Conflicts = append(out.Conflicts, lineageHistoryConflict{Sequence: receipt.Sequence, CommandID: receipt.CommandID, Reason: "command_resequenced", PreviousSequence: previous})
		} else if !present && !known && receipt.Sequence > baseline.ThroughSequence {
			out.Added = append(out.Added, lineageHistoryRecordReference{Sequence: receipt.Sequence, CommandID: receipt.CommandID, Action: receipt.Action})
		}
	}
	for _, receipt := range baseline.Controls {
		if !newSequences[receipt.Sequence] {
			out.Conflicts = append(out.Conflicts, lineageHistoryConflict{Sequence: receipt.Sequence, CommandID: canonicalLineageHistoryUUID(receipt.CommandID), Reason: "target_receipt_missing"})
		}
	}
	out.Consistent = len(out.Conflicts) == 0
	return out, nil
}
