package sched

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/state"
)

// SB-8: a failed app task restore must say where it failed without logging
// error text. The innermost stage wins, and the original error stays reachable.
func TestAppTaskFailureStageAndGRPCCode(t *testing.T) {
	vmmdErr := status.Error(codes.Unavailable, "vmmd: node draining")
	tests := []struct {
		name      string
		err       error
		wantStage string
		wantCode  string
	}{
		{"nil", nil, "none", "none"},
		{"untagged", errors.New("boom"), "unknown", "none"},
		{"placement", withAppTaskStage("placement", errors.New("no node")), "placement", "none"},
		{"inner stage wins", withAppTaskStage("resolve", withAppTaskStage("admission", errors.New("full"))), "admission", "none"},
		{"vmmd grpc", withAppTaskStage("vmmd_restore", fmt.Errorf("restore: %w", vmmdErr)), "vmmd_restore", "Unavailable"},
		{"vmmd joined cleanup", withAppTaskStage("vmmd_restore", errors.Join(vmmdErr, errors.New("cleanup"))), "vmmd_restore", "Unavailable"},
		{"deployment unavailable", withAppTaskStage("resolve", state.ErrAppTaskDeploymentUnavailable), "deployment_unavailable", "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appTaskFailureStage(tt.err); got != tt.wantStage {
				t.Errorf("stage = %q, want %q", got, tt.wantStage)
			}
			if got := appTaskGRPCCode(tt.err); got != tt.wantCode {
				t.Errorf("grpc code = %q, want %q", got, tt.wantCode)
			}
		})
	}
	inactive := withAppTaskStage("resolve", ErrAppTaskAccountInactive)
	if !errors.Is(inactive, ErrAppTaskAccountInactive) {
		t.Fatal("stage tagging must keep the wrapped sentinel reachable")
	}
}
