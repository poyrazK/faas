package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

//go:embed task_recovery_qualification.mjs
var mcpTaskRecoveryCompatibility string

func cmdMCPTaskRecoveryOwner(args []string) int {
	action := strings.TrimPrefix(args[0], "recovery-")
	flags := newFlagSet("mcp-task-recovery-owner", flag.ContinueOnError)
	id := flags.String("id", "", "original intent identity")
	op := flags.String("operation-id", "", "original operation identity")
	handover := flags.String("handover-id", "", "one immutable owner-selected recovery identity")
	revision := flags.Uint64("revision", 0, "exact native checkpoint revision")
	digest := flags.String("qualification-sha256", "", "exact qualification digest for approval or revocation")
	local := flags.Bool("allow-local-checks", false, "allow reviewed Node code to check live Task compatibility")
	if flags.Parse(args[1:]) != nil {
		return 1
	}
	if flags.NArg() != 0 || !toolpublication.ValidID(*id) || !toolpublication.ValidID(*op) || !toolpublication.ValidID(*handover) || *revision == 0 ||
		(action != "stop" && action != "qualify" && action != "approve" && action != "revoke") ||
		((action == "approve" || action == "revoke") && !toolpublication.ValidID(*digest)) || ((action == "stop" || action == "qualify") && *digest != "") || (action == "qualify" && !*local) || (action != "qualify" && *local) {
		return printErr("Task recovery", errors.New("require exact intent, operation, handover and revision; qualification needs local-check consent; approve/revoke need the exact qualification digest"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Task recovery", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.TaskToolRecoveryApprovalTTL)
	defer cancel()
	record, err := c.GetTaskToolReleaseIntent(ctx, *id)
	if err != nil {
		return printErr("Task recovery", err)
	}
	request := &api.TaskToolRecoveryRequest{HandoverID: *handover, OperationID: *op, Revision: *revision, QualificationSHA256: *digest}
	if record.Execution == nil || record.Execution.OperationID != *op || record.Execution.Receipt == nil || record.Execution.Receipt.Revision != *revision {
		return printErr("Task recovery", errors.New("native checkpoint changed; inspect before choosing a handover"))
	}
	if action == "qualify" {
		h := record.Execution.Recovery
		if h == nil || h.ID != *handover || h.State != "settled" || h.SettledAt == nil || record.Execution.ExecutorExitedAt == nil {
			return printErr("Task recovery", errors.New("wait for the original executor exit receipt and native write barrier; interrupted executors without an acknowledgement remain blocked"))
		}
		if h.Qualification != nil {
			return jsonOut(writeJSON(record))
		}
		qualification, err := qualifyMCPTaskRecovery(ctx, c, record)
		if err != nil {
			return printErr("Task recovery", errors.New("fresh recovery checks failed; no qualification or approval recorded"))
		}
		request.Qualification = &qualification
	}
	out, err := c.DecideTaskToolReleaseIntent(ctx, *id, api.TaskToolReleaseIntentDecision{IntentSHA256: record.IntentSHA256, ReleaseFingerprint: record.Intent.ReleaseFingerprint, Decision: "recovery_" + action, Recovery: request})
	if err != nil {
		return printErr("Task recovery", err)
	}
	if out.Execution != nil && out.Execution.Recovery != nil && out.Execution.Recovery.Qualification != nil {
		return jsonOut(writeJSON(struct {
			Record              api.TaskToolReleaseIntentRecord `json:"record"`
			QualificationSHA256 string                          `json:"qualification_sha256"`
			ReadyToApply        bool                            `json:"ready_to_apply"`
		}{out, tasktoolrelease.QualificationDigest(*out.Execution.Recovery.Qualification), false}))
	}
	return jsonOut(writeJSON(out))
}

func qualifyMCPTaskRecovery(ctx context.Context, c *Client, record api.TaskToolReleaseIntentRecord) (api.TaskToolRecoveryQualification, error) {
	var q api.TaskToolRecoveryQualification
	p, dir, err := loadMCPTaskExecutionProfile(record)
	if err != nil {
		return q, err
	}
	release, err := ownMCPJournal(filepath.Join(dir, "release.json"), false)
	if err != nil {
		return q, err
	}
	defer release()
	restore, err := mcpTaskExecutionEnv(p)
	if err != nil {
		return q, err
	}
	defer restore()
	review, err := reviewMCPTaskRelease(p.Plan)
	if err != nil || review.Fingerprint != record.Intent.ReleaseFingerprint {
		return q, errors.New("reviewed inputs changed")
	}
	plan, err := readMCPNativePlan(p.Plan)
	if err != nil {
		return q, err
	}
	started := time.Now().UTC()
	if err = verifyMCPTaskExecutionBindings(ctx, plan, p); err != nil {
		return q, err
	}
	command := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", mcpTaskRecoveryCompatibility)
	command.Dir, command.Env = plan.WorkerPath, mcpTaskExecutionChildEnv(p, false)
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != "{\"qualified\":true}" {
		return q, errors.New("Task compatibility unavailable")
	}
	receipt := record.Execution.Receipt
	s := mcpNativeReleaseState{Fingerprint: record.Intent.ReleaseFingerprint, ServingCaptured: receipt.ServingCaptured, ServingDeployment: receipt.ServingDeploymentID,
		WebDeployment: receipt.WebDeploymentID, WorkerDeployment: receipt.WorkerDeploymentID, PreviousDeployments: receipt.PreviousDeployments, WorkerIDs: receipt.WorkerIDs, Parked: receipt.Parked}
	if err = checkMCPNativePrevious(ctx, c, plan, &s); err != nil {
		return q, err
	}
	if err = mcpNativeObserverHealthy(ctx, c, plan); err != nil {
		return q, err
	}
	previous := s
	previous.WebDeployment, previous.ServingDeployment = s.ServingDeployment, s.WebDeployment
	if err = mcpNativeVerifyEndpoint(ctx, c, plan, &previous); err != nil {
		return q, err
	}
	report, err := tasktoolrelease.InspectRecovery(ctx, c, record.ID, true, started)
	if err != nil {
		return q, err
	}
	eligible := false
	for _, action := range report.Actions {
		if action.Action == "restore" && action.Eligibility == "requires_owner_checks" {
			eligible = true
		}
	}
	if !eligible {
		return q, errors.New("native restore observations do not qualify")
	}
	q = api.TaskToolRecoveryQualification{HandoverID: record.Execution.Recovery.ID, OperationID: record.Execution.OperationID, Revision: receipt.Revision, Action: "restore",
		TargetDeploymentID: receipt.ServingDeploymentID, ExpectedServingDeploymentID: receipt.WebDeploymentID, BindingsSHA256: p.digest(), ObservedAt: started, ExpiresAt: started.Add(api.TaskToolRecoveryApprovalTTL)}
	q.NativeApps = make([]api.TaskToolReleaseApp, 0, len(record.Intent.Apps))
	for _, pin := range record.Intent.Apps {
		current, err := c.GetAppReleaseReview(ctx, pin.App)
		if err != nil {
			return q, err
		}
		pin.Review = current
		q.NativeApps = append(q.NativeApps, pin)
	}
	evidence, _ := json.Marshal(struct {
		Report tasktoolrelease.RecoveryReport
		Policy *api.MCPAccessPolicyExpectation
		Inputs string
	}{report, previous.AccessPolicyExpectation, p.digest()})
	q.EvidenceSHA256 = toolpublication.Digest(evidence)
	return q, nil
}
