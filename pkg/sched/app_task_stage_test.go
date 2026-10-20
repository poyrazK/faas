package sched_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/sched"
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
		{"placement", sched.WithAppTaskStage("placement", errors.New("no node")), "placement", "none"},
		{"inner stage wins", sched.WithAppTaskStage("resolve", sched.WithAppTaskStage("admission", errors.New("full"))), "admission", "none"},
		{"vmmd grpc", sched.WithAppTaskStage("vmmd_restore", fmt.Errorf("restore: %w", vmmdErr)), "vmmd_restore", "Unavailable"},
		{"vmmd joined cleanup", sched.WithAppTaskStage("vmmd_restore", errors.Join(vmmdErr, errors.New("cleanup"))), "vmmd_restore", "Unavailable"},
		{"deployment unavailable", sched.WithAppTaskStage("resolve", state.ErrAppTaskDeploymentUnavailable), "deployment_unavailable", "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sched.AppTaskFailureStage(tt.err); got != tt.wantStage {
				t.Errorf("stage = %q, want %q", got, tt.wantStage)
			}
			if got := sched.AppTaskGRPCCode(tt.err); got != tt.wantCode {
				t.Errorf("grpc code = %q, want %q", got, tt.wantCode)
			}
		})
	}
	inactive := sched.WithAppTaskStage("resolve", sched.ErrAppTaskAccountInactive)
	if !errors.Is(inactive, sched.ErrAppTaskAccountInactive) {
		t.Fatal("stage tagging must keep the wrapped sentinel reachable")
	}
}
