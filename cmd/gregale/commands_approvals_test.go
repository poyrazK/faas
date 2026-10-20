package main_test

import (
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// adr: 20261020085706 — `gregale approvals require` flag shapes.

func TestApprovalPolicyRequest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		approvals  int
		afterStage int
		timeout    string
		approvers  string
		wantMins   int
		wantErr    bool
	}{
		{name: "defaults", approvals: 2, wantMins: 1440},
		{name: "four hours, developers", approvals: 1, timeout: "4h", approvers: "developer", wantMins: 240},
		{name: "stage two", approvals: 1, afterStage: 2, timeout: "30m", wantMins: 30},
		{name: "no approvals", approvals: 0, wantErr: true},
		{name: "too many approvals", approvals: 6, wantErr: true},
		{name: "seconds", approvals: 1, timeout: "90s", wantErr: true},
		{name: "too short", approvals: 1, timeout: "5m", wantErr: true},
		{name: "too long", approvals: 1, timeout: "200h", wantErr: true},
		{name: "unknown approvers", approvals: 1, approvers: "viewer", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := main.ApprovalPolicyRequest(tc.approvals, tc.afterStage, tc.timeout, tc.approvers, false)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if req.TimeoutMinutes != tc.wantMins || req.AfterStage < 1 || req.Approvers == "" {
				t.Fatalf("request = %+v", req)
			}
		})
	}
}
