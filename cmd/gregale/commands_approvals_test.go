package main_test

import (
	"strings"
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
		notify     string
		wantMins   int
		wantNotify int
		wantErr    bool
	}{
		{name: "defaults", approvals: 2, wantMins: 1440},
		{name: "four hours, developers", approvals: 1, timeout: "4h", approvers: "developer", wantMins: 240},
		{name: "stage two", approvals: 1, afterStage: 2, timeout: "30m", wantMins: 30},
		{name: "notify two channels", approvals: 1, notify: "releases, leads", wantMins: 1440, wantNotify: 2},
		{name: "notify a channel twice", approvals: 1, notify: "releases,releases", wantErr: true},
		{name: "no approvals", approvals: 0, wantErr: true},
		{name: "too many approvals", approvals: 6, wantErr: true},
		{name: "seconds", approvals: 1, timeout: "90s", wantErr: true},
		{name: "too short", approvals: 1, timeout: "5m", wantErr: true},
		{name: "too long", approvals: 1, timeout: "200h", wantErr: true},
		{name: "unknown approvers", approvals: 1, approvers: "viewer", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := main.ApprovalPolicyRequest(tc.approvals, tc.afterStage, tc.timeout, tc.approvers, false, tc.notify)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if req.TimeoutMinutes != tc.wantMins || req.AfterStage < 1 || req.Approvers == "" || len(req.NotifyChannels) != tc.wantNotify {
				t.Fatalf("request = %+v", req)
			}
		})
	}
}

// adr: 20261020085706 amendment 6 — a policy without --notify would let
// releases wait unannounced, so the CLI refuses it before calling apid.
func TestApprovalRequireNeedsNotify(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stderr, restore := main.CaptureStderr(t)
	defer restore()
	if code := main.CmdApprovals([]string{"require", "--app", "shop", "--approvals", "1"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--notify is required") {
		t.Fatalf("stderr:\n%s", stderr.String())
	}
}
