package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
)

func TestMCPConnectionHealthOutput(t *testing.T) {
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	var output bytes.Buffer
	(*main.OsStdout) = &output
	(*main.JsonOutput) = false
	t.Cleanup(func() { (*main.OsStdout) = oldOut; (*main.JsonOutput) = oldJSON })
	snapshot := api.MCPConnectionHealthSnapshot{TotalIssues: 1, Complete: false, NextOffset: 50, Items: []api.MCPConnectionHealthIssue{{AppSlug: "app", Role: "reader", Kind: "issuance_unknown", State: "manual_required", RecoveryAction: "review_provider", RecoveryPath: "/v1/mcp-oauth-retirements", RetirementID: "retirement"}}}
	if main.WriteMCPConnectionHealth(snapshot) != 0 {
		t.Fatal("output failed")
	}
	for _, want := range []string{"1 tracked issues", "Inventory is incomplete", "app/reader", "review_provider", "Retirement: retirement", "--offset 50"} {
		if !strings.Contains(output.String(), want) {
			t.Fatal("missing health guidance", want)
		}
	}
}
