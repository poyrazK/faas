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

//go:embed task_recovery_quarantine.mjs
var mcpTaskQuarantineGate string

type taskQuarantineAttempt struct {
	QualificationSHA256 string `json:"qualification_sha256"`
	Token               string `json:"token"`
}
type taskQuarantineProof struct {
	WorkerIDs       []string  `json:"worker_ids"`
	NamespaceSHA256 string    `json:"namespace_sha256"`
	ObservedAt      time.Time `json:"observed_at"`
	Draining        bool      `json:"draining"`
}

func cmdMCPTaskQuarantine(args []string) int {
	flags := newFlagSet("mcp-task-quarantine", flag.ContinueOnError)
	id := flags.String("id", "", "original intent identity")
	op := flags.String("operation-id", "", "original operation identity")
	handover := flags.String("handover-id", "", "settled recovery identity")
	digest := flags.String("qualification-sha256", "", "separate quarantine qualification digest")
	consent := flags.Bool("allow-tool-execution", false, "allow the separately approved candidate quarantine")
	local := flags.Bool("allow-local-checks", false, "allow reviewed local compatibility checks for owner qualification")
	phase := flags.String("phase", "", "private fixed adapter phase")
	if flags.Parse(args[1:]) != nil {
		return 1
	}
	action := args[0]
	owner := action == "quarantine-qualify" || action == "quarantine-approve" || action == "quarantine-revoke"
	if flags.NArg() != 0 || !toolpublication.ValidID(*id) || !toolpublication.ValidID(*op) || !toolpublication.ValidID(*handover) || action != "quarantine" && action != "quarantine-hook" && !owner || action == "quarantine-qualify" && (*digest != "" || !*local) || action != "quarantine-qualify" && (!toolpublication.ValidID(*digest) || *local) || action == "quarantine" && !*consent || action != "quarantine" && *consent || action != "quarantine-hook" && *phase != "" || action == "quarantine-hook" && *phase != "claim" && *phase != "observe" {
		return printErr("Task quarantine", errors.New("require exact native identities, separate owner qualification/approval and appropriate local execution consent"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Task quarantine", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.TaskToolRecoveryObservationTTL)
	defer cancel()
	r, err := c.GetTaskToolReleaseIntent(ctx, *id)
	if err != nil || !tasktoolrelease.ValidRecoveryResult(r) || !tasktoolrelease.ValidQuarantine(r) || r.Execution.OperationID != *op || r.Execution.Recovery.ID != *handover {
		return printErr("Task quarantine", errors.New("a matching settled native restore receipt is required"))
	}
	if owner && action != "quarantine-qualify" {
		return decideMCPTaskQuarantine(ctx, c, r, strings.TrimPrefix(action, "quarantine-"), &api.TaskToolQuarantineRequest{QualificationSHA256: *digest})
	}
	q := r.Execution.Recovery.Quarantine
	if action == "quarantine-qualify" && q != nil {
		return printMCPTaskQuarantineQualification(r)
	}
	if !owner && (!tasktoolrelease.QuarantineIdentity(r, *digest) || q == nil) {
		return printErr("Task quarantine", errors.New("no matching separate quarantine qualification"))
	}
	if !owner && q.Result != nil {
		return jsonOut(writeJSON(r))
	}
	if !owner && q.State == "qualified" && (q.Decision != "approved" || !time.Now().Before(q.Qualification.ExpiresAt)) {
		return printErr("Task quarantine", errors.New("separate current owner quarantine approval required"))
	}
	profile, dir, err := loadMCPTaskExecutionProfile(r)
	if err != nil {
		return printErr("Task quarantine", errors.New("owner binding profile unavailable"))
	}
	release, err := ownMCPJournal(filepath.Join(dir, "release.json"), action == "quarantine-hook")
	if err != nil {
		return printErr("Task quarantine", err)
	}
	defer release()
	restore, err := mcpTaskExecutionEnv(profile)
	if err != nil {
		return printErr("Task quarantine", err)
	}
	defer restore()
	review, err := reviewMCPTaskRelease(profile.Plan)
	if err != nil || review.Fingerprint != r.Intent.ReleaseFingerprint {
		return printErr("Task quarantine", errors.New("reviewed source or dependencies changed"))
	}
	plan, err := readMCPNativePlan(profile.Plan)
	if err != nil {
		return printErr("Task quarantine", err)
	}
	if err = verifyMCPTaskExecutionBindings(ctx, plan, profile); err != nil {
		return printErr("Task quarantine", errors.New("separated Task role bindings changed"))
	}
	if owner {
		return qualifyMCPTaskQuarantine(ctx, c, r, profile, plan)
	}
	if action == "quarantine-hook" {
		if *phase == "claim" {
			return claimMCPTaskQuarantine(ctx, c, r, profile, dir, plan, *digest)
		}
		return observeMCPTaskQuarantine(ctx, c, r, profile, dir, *digest)
	}
	return runMCPTaskQuarantine(ctx, c, r, profile, dir, plan, *digest)
}

func decideMCPTaskQuarantine(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, action string, in *api.TaskToolQuarantineRequest) int {
	out, err := c.DecideTaskToolReleaseIntent(ctx, r.ID, api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: r.Intent.ReleaseFingerprint, Decision: "recovery_quarantine_" + action, Quarantine: in})
	if err != nil {
		return printErr("Task quarantine", err)
	}
	return printMCPTaskQuarantineQualification(out)
}
func printMCPTaskQuarantineQualification(r api.TaskToolReleaseIntentRecord) int {
	if r.Execution == nil || r.Execution.Recovery == nil || r.Execution.Recovery.Quarantine == nil {
		return printErr("Task quarantine", errors.New("native quarantine qualification missing"))
	}
	return jsonOut(writeJSON(struct {
		Record              api.TaskToolReleaseIntentRecord `json:"record"`
		QualificationSHA256 string                          `json:"qualification_sha256"`
		ReadyToApply        bool                            `json:"ready_to_apply"`
	}{r, tasktoolrelease.QualificationDigest(r.Execution.Recovery.Quarantine.Qualification), false}))
}

func taskQuarantinePreflight(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, p mcpNativeReleasePlan) ([]api.TaskToolReleaseApp, *api.MCPAccessPolicyExpectation, map[string][]string, error) {
	receipt := r.Execution.Receipt
	ids, err := mcpNativeWorkerIDs(ctx, c, p.WorkerApp, receipt.WorkerDeploymentID, true)
	want := slices.Clone(receipt.WorkerIDs)
	slices.Sort(ids)
	slices.Sort(want)
	if err != nil || !slices.Equal(ids, want) {
		return nil, nil, nil, errors.New("candidate worker identities or claim-fence attestation changed")
	}
	s := mcpNativeReleaseState{Fingerprint: r.Intent.ReleaseFingerprint, ServingCaptured: receipt.ServingCaptured, ServingDeployment: receipt.ServingDeploymentID, WebDeployment: receipt.WebDeploymentID, WorkerDeployment: receipt.WorkerDeploymentID, WorkerIDs: receipt.WorkerIDs, PreviousDeployments: receipt.PreviousDeployments, Parked: receipt.Parked}
	if err = checkMCPNativePrevious(ctx, c, p, &s); err != nil {
		return nil, nil, nil, errors.New("replacement workers unavailable")
	}
	if len(p.PreviousWorkerApps) == 0 {
		return nil, nil, nil, errors.New("running replacement workers required")
	}
	if err = mcpNativeObserverHealthy(ctx, c, p); err != nil {
		return nil, nil, nil, errors.New("observer evidence unavailable")
	}
	serving, err := mcpServingDeployment(ctx, c, p.WebApp, "")
	if err != nil || serving != receipt.ServingDeploymentID {
		return nil, nil, nil, errors.New("captured restored web is no longer serving")
	}
	var policy *api.MCPAccessPolicyExpectation
	if r.Intent.AccessMode == "open" {
		policy, err = openMCPTaskRestorePolicy(ctx, c, p)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	target := s
	target.WebDeployment = receipt.ServingDeploymentID
	if err = mcpNativeVerifyEndpoint(ctx, c, p, &target); err != nil {
		return nil, nil, nil, errors.New("restored MCP artifact unavailable")
	}
	if policy != nil {
		current, e := openMCPTaskRestorePolicy(ctx, c, p)
		if e != nil || current.Fingerprint != policy.Fingerprint || current.Resource != policy.Resource {
			return nil, nil, nil, errors.New("native policy changed")
		}
		target.AccessPolicyExpectation = policy
	}
	if target.AccessPolicyExpectation == nil {
		return nil, nil, nil, errors.New("native access-policy audit unavailable")
	}
	apps := make([]api.TaskToolReleaseApp, 0, len(r.Intent.Apps))
	instances := map[string][]string{}
	for _, pin := range r.Intent.Apps {
		review, e := c.GetAppReleaseReview(ctx, pin.App)
		if e != nil {
			return nil, nil, nil, e
		}
		pin.Review = review
		apps = append(apps, pin)
		rows, e := c.ListInstances(ctx, pin.App)
		if e != nil {
			return nil, nil, nil, e
		}
		instances[pin.Review.AppID] = []string{}
		for _, row := range rows {
			if row.State == "running" && (pin.Role != "web" || row.DeploymentID == receipt.ServingDeploymentID) {
				instances[pin.Review.AppID] = append(instances[pin.Review.AppID], row.ID)
			}
		}
	}
	return apps, target.AccessPolicyExpectation, instances, nil
}

func qualifyMCPTaskQuarantine(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, profile mcpTaskExecutionProfile, p mcpNativeReleasePlan) int {
	started := time.Now().UTC()
	command := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", mcpTaskRecoveryCompatibility)
	command.Dir = p.WorkerPath
	command.Env = environWithout(mcpTaskExecutionChildEnv(profile, false), "MCP_TASK_MIGRATION_DATABASE_URL")
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != "{\"qualified\":true}" {
		return printErr("Task quarantine", errors.New("retained Task compatibility unavailable"))
	}
	apps, policy, instances, err := taskQuarantinePreflight(ctx, c, r, p)
	if err != nil {
		return printErr("Task quarantine", err)
	}
	evidence, _ := json.Marshal(struct {
		Policy    *api.MCPAccessPolicyExpectation
		Instances map[string][]string
		Bindings  string
	}{policy, instances, profile.digest()})
	q := api.TaskToolRecoveryQualification{HandoverID: r.Execution.Recovery.ID, OperationID: r.Execution.OperationID, Revision: r.Execution.Receipt.Revision, Action: "quarantine", TargetDeploymentID: r.Execution.Receipt.WorkerDeploymentID, ExpectedServingDeploymentID: r.Execution.Receipt.ServingDeploymentID, BindingsSHA256: profile.digest(), EvidenceSHA256: toolpublication.Digest(evidence), NativeApps: apps, ObservedAt: started, ExpiresAt: started.Add(api.TaskToolRecoveryApprovalTTL)}
	return decideMCPTaskQuarantine(ctx, c, r, "qualify", &api.TaskToolQuarantineRequest{Qualification: &q})
}

func claimMCPTaskQuarantine(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, dir string, plan mcpNativeReleasePlan, digest string) int {
	apps, policy, instances, err := taskQuarantinePreflight(ctx, c, r, plan)
	if err != nil {
		return printErr("Task quarantine", err)
	}
	evidence, _ := json.Marshal(struct {
		Apps      []api.TaskToolReleaseApp
		Policy    *api.MCPAccessPolicyExpectation
		Instances map[string][]string
	}{apps, policy, instances})
	token, err := mcpTaskExecutionEntropy()
	if err != nil {
		return printErr("Task quarantine", err)
	}
	attempt := taskQuarantineAttempt{digest, token}
	if err = writeMCPTaskPrivate(filepath.Join(dir, "quarantine-attempt.json"), attempt); err != nil {
		return printErr("Task quarantine", errors.New("an existing attempt requires read-only reconciliation"))
	}
	in := &api.TaskToolQuarantineRequest{Action: "claim", QualificationSHA256: digest, AttemptToken: token, BindingsSHA256: p.digest(), EvidenceSHA256: toolpublication.Digest(evidence), CheckedAt: time.Now().UTC(), ExpectedInstanceIDs: instances, ExpectedMCPAccessPolicy: policy}
	if err = readMCPTaskPrivate(filepath.Join(dir, "claim.json"), &in.ClaimToken); err != nil {
		return printErr("Task quarantine", errors.New("original private claim unavailable"))
	}
	out, err := c.CheckTaskToolReleaseIntent(ctx, r.ID, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: r.Intent.ReleaseFingerprint, Quarantine: in})
	if err != nil || !tasktoolrelease.QuarantineIdentity(out, digest) || !tasktoolrelease.ValidQuarantine(out) || out.Execution.Recovery.Quarantine.State != "applying" || out.Execution.Recovery.Quarantine.AttemptSHA256 != toolpublication.Digest([]byte(token)) {
		return printErr("Task quarantine", errors.New("admission uncertain or refused; inspect the native operation, never repeat the SQL attempt"))
	}
	return jsonOut(writeJSON(map[string]bool{"admitted": true}))
}

func observeMCPTaskQuarantine(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, dir, digest string) int {
	var attempt taskQuarantineAttempt
	if readMCPTaskPrivate(filepath.Join(dir, "quarantine-attempt.json"), &attempt) != nil || attempt.QualificationSHA256 != digest {
		return printErr("Task quarantine", errors.New("private admitted attempt unavailable"))
	}
	var proof taskQuarantineProof
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, int64(api.TaskToolReleaseIntentMaxBytes)+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&proof) != nil || decoder.Decode(new(any)) != io.EOF || !proof.Draining {
		return printErr("Task quarantine", errors.New("candidate claim-fence observation unavailable"))
	}
	want, got := slices.Clone(r.Execution.Receipt.WorkerIDs), slices.Clone(proof.WorkerIDs)
	slices.Sort(want)
	slices.Sort(got)
	if strings.TrimSpace(p.Env[mcpTasksObserverNamespaceEnv]) == "" || !slices.Equal(want, got) || proof.NamespaceSHA256 != toolpublication.Digest([]byte(strings.TrimSpace(p.Env[mcpTasksObserverNamespaceEnv]))) {
		return printErr("Task quarantine", errors.New("foreign candidate fence observation"))
	}
	body, _ := json.Marshal(proof)
	out, err := c.CheckTaskToolReleaseIntent(ctx, r.ID, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: r.Intent.ReleaseFingerprint, Quarantine: &api.TaskToolQuarantineRequest{Action: "observe", QualificationSHA256: digest, AttemptToken: attempt.Token, BindingsSHA256: p.digest(), EvidenceSHA256: toolpublication.Digest(body), CheckedAt: proof.ObservedAt}})
	if err != nil {
		return printErr("Task quarantine", errors.New("fences observed but native receipt uncertain; inspect the same operation"))
	}
	if !tasktoolrelease.ValidQuarantine(out) || !tasktoolrelease.QuarantineIdentity(out, digest) || out.Execution.Recovery.Quarantine.Result == nil {
		return printErr("Task quarantine", errors.New("native fence observation missing"))
	}
	return jsonOut(writeJSON(map[string]bool{"observed": true}))
}

func runMCPTaskQuarantine(ctx context.Context, c *Client, r api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, dir string, plan mcpNativeReleasePlan, digest string) int {
	binary, err := os.Executable()
	if err != nil {
		return printErr("Task quarantine", err)
	}
	q := r.Execution.Recovery.Quarantine
	mutate := q.State == "qualified"
	deadline := time.Now().Add(api.TaskToolRecoveryObservationTTL)
	if mutate {
		deadline = q.Qualification.ExpiresAt
	}
	gate, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	input, _ := json.Marshal(map[string]any{"command": []string{binary, "--json", "mcp", "server", "tasks", "execution", "quarantine-hook", "--id", r.ID, "--operation-id", r.Execution.OperationID, "--handover-id", r.Execution.Recovery.ID, "--qualification-sha256", digest}, "timeoutMs": time.Until(deadline).Milliseconds(), "maxBufferBytes": 2 * api.TaskToolReleaseIntentMaxBytes, "mutate": mutate, "workerIDs": r.Execution.Receipt.WorkerIDs, "workerIDsMax": api.TaskToolReleaseReceiptWorkerIDsMax})
	command := exec.CommandContext(gate, "node", "--input-type=module", "--eval", mcpTaskQuarantineGate, string(input))
	command.Dir = plan.WorkerPath
	command.Env = append(environWithout(mcpTaskExecutionChildEnv(p, true), "FAAS_TOKEN", "FAAS_API", mcpTaskFenceEnv, mcpTaskFenceAPIEnv, "MCP_TASK_MIGRATION_DATABASE_URL"), "FAAS_TOKEN="+c.Token(), "FAAS_API="+p.APIBase)
	_, _ = command.Output()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), api.DefaultClientTimeout)
	defer stop()
	latest, err := c.GetTaskToolReleaseIntent(cleanup, r.ID)
	if err != nil || !tasktoolrelease.QuarantineIdentity(latest, digest) || !tasktoolrelease.ValidQuarantine(latest) {
		return printErr("Task quarantine", errors.New("inspect the durable native operation"))
	}
	if latest.Execution.Recovery.Quarantine.Result == nil {
		return printErr("Task quarantine", errors.New("quarantine remains uncertain; repeats only inspect recorded candidate fences"))
	}
	return jsonOut(writeJSON(latest))
}
