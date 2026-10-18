package main

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/datarepair"
)

func printDataRepairValidation(raw []byte, id, digest string) int {
	var report datarepair.ValidationReport
	if json.Unmarshal(raw, &report) != nil || !report.Valid() || report.PlanID != id || report.PlanDigest != digest {
		return printErr("Invalid repair validation response", datarepair.ErrUnavailable)
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if _, err := fmt.Fprintln(osStdout, string(encoded)); err != nil {
		return printErr("Cannot write repair validation", err)
	}
	if !report.Matches {
		return 1
	}
	return 0
}
