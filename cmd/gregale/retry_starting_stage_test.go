package main_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRetryStartingStage(t *testing.T) {
	raw, _ := json.Marshal(state.RetryStageState(state.StageSecurityScan))
	stage, reason := main.RetryStartingStage(raw, "security_scan")
	if stage != "source_download" || reason == "" {
		t.Fatalf("stage=%s reason=%s", stage, reason)
	}
	stage, reason = main.RetryStartingStage(nil, "security_scan")
	if stage != "security_scan" || reason != "" {
		t.Fatalf("legacy response: stage=%s reason=%s", stage, reason)
	}
}
