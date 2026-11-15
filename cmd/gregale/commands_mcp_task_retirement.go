package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

//go:embed task_recovery_retirement.mjs
var mcpTaskRetirementGate string

type taskRetirementProof struct {
	WorkerIDs            []string  `json:"worker_ids"`
	NamespaceSHA256      string    `json:"namespace_sha256"`
	ObservedAt           time.Time `json:"observed_at"`
	Draining             bool      `json:"draining"`
	RegistrationsStopped bool      `json:"registrations_stopped"`
}

func cmdMCPTaskRetirement(args []string) int {
	flags := newFlagSet("mcp-task-retirement", flag.ContinueOnError)
	id := flags.String("id", "", "original intent identity")
	op := flags.String("operation-id", "", "original operation identity")
	handover := flags.String("handover-id", "", "settled recovery identity")
	digest := flags.String("qualification-sha256", "", "separate retirement qualification digest")
	consent := flags.Bool("allow-tool-execution", false, "allow separately approved candidate retirement")
	local := flags.Bool("allow-local-checks", false, "allow reviewed local checks for qualification")
	phase := flags.String("phase", "", "private fixed adapter phase")
	if flags.Parse(args[1:]) != nil {
		return 1
	}
	action := args[0]
	owner := action == "retire-qualify" || action == "retire-approve" || action == "retire-revoke"
	if flags.NArg() != 0 || !toolpublication.ValidID(*id) || !toolpublication.ValidID(*op) || !toolpublication.ValidID(*handover) || action != "retire" && action != "retire-hook" && !owner || action == "retire-qualify" && (*digest != "" || !*local) || action != "retire-qualify" && !(action == "retire-hook" && *phase == "qualify") && (!toolpublication.ValidID(*digest) || *local) || action == "retire" && !*consent || action != "retire" && *consent || action != "retire-hook" && *phase != "" || action == "retire-hook" && *phase != "qualify" && *phase != "park" && *phase != "observe" {
		return printErr("Task retirement", errors.New("require exact native identities, separate owner approval and appropriate local execution consent"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Task retirement", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.TaskToolRecoveryObservationTTL)
	defer cancel()
	r, err := c.GetTaskToolReleaseIntent(ctx, *id)
	if err != nil || !tasktoolrelease.ValidRecoveryResult(r) || !tasktoolrelease.ValidQuarantine(r) || !tasktoolrelease.ValidRetirement(r) || r.Execution.Recovery.Quarantine == nil || r.Execution.Recovery.Quarantine.Result == nil || r.Execution.OperationID != *op || r.Execution.Recovery.ID != *handover {
		return printErr("Task retirement", errors.New("matching settled restore and quarantine receipts required"))
	}
	if owner && action != "retire-qualify" {
		return decideMCPTaskRetirement(ctx, c, r, strings.TrimPrefix(action, "retire-"), &api.TaskToolRetirementRequest{QualificationSHA256: *digest})
	}
	q := r.Execution.Recovery.Retirement
	if action == "retire-qualify" && q != nil {
		return printMCPTaskRetirementQualification(r)
	}
	qualifying := action == "retire-qualify" || action == "retire-hook" && *phase == "qualify"
	if !qualifying && (!tasktoolrelease.RetirementIdentity(r, *digest) || q == nil) {
		return printErr("Task retirement", errors.New("no matching separate retirement qualification"))
	}
	if !qualifying && q.Result != nil {
		return jsonOut(writeJSON(r))
	}
	if !qualifying && q.State == "qualified" && (q.Decision != "approved" || !time.Now().Before(q.Qualification.ExpiresAt)) {
		return printErr("Task retirement", errors.New("separate current owner retirement approval required"))
	}
	p, dir, err := loadMCPTaskExecutionProfile(r)
	if err != nil {
		return printErr("Task retirement", errors.New("owner binding profile unavailable"))
	}
	release, err := ownMCPJournal(filepath.Join(dir, "release.json"), action == "retire-hook")
	if err != nil {
		return printErr("Task retirement", err)
	}
	defer release()
	restore, err := mcpTaskExecutionEnv(p)
	if err != nil {
		return printErr("Task retirement", err)
	}
	defer restore()
	review, err := reviewMCPTaskRelease(p.Plan)
	if err != nil || review.Fingerprint != r.Intent.ReleaseFingerprint {
		return printErr("Task retirement", errors.New("reviewed source or dependencies changed"))
	}
	plan, err := readMCPNativePlan(p.Plan)
	if err != nil {
		return printErr("Task retirement", err)
	}
	if verifyMCPTaskExecutionBindings(ctx, plan, p) != nil {
		return printErr("Task retirement", errors.New("separated Task role bindings changed"))
	}
	if action == "retire-hook" {
		return hookMCPTaskRetirement(ctx, c, r, p, dir, plan, *digest, *phase)
	}
	phaseName := "observe"
	if qualifying {
		phaseName = "qualify"
	} else if q.State == "qualified" {
		phaseName = "park"
	}
	return runMCPTaskRetirement(ctx, c, r, p, plan, *digest, phaseName)
}
func decideMCPTaskRetirement(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, action string, in *api.TaskToolRetirementRequest) int {
	out, err := c.DecideTaskToolReleaseIntent(ctx, r.ID, api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: r.Intent.ReleaseFingerprint, Decision: "recovery_retire_" + action, Retirement: in})
	if err != nil {
		return printErr("Task retirement", err)
	}
	return printMCPTaskRetirementQualification(out)
}
func printMCPTaskRetirementQualification(r api.TaskToolReleaseIntentRecord) int {
	if r.Execution == nil || r.Execution.Recovery == nil || r.Execution.Recovery.Retirement == nil {
		return printErr("Task retirement", errors.New("native retirement qualification missing"))
	}
	return jsonOut(writeJSON(struct {
		Record              api.TaskToolReleaseIntentRecord `json:"record"`
		QualificationSHA256 string                          `json:"qualification_sha256"`
		ReadyToApply        bool                            `json:"ready_to_apply"`
	}{r, tasktoolrelease.QualificationDigest(r.Execution.Recovery.Retirement.Qualification), false}))
}
func readMCPTaskRetirementProof(p mcpTaskExecutionProfile, r api.TaskToolReleaseIntentRecord, stopped bool) (taskRetirementProof, error) {
	var proof taskRetirementProof
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, int64(api.TaskToolReleaseIntentMaxBytes)+1))
	decoder.DisallowUnknownFields()
	want := slices.Clone(r.Execution.Receipt.WorkerIDs)
	if decoder.Decode(&proof) != nil || decoder.Decode(new(any)) != io.EOF {
		return proof, errors.New("candidate registration observation unavailable")
	}
	got := slices.Clone(proof.WorkerIDs)
	slices.Sort(want)
	slices.Sort(got)
	namespace := strings.TrimSpace(p.Env[mcpTasksObserverNamespaceEnv])
	if namespace == "" || !slices.Equal(want, got) || proof.NamespaceSHA256 != toolpublication.Digest([]byte(namespace)) || stopped && !proof.RegistrationsStopped || !stopped && !proof.Draining {
		return proof, errors.New("foreign or incomplete candidate registration observation")
	}
	return proof, nil
}
func hookMCPTaskRetirement(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, dir string, plan mcpNativeReleasePlan, digest, phase string) int {
	proof, err := readMCPTaskRetirementProof(p, r, phase == "observe")
	if err != nil {
		return printErr("Task retirement", err)
	}
	if phase == "observe" {
		return observeMCPTaskRetirement(ctx, c, r, p, dir, digest, proof)
	}
	if err = requireMCPConditionalParking(ctx, c); err != nil {
		return printErr("Task retirement", err)
	}
	apps, policy, instances, err := taskQuarantinePreflight(ctx, c, r, plan)
	if err != nil {
		return printErr("Task retirement", err)
	}
	app, err := c.GetApp(ctx, plan.WorkerApp)
	if err != nil || !isWorkerApp(app) {
		return printErr("Task retirement", errors.New("candidate must be a native worker app"))
	}
	rows, err := c.ListInstances(ctx, plan.WorkerApp)
	if err != nil {
		return printErr("Task retirement", err)
	}
	ids, budget, err := mcpNativeWorkerReceipts(ctx, c, plan.WorkerApp, r.Execution.Receipt.WorkerDeploymentID, rows, true, true)
	slices.Sort(ids)
	want := slices.Clone(r.Execution.Receipt.WorkerIDs)
	slices.Sort(want)
	if err != nil || !slices.Equal(ids, want) || app.Manifest.StopGracePeriod <= budget || budget <= 0 {
		return printErr("Task retirement", errors.New("worker generation or native stop grace does not cover its reported Task shutdown deadline"))
	}
	evidence, _ := json.Marshal(struct {
		Proof      taskRetirementProof
		Apps       []api.TaskToolReleaseApp
		Policy     *api.MCPAccessPolicyExpectation
		Instances  map[string][]string
		Bindings   string
		ShutdownMS int64
	}{proof, apps, policy, instances, p.digest(), budget.Milliseconds()})
	if phase == "qualify" {
		q := api.TaskToolRecoveryQualification{HandoverID: r.Execution.Recovery.ID, OperationID: r.Execution.OperationID, Revision: r.Execution.Receipt.Revision, Action: "retire", TargetDeploymentID: r.Execution.Receipt.WorkerDeploymentID, ExpectedServingDeploymentID: r.Execution.Receipt.ServingDeploymentID, BindingsSHA256: p.digest(), EvidenceSHA256: toolpublication.Digest(evidence), NativeApps: apps, ObservedAt: proof.ObservedAt, ExpiresAt: proof.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL)}
		out, err := c.DecideTaskToolReleaseIntent(ctx, r.ID, api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: r.Intent.ReleaseFingerprint, Decision: "recovery_retire_qualify", Retirement: &api.TaskToolRetirementRequest{Qualification: &q}})
		if err != nil || out.Execution == nil || out.Execution.Recovery == nil || out.Execution.Recovery.Retirement == nil || !tasktoolrelease.ValidRetirement(out) {
			return printErr("Task retirement", errors.New("native retirement qualification refused"))
		}
		return jsonOut(writeJSON(map[string]bool{"qualified": true}))
	}
	in := api.TaskToolRetirementRequest{Action: "park", QualificationSHA256: digest, BindingsSHA256: p.digest(), EvidenceSHA256: toolpublication.Digest(evidence), CheckedAt: proof.ObservedAt, ExpectedInstanceIDs: instances, ExpectedMCPAccessPolicy: policy, ShutdownTimeoutMS: budget.Milliseconds()}
	if readMCPTaskPrivate(filepath.Join(dir, "claim.json"), &in.ClaimToken) != nil {
		return printErr("Task retirement", errors.New("original private claim unavailable"))
	}
	// Exactly one submission. A lost reply is reconciled from the native journal.
	_, _ = c.ParkTaskToolRetirement(ctx, r.ID, in)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DefaultClientTimeout)
	defer cancel()
	out, err := c.GetTaskToolReleaseIntent(cleanup, r.ID)
	if err != nil || !tasktoolrelease.ValidRetirement(out) || !tasktoolrelease.RetirementIdentity(out, digest) || out.Execution.Recovery.Retirement.State == "qualified" {
		return printErr("Task retirement", errors.New("parking uncertain or refused; inspect the same native operation"))
	}
	return jsonOut(writeJSON(map[string]bool{"parked": true}))
}
func observeMCPTaskRetirement(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, dir, digest string, proof taskRetirementProof) int {
	app, err := c.GetApp(ctx, func() string {
		for _, pin := range r.Intent.Apps {
			if pin.Role == "worker" {
				return pin.App
			}
		}
		return ""
	}())
	if err != nil || app.Status != "evicted_cold" {
		return printErr("Task retirement", errors.New("native worker parking not observed"))
	}
	body, _ := json.Marshal(proof)
	in := &api.TaskToolRetirementRequest{Action: "observe", QualificationSHA256: digest, BindingsSHA256: p.digest(), EvidenceSHA256: toolpublication.Digest(body), CheckedAt: proof.ObservedAt}
	if readMCPTaskPrivate(filepath.Join(dir, "claim.json"), &in.ClaimToken) != nil {
		return printErr("Task retirement", errors.New("original private claim unavailable"))
	}
	out, err := c.CheckTaskToolReleaseIntent(ctx, r.ID, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: r.Intent.ReleaseFingerprint, Retirement: in})
	if err != nil || !tasktoolrelease.RetirementIdentity(out, digest) || !tasktoolrelease.ValidRetirement(out) || out.Execution.Recovery.Retirement.Result == nil {
		return printErr("Task retirement", errors.New("native drain observation pending"))
	}
	return jsonOut(writeJSON(map[string]bool{"observed": true}))
}
func runMCPTaskRetirement(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, plan mcpNativeReleasePlan, digest, phase string) int {
	binary, err := os.Executable()
	if err != nil {
		return printErr("Task retirement", err)
	}
	argv := []string{binary, "--json", "mcp", "server", "tasks", "execution", "retire-hook", "--id", r.ID, "--operation-id", r.Execution.OperationID, "--handover-id", r.Execution.Recovery.ID}
	if phase != "qualify" {
		argv = append(argv, "--qualification-sha256", digest)
	}
	deadline := time.Now().Add(api.TaskToolRecoveryObservationTTL)
	input, _ := json.Marshal(map[string]any{"command": argv, "timeoutMs": time.Until(deadline).Milliseconds(), "maxBufferBytes": 2 * api.TaskToolReleaseIntentMaxBytes, "phase": phase, "workerIDs": r.Execution.Receipt.WorkerIDs, "workerIDsMax": api.TaskToolReleaseReceiptWorkerIDsMax})
	command := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", mcpTaskRetirementGate, string(input))
	command.Dir = plan.WorkerPath
	command.Env = append(environWithout(mcpTaskExecutionChildEnv(p, true), "FAAS_TOKEN", "FAAS_API", mcpTaskFenceEnv, mcpTaskFenceAPIEnv, "MCP_TASK_MIGRATION_DATABASE_URL"), "FAAS_TOKEN="+c.Token(), "FAAS_API="+p.APIBase)
	_, _ = command.Output()
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DefaultClientTimeout)
	defer cancel()
	latest, err := c.GetTaskToolReleaseIntent(cleanup, r.ID)
	if err != nil || !tasktoolrelease.ValidRetirement(latest) || latest.Execution == nil || latest.Execution.Recovery == nil || latest.Execution.Recovery.Retirement == nil {
		return printErr("Task retirement", errors.New("inspect the durable native operation"))
	}
	if phase == "qualify" {
		return printMCPTaskRetirementQualification(latest)
	}
	if !tasktoolrelease.RetirementIdentity(latest, digest) || latest.Execution.Recovery.Retirement.Result == nil {
		return printErr("Task retirement", errors.New("retirement remains pending; repeats only observe the recorded park transition and registration expiry"))
	}
	return jsonOut(writeJSON(latest))
}
