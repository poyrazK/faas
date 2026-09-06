package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRetryStartingStage(t *testing.T) {
	raw, _ := json.Marshal(state.RetryStageState(state.StageSecurityScan))
	stage, reason := retryStartingStage(raw, "security_scan")
	if stage != "source_download" || reason == "" {
		t.Fatalf("stage=%s reason=%s", stage, reason)
	}
	stage, reason = retryStartingStage(nil, "security_scan")
	if stage != "security_scan" || reason != "" {
		t.Fatalf("legacy response: stage=%s reason=%s", stage, reason)
	}
}
