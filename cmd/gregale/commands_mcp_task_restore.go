package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

//go:embed task_recovery_restore.mjs
var mcpTaskRecoveryRestoreGate string

func cmdMCPTaskRestore(args []string) int {
	flags := newFlagSet("mcp-task-restore", flag.ContinueOnError)
	id := flags.String("id", "", "original intent identity")
	op := flags.String("operation-id", "", "original operation identity")
	handover := flags.String("handover-id", "", "owner-approved recovery identity")
	digest := flags.String("qualification-sha256", "", "exact owner-approved qualification digest")
	consent := flags.Bool("allow-tool-execution", false, "allow the owner-approved one-time web traffic restore")
	if flags.Parse(args[1:]) != nil {
		return 1
	}
	if flags.NArg() != 0 || !toolpublication.ValidID(*id) || !toolpublication.ValidID(*op) || !toolpublication.ValidID(*handover) || !toolpublication.ValidID(*digest) || args[0] == "restore" && !*consent {
		return printErr("Task restore", errors.New("require exact intent, operation, handover, qualification and execution consent"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Task restore", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.TaskToolRecoveryObservationTTL)
	defer cancel()
	record, err := c.GetTaskToolReleaseIntent(ctx, *id)
	if err != nil {
		return printErr("Task restore", err)
	}
	in := api.TaskToolRecoveryRestoreRequest{OperationID: *op, HandoverID: *handover, QualificationSHA256: *digest}
	if !tasktoolrelease.RestoreIdentity(record, in) {
		return printErr("Task restore", errors.New("no matching owner recovery qualification"))
	}
	if record.Execution.Recovery.Result != nil {
		if !tasktoolrelease.ValidRecoveryResult(record) {
			return printErr("Task restore", errors.New("native recovery result identity changed"))
		}
		return jsonOut(writeJSON(record))
	}
	h := record.Execution.Recovery
	if h.Decision != "approved" || h.State != "settled" || !time.Now().Before(h.Qualification.ExpiresAt) {
		return printErr("Task restore", errors.New("a settled handover and unexpired owner recovery approval are required"))
	}
	profile, dir, err := loadMCPTaskExecutionProfile(record)
	if err != nil {
		return printErr("Task restore", errors.New("owner binding profile unavailable or changed"))
	}
	release, err := ownMCPJournal(filepath.Join(dir, "release.json"), args[0] == "restore-hook")
	if err != nil {
		return printErr("Task restore", err)
	}
	defer release()
	restore, err := mcpTaskExecutionEnv(profile)
	if err != nil {
		return printErr("Task restore", err)
	}
	defer restore()
	review, err := reviewMCPTaskRelease(profile.Plan)
	if err != nil || review.Fingerprint != record.Intent.ReleaseFingerprint {
		return printErr("Task restore", errors.New("reviewed source changed"))
	}
	plan, err := readMCPNativePlan(profile.Plan)
	if err != nil {
		return printErr("Task restore", err)
	}
	if args[0] == "restore-hook" {
		return applyMCPTaskRestore(ctx, c, record, profile, dir, plan, in)
	}
	if err = verifyMCPTaskExecutionBindings(ctx, plan, profile); err != nil {
		return printErr("Task restore", errors.New("live Task role bindings changed"))
	}
	binary, err := os.Executable()
	if err != nil {
		return printErr("Task restore", err)
	}
	timeout := time.Until(h.Qualification.ExpiresAt)
	if timeout <= 0 {
		return printErr("Task restore", errors.New("owner recovery approval expired"))
	}
	gate, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	input, _ := json.Marshal(map[string]any{"command": []string{binary, "--json", "mcp", "server", "tasks", "execution", "restore-hook", "--id", record.ID, "--operation-id", in.OperationID, "--handover-id", in.HandoverID, "--qualification-sha256", in.QualificationSHA256}, "timeoutMs": timeout.Milliseconds(), "maxBufferBytes": 2 * api.TaskToolReleaseIntentMaxBytes})
	command := exec.CommandContext(gate, "node", "--input-type=module", "--eval", mcpTaskRecoveryRestoreGate, string(input))
	command.Dir = plan.WorkerPath
	command.Env = append(environWithout(mcpTaskExecutionChildEnv(profile, true), "FAAS_TOKEN", "FAAS_API", mcpTaskFenceEnv, mcpTaskFenceAPIEnv, "MCP_TASK_MIGRATION_DATABASE_URL"), "FAAS_TOKEN="+c.Token(), "FAAS_API="+profile.APIBase)
	_, _ = command.Output()
	// The hook may have committed even if the gate lost its reply. Inspect once;
	// never repeat its native POST, acquire another claim or undo traffic here.
	observed, err := inspectMCPTaskRestore(ctx, c, record.ID, in)
	if err != nil {
		return printErr("Task restore", errors.New("restore outcome requires native operation inspection"))
	}
	return jsonOut(writeJSON(observed))
}

func inspectMCPTaskRestore(ctx context.Context, c *Client, id string, in api.TaskToolRecoveryRestoreRequest) (api.TaskToolReleaseIntentRecord, error) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DefaultClientTimeout)
	defer cancel()
	record, err := c.GetTaskToolReleaseIntent(cleanup, id)
	if err != nil {
		return record, err
	}
	if !tasktoolrelease.RestoreIdentity(record, in) || !tasktoolrelease.ValidRecoveryResult(record) {
		return record, errors.New("restore not committed")
	}
	return record, nil
}

func applyMCPTaskRestore(ctx context.Context, c *Client, record api.TaskToolReleaseIntentRecord, profile mcpTaskExecutionProfile, dir string, plan mcpNativeReleasePlan, in api.TaskToolRecoveryRestoreRequest) int {
	if err := verifyMCPTaskExecutionBindings(ctx, plan, profile); err != nil {
		return printErr("Task restore", errors.New("task role bindings changed"))
	}
	receipt := record.Execution.Receipt
	s := mcpNativeReleaseState{Fingerprint: record.Intent.ReleaseFingerprint, ServingCaptured: receipt.ServingCaptured, ServingDeployment: receipt.ServingDeploymentID, WebDeployment: receipt.WebDeploymentID, WorkerDeployment: receipt.WorkerDeploymentID, PreviousDeployments: receipt.PreviousDeployments, WorkerIDs: receipt.WorkerIDs, Parked: receipt.Parked}
	currentIDs, err := mcpNativeWorkerIDs(ctx, c, plan.WorkerApp, s.WorkerDeployment, true)
	wantIDs := slices.Clone(s.WorkerIDs)
	slices.Sort(wantIDs)
	slices.Sort(currentIDs)
	if err != nil || !slices.Equal(wantIDs, currentIDs) {
		return printErr("Task restore", errors.New("task worker identities changed"))
	}
	if err := mcpNativeCheckWorkers(ctx, c, plan, &s); err != nil {
		return printErr("Task restore", errors.New("candidate Task worker readiness changed"))
	}
	if err := checkMCPNativePrevious(ctx, c, plan, &s); err != nil {
		return printErr("Task restore", errors.New("previous worker readiness changed"))
	}
	if err := mcpNativeObserverHealthy(ctx, c, plan); err != nil {
		return printErr("Task restore", errors.New("observer evidence changed"))
	}
	var openPolicy *api.MCPAccessPolicyExpectation
	if record.Intent.AccessMode == "open" {
		openPolicy, err = openMCPTaskRestorePolicy(ctx, c, plan)
		if err != nil {
			return printErr("Task restore", errors.New("native open-access policy unavailable or changed"))
		}
	}
	previous := s
	previous.WebDeployment, previous.ServingDeployment = s.ServingDeployment, s.WebDeployment
	if err := mcpNativeVerifyEndpoint(ctx, c, plan, &previous); err != nil {
		return printErr("Task restore", errors.New("exact previous MCP artifact is not ready"))
	}
	if openPolicy != nil {
		current, err := openMCPTaskRestorePolicy(ctx, c, plan)
		if err != nil || current.Resource != openPolicy.Resource || current.Fingerprint != openPolicy.Fingerprint {
			return printErr("Task restore", errors.New("native access policy changed during readiness checks"))
		}
		previous.AccessPolicyExpectation = openPolicy
	}
	if previous.AccessPolicyExpectation == nil {
		return printErr("Task restore", errors.New("a native access-policy audit is required"))
	}
	report, err := tasktoolrelease.InspectRecovery(ctx, c, record.ID, true, time.Now())
	if err != nil {
		return printErr("Task restore", errors.New("native recovery evidence unavailable"))
	}
	eligible := false
	for _, action := range report.Actions {
		if action.Action == "restore" && action.Eligibility == "requires_owner_checks" {
			eligible = true
		}
	}
	if !eligible {
		return printErr("Task restore", errors.New("native restore observations changed"))
	}
	if err = readMCPTaskPrivate(filepath.Join(dir, "claim.json"), &in.ClaimToken); err != nil {
		return printErr("Task restore", errors.New("private owner-bound claim unavailable"))
	}
	in.ExpectedInstanceIDs = map[string][]string{}
	for _, pin := range record.Execution.Recovery.Qualification.NativeApps {
		rows, err := c.ListInstances(ctx, pin.App)
		if err != nil {
			return printErr("Task restore", errors.New("native instance snapshot unavailable"))
		}
		in.ExpectedInstanceIDs[pin.Review.AppID] = []string{}
		for _, row := range rows {
			if row.State != "running" {
				continue
			}
			if pin.Role == "web" && row.DeploymentID != receipt.ServingDeploymentID {
				continue
			}
			in.ExpectedInstanceIDs[pin.Review.AppID] = append(in.ExpectedInstanceIDs[pin.Review.AppID], row.ID)
		}
	}
	in.BindingsSHA256, in.ExpectedMCPAccessPolicy = profile.digest(), previous.AccessPolicyExpectation
	evidence, _ := json.Marshal(struct {
		Report        tasktoolrelease.RecoveryReport
		Policy        *api.MCPAccessPolicyExpectation
		Qualification string
		Instances     map[string][]string
	}{report, in.ExpectedMCPAccessPolicy, in.QualificationSHA256, in.ExpectedInstanceIDs})
	in.EvidenceSHA256, in.CheckedAt = toolpublication.Digest(evidence), time.Now().UTC()
	result, err := c.RestoreTaskToolRecovery(ctx, record.ID, in)
	if err != nil {
		result, err = inspectMCPTaskRestore(ctx, c, record.ID, in)
	}
	if err != nil || !tasktoolrelease.RestoreIdentity(result, in) || !tasktoolrelease.ValidRecoveryResult(result) {
		return printErr("Task restore", errors.New("restore outcome is unknown or blocked; inspect the native operation"))
	}
	return jsonOut(writeJSON(result))
}

func openMCPTaskRestorePolicy(ctx context.Context, c *Client, p mcpNativeReleasePlan) (*api.MCPAccessPolicyExpectation, error) {
	cfg, err := mcphosting.Load(p.WebPath)
	if err != nil {
		return nil, err
	}
	app, err := c.GetApp(ctx, p.WebApp)
	if err != nil {
		return nil, err
	}
	resource, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(resource)
	if err != nil {
		return nil, err
	}
	rules, err := c.ListCoveringEdgeRulesForApp(ctx, p.WebApp)
	if err != nil {
		return nil, err
	}
	for _, rule := range rules {
		if rule.Enabled && rule.Kind == "jwt" && api.MCPAccessRuleOverlaps(rule, parsed) {
			return nil, errors.New("open endpoint has a covering JWT policy")
		}
	}
	fingerprint, err := api.MCPAccessPolicyFingerprint(resource, rules, time.Now())
	if err != nil {
		return nil, err
	}
	return &api.MCPAccessPolicyExpectation{Resource: resource, Fingerprint: fingerprint}, nil
}
