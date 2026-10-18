package main

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/datarepair"
)

func printDataRepairVerification(raw []byte, id, digest string, hosted bool) int {
	var report datarepair.VerificationReport
	if json.Unmarshal(raw, &report) != nil || !report.Valid() || report.PlanID != id || report.PlanDigest != digest || hosted && report.RepairOperationID != id {
		return printErr("Invalid repair verification response", datarepair.ErrUnavailable)
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if _, err := fmt.Fprintln(osStdout, string(encoded)); err != nil {
		return printErr("Cannot write repair verification", err)
	}
	if !report.Available {
		return 1
	}
	// Later edits and missing records are observations, not a failed commit.
	return 0
}
