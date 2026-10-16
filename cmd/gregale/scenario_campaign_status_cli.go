package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

func testCampaignStatusFileStamp(path string) string {
	var raw json.RawMessage
	if err := readTestCampaignJSON(path, &raw); err != nil {
		if os.IsNotExist(err) {
			return "absent"
		}
		return "unreadable"
	}
	return testCampaignJSONHash(raw)
}

func protectTestCampaignStatusOutputs(outputs, inputs, directories []string) error {
	paths := []string{}
	for _, output := range outputs {
		if output == "" {
			continue
		}
		absolute, err := filepath.Abs(output)
		if err != nil {
			return err
		}
		for _, directory := range directories {
			if directory == "" {
				continue
			}
			root, err := filepath.Abs(directory)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, absolute)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return errors.New("status exports must be outside campaign, recovery and parent directories")
			}
		}
		info, err := os.Lstat(absolute)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("status output must not be a symlink")
		}
		for _, other := range append(append([]string{}, inputs...), paths...) {
			if other == "" {
				continue
			}
			otherAbs, err := filepath.Abs(other)
			if err != nil {
				return err
			}
			if absolute == otherAbs {
				return errors.New("status output aliases another input or output")
			}
			if info != nil {
				source, err := os.Stat(otherAbs)
				if err == nil && os.SameFile(info, source) {
					return errors.New("status output aliases another input or output")
				}
			}
		}
		paths = append(paths, absolute)
	}
	if stdout, err := os.Stdout.Stat(); err == nil && stdout.Mode().IsRegular() {
		for _, path := range append(append([]string{}, inputs...), paths...) {
			if path == "" {
				continue
			}
			if info, err := os.Stat(path); err == nil && os.SameFile(stdout, info) {
				return errors.New("stdout must not alias a status input or export")
			}
		}
	}
	return nil
}

func cmdTestCampaignStatus(args []string) int {
	fs := newFlagSet("test policy status", flag.ContinueOnError)
	handoff := fs.String("handoff", "", "portable import index for offline parent resolution")
	planPath := fs.String("plan", "", "saved cost-aware campaign plan")
	policyPath := fs.String("policy", "", "policy used by the plan")
	manifestPath := fs.String("manifest", "gregale-test.yaml", "manifest used by the plan")
	directory := fs.String("directory", "", "optional campaign checkpoint directory")
	verifyLive := fs.Bool("verify-live", false, "opt in to bounded read-only API verification of journaled resources; requires --directory")
	side := fs.String("side", "", "optional baseline/current scope; defaults to checkpoint side or all planned sides")
	bundle := fs.String("recovery-bundle", "", "optional reconciliation bundle bound to this campaign")
	from := fs.String("from", "", "original current report used to plan")
	baseline := fs.String("baseline", "", "original baseline report used to plan")
	jsonPath := fs.String("json", "", "optional JSON export outside campaign directories")
	htmlPath := fs.String("html", "", "optional standalone offline HTML export")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *planPath == "" || *policyPath == "" ||
		(*side != "" && *side != "baseline" && *side != "current") || ((*bundle != "" || *verifyLive) && *directory == "") {
		return printErr("Invalid status options", errors.New("--plan and --policy are required; side is baseline/current; a recovery bundle or --verify-live needs --directory"))
	}
	resolver, handoffErr := loadTestCampaignBundleResolver(*handoff)
	if handoffErr != nil {
		return printErr("Invalid handoff import", handoffErr)
	}
	if *directory != "" {
		absolute, err := filepath.Abs(*directory)
		if err != nil {
			return printErr("Invalid campaign directory", err)
		}
		*directory = absolute
	}
	if *bundle != "" {
		absolute, err := filepath.Abs(*bundle)
		if err != nil {
			return printErr("Invalid recovery bundle", err)
		}
		*bundle = absolute
	}
	inputs := []string{*planPath, *policyPath, *manifestPath, *from, *baseline}
	directories := []string{*directory, *bundle}
	if resolver != nil {
		directories = append(directories, resolver.Index.Root)
		inputs = append(inputs, resolver.IndexPath)
	}
	if err := protectTestCampaignStatusOutputs([]string{*jsonPath, *htmlPath}, inputs, directories); err != nil {
		return printErr("Invalid status output", err)
	}
	var plan testCampaignPolicyPlan
	if err := readTestCampaignJSON(*planPath, &plan); err != nil {
		return printErr("Invalid status plan", err)
	}
	policy, err := readTestCampaignPolicy(*policyPath)
	if err != nil {
		return printErr("Invalid status policy", err)
	}
	manifest, _, err := readTestManifestDocument(*manifestPath, func(_ string, s testScenario) []string { return testHTTPDataFields(s) })
	if err != nil {
		return printErr("Invalid status manifest", err)
	}
	// A blocked recovery plan is still inspectable. Execution restrictions
	// remain enforced by run; this command never schedules anything.
	check := plan
	check.Recovery = nil
	if err := validateTestCampaignRunnable(check, manifest.Scenarios, policy); err != nil {
		return printErr("Status binding changed", err)
	}
	var before, after []testRunReceipt
	for _, input := range []struct {
		path, hash string
		receipts   *[]testRunReceipt
	}{
		{*from, plan.Execution.CurrentSHA256, &after}, {*baseline, plan.Execution.BaselineSHA256, &before},
	} {
		if (input.path == "") != (input.hash == "") {
			return printErr("Status input changed", errors.New("supply exactly the original reports used by the plan"))
		}
		if input.path != "" {
			var hash string
			*input.receipts, hash, err = readTestBaselineReport(input.path, nil)
			if err != nil || hash != input.hash {
				return printErr("Status input changed", errors.New("original report hash differs or is unreadable"))
			}
		}
	}
	var checkpoint *testCampaignCheckpoint
	checkpointStamp, sealStamp := "absent", "absent"
	if *directory != "" {
		path := filepath.Join(*directory, "checkpoint.json")
		checkpointStamp = testCampaignStatusFileStamp(path)
		sealStamp = testCampaignStatusFileStamp(filepath.Join(*directory, testCampaignRecoverySealName))
		if checkpointStamp != "absent" {
			checkpoint = &testCampaignCheckpoint{}
			if err := readTestCampaignJSON(path, checkpoint); err != nil {
				return printErr("Invalid status checkpoint", err)
			}
			if *side != "" && *side != checkpoint.Side {
				return printErr("Status side differs", errors.New("scope must match the checkpoint side"))
			}
		}
	}
	report, err := buildTestCampaignStatus(plan, checkpoint, *directory, *side, policy, manifest.Scenarios, before, after, resolver)
	if err != nil {
		return printErr("Could not inspect campaign status", err)
	}
	if *bundle != "" {
		if checkpoint == nil {
			return printErr("Recovery checkpoint missing", errors.New("a recovery bundle requires its original checkpoint"))
		}
		if err := applyTestCampaignStatusRecovery(&report, plan, *checkpoint, *bundle, policy, manifest.Scenarios, resolver); err != nil {
			return printErr("Invalid status recovery bundle", err)
		}
	}

	var watcherSnapshot *testCampaignStatusWatchSnapshot
	if *directory != "" {
		snapshot := inspectTestCampaignStatusWatcher(testCampaignWatchPaths{Plan: *planPath, Policy: *policyPath, Manifest: *manifestPath, Directory: *directory, Handoff: *handoff}, plan, checkpoint, manifest.Scenarios, resolver)
		watcherSnapshot = &snapshot
		report.Watcher = snapshot.Watcher
		inputs = append(inputs, snapshot.Paths...)
	}
	for _, parent := range report.Lineage {
		directories = append(directories, parent.Directory)
		inputs = append(inputs, filepath.Join(parent.Directory, "checkpoint.json"), filepath.Join(parent.Directory, testCampaignRecoverySealName))
	}
	if *directory != "" {
		inputs = append(inputs, filepath.Join(*directory, "checkpoint.json"), filepath.Join(*directory, testCampaignRecoverySealName),
			filepath.Join(*directory, "policy-result.json"), filepath.Join(*directory, "combined-current.json"), filepath.Join(*directory, "combined-baseline.json"))
		for _, batch := range plan.Batches {
			for _, suffix := range []string{".json", ".log"} {
				inputs = append(inputs, filepath.Join(*directory, "batch-"+batch.ID+suffix))
			}
		}
	}
	if *bundle != "" {
		inputs = append(inputs, filepath.Join(*bundle, "reconciliation.json"), filepath.Join(*bundle, "continuation-plan.json"), filepath.Join(*bundle, "current.json"), filepath.Join(*bundle, "baseline.json"), filepath.Join(*bundle, "policy-result.json"))
		for i := range plan.Batches {
			inputs = append(inputs, filepath.Join(*bundle, fmt.Sprintf("recovered-batch-%d.json", i+1)))
		}
	}
	if err := protectTestCampaignStatusOutputs([]string{*jsonPath, *htmlPath}, inputs, directories); err != nil {
		return printErr("Invalid status output", err)
	}
	if *verifyLive {
		ctx, cancel := context.WithTimeout(context.Background(), testCampaignStatusLiveBudget)
		applyTestCampaignStatusLive(ctx, &report, testCampaignWatchPaths{Plan: *planPath, Policy: *policyPath, Manifest: *manifestPath, Directory: *directory, Handoff: *handoff}, plan, checkpoint, manifest.Scenarios, resolver)
		cancel()
	}
	if *directory != "" && (testCampaignStatusFileStamp(filepath.Join(*directory, "checkpoint.json")) != checkpointStamp ||
		testCampaignStatusFileStamp(filepath.Join(*directory, testCampaignRecoverySealName)) != sealStamp) {
		report.SnapshotStable = false
		report.Issues = append(report.Issues, testCampaignStatusIssue{Code: "snapshot_changed", Message: "Checkpoint or seal changed while this report was read.", Action: "Rerun status for a fresh snapshot before relying on the displayed progress."})
	}
	if watcherSnapshot != nil {
		applyTestCampaignStatusWatcherSnapshot(&report, *watcherSnapshot)
	}
	if !report.SnapshotStable {
		invalidateTestCampaignStatusLive(&report)
	}
	if *htmlPath != "" {
		body, err := renderTestCampaignStatusHTML(report)
		if err != nil {
			return printErr("Could not render status HTML", err)
		}
		if err := writeTestMergedReport(*htmlPath, body); err != nil {
			return printErr("Could not write status HTML", err)
		}
	}
	if *jsonPath != "" {
		if err := writeTestCampaignJSON(*jsonPath, report); err != nil {
			return printErr("Could not write status JSON", err)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return printErr("Could not write status", err)
	}
	return 0
}

func renderTestCampaignStatusHTML(report testCampaignStatusReport) ([]byte, error) {
	t, err := template.New("campaign-status").Parse(testCampaignStatusHTML)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := t.Execute(&output, report); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

const testCampaignStatusHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'">
<title>Gregale campaign status</title><style>
body{font:15px/1.5 system-ui,sans-serif;margin:2rem auto;padding:0 1rem;max-width:1400px;color:#18232d;background:#f5f7fa}
section{background:white;padding:1rem 1.3rem;margin:1rem 0;border:1px solid #d5dee6;border-radius:8px}
table{border-collapse:collapse;width:100%;font-size:14px}th,td{text-align:left;vertical-align:top;border-bottom:1px solid #dde4eb;padding:.6rem}
th{background:#eef3f7}code{overflow-wrap:anywhere;font-size:.85em}.scroll{overflow:auto}.muted{color:#526471}.count{display:inline-block;margin-right:1.3rem}h1,h2{line-height:1.2}
</style></head><body>
<h1>Gregale campaign status</h1>
<p class="muted">Read-only snapshot at {{.ObservedAt}} · side {{.Side}} · stable snapshot: {{.SnapshotStable}}</p>
<p>Policy accepted: <strong>{{.PolicyPassed}}</strong> · violations: {{.PolicyViolations}} · lock present: {{.LockPresent}} · halted: {{.Halted}} · sealed: {{.Sealed}}</p>
<p><code>{{.PlanSHA256}}</code></p>
{{with .LiveVerification}}<section><h2>Live resource verification</h2>
<p>State: <strong>{{.State}}</strong> · all resources verified: {{.AllVerified}} · observed clean: {{.ResourcesClean}} · stable local evidence: {{.SnapshotStable}}</p>
<p>{{.StartedAt}} — {{.FinishedAt}}</p>
{{with .Issue}}<p>{{.Resource}} / {{.Operation}}: {{.Reason}}</p>{{end}}
<div class="scroll"><table><thead><tr><th>Run / resource</th><th>Current state / checked</th><th>Verified at</th><th>Saved state / change</th><th>Diagnostic</th></tr></thead><tbody>
{{range .Resources}}<tr><td>{{.RunID}}<br>{{.Kind}} {{.Name}}</td><td>{{.State}}<br>{{with .CheckedAt}}{{.}}{{end}}</td><td>{{with .VerifiedAt}}{{.}}{{else}}Unverified{{end}}</td><td>{{.PreviousState}} / {{.Drift}}</td><td>{{with .Issue}}{{.Reason}} · {{.Attempts}} attempts · retryable {{.Retryable}}{{end}}</td></tr>{{else}}<tr><td colspan="5">No verified resource inventory is available.</td></tr>{{end}}
</tbody></table></div><ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
{{with .Watcher}}<section><h2>Cleanup watcher</h2>
<p>State: <strong>{{.State}}</strong> · owner: {{.Owner.Status}} (PID {{.Owner.PID}}) · kernel guard: {{.Guard}}{{if .GuardPID}} (PID {{.GuardPID}}){{end}}</p>
<p>Local evidence verified: {{.EvidenceVerified}} · resume binding verified: {{.BindingVerified}} · ready to resume: {{.ResumeReady}} · stable snapshot: {{.SnapshotStable}}</p>
<p>Cleanup attempts: {{.Attempts}} · previous owners: {{.PreviousOwners}} · recorded cleanup complete: {{.RecordedComplete}}</p>
<p>{{.NextAction}}{{if .Command}}<br><code>{{.Command}}</code>{{end}}</p>
{{range .Blockers}}<p><strong>{{.Code}}</strong>: {{.Message}}<br>{{.Action}}</p>{{end}}
{{with .Progress}}<h3>Last saved cleanup observation</h3><p>{{if .RecordedAt}}{{.RecordedAt}} · {{end}}<code>{{.Name}}</code> · recorded complete: {{.RecordedComplete}}</p>
{{with .Issue}}<p>{{.Resource}} / {{.Operation}}: {{.Reason}} · attempts {{.Attempts}} · retryable {{.Retryable}}</p>{{end}}
<table><thead><tr><th>Run / workload</th><th>Resource</th><th>Last observed state</th></tr></thead><tbody>
{{range .Faults}}{{$run := .RunID}}<tr><td>{{$run}}</td><td>Registration</td><td>{{.Registration}}</td></tr>{{range $name,$state := .Consumers}}<tr><td>{{$run}}</td><td>Consumer {{$name}}</td><td>{{$state}}</td></tr>{{end}}{{end}}
{{range .Sessions}}<tr><td>{{.RunID}} / {{.Project}}</td><td>Session</td><td>{{.Status}}</td></tr>{{end}}
</tbody></table>{{end}}
<ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
<section><h2>Batch progress</h2>{{range $state,$count := .Counts}}<span class="count"><strong>{{$count}}</strong> {{$state}}</span>{{end}}
<div class="scroll"><table><thead><tr><th>Batch</th><th>Context</th><th>Status / evidence</th><th>Verified runs / cleanup</th><th>Next action</th></tr></thead><tbody>
{{range .Batches}}<tr><td>{{.ID}}<br>{{.Side}}, repeat {{.Repetitions}}</td><td>{{.Scenario}} / {{.Profile}}</td><td>{{.Status}}<br><small>{{.Evidence}}</small></td><td>{{.ExecutedRuns}} / {{.Cleanup}}</td><td>{{.Action}}</td></tr>{{else}}<tr><td colspan="5">No batches in this scope.</td></tr>{{end}}
</tbody></table></div></section>
<section><h2>Estimated allocation</h2><p class="muted">Conservative workload estimates; these are not measured billing amounts.</p>
<table><thead><tr><th></th><th>Leaf runs</th><th>Deployments</th><th>Workload minutes</th></tr></thead><tbody>
<tr><th>Allocated</th><td>{{.Allocation.Runs}}</td><td>{{.Allocation.Deployments}}</td><td>{{.Allocation.WorkloadMinutes}}</td></tr>
<tr><th>Reserved</th><td>{{.Reserved.Runs}}</td><td>{{.Reserved.Deployments}}</td><td>{{.Reserved.WorkloadMinutes}}</td></tr>
<tr><th>Remaining</th><td>{{.Remaining.Runs}}</td><td>{{.Remaining.Deployments}}</td><td>{{.Remaining.WorkloadMinutes}}</td></tr>
<tr><th>Plan limits (0 = unlimited)</th><td>{{.PlanBudget.Runs}}</td><td>{{.PlanBudget.Deployments}}</td><td>{{.PlanBudget.WorkloadMinutes}}</td></tr>
</tbody></table><p>Recovery plans enforce exhausted zero allocations as hard limits.</p></section>
<section><h2>Coverage gaps</h2><div class="scroll"><table><thead><tr><th>Requirement / context</th><th>Baseline remaining</th><th>Current remaining</th><th>Next action</th></tr></thead><tbody>
{{range .CoverageGaps}}<tr><td>{{.Name}} · {{.Context.Scenario}} / {{.Context.Profile}} / {{.Context.Case}}<br><code>{{.Configuration}}</code></td><td>{{.BaselineRemaining}}</td><td>{{.CurrentRemaining}}</td><td>{{.Action}}</td></tr>{{else}}<tr><td colspan="4">No explicit coverage gaps in the verified evidence.</td></tr>{{end}}
</tbody></table></div></section>
<section><h2>Environment gaps</h2>{{range .EnvironmentGaps}}<p><strong>{{.Name}}</strong> · {{.Context.Scenario}} / {{.Context.Profile}}: {{.Reason}}<br>Next action: {{.Action}}</p>{{else}}<p>No explicit environment gaps in the verified evidence.</p>{{end}}</section>
<section><h2>Recovery history</h2>{{if .RecoveryBundle}}<p>Bundle: <code>{{.RecoveryBundle}}</code><br>Continuation: <code>{{.ContinuationSHA256}}</code></p>{{end}}<p>Earlier history fully traced: {{.LineageComplete}}</p>
{{range .Lineage}}<p>Verified: {{.Verified}} · {{.Side}} / {{.Revision}}<br><code>{{.Directory}}</code><br>Plan <code>{{.PlanSHA256}}</code><br>Checkpoint <code>{{.CheckpointSHA256}}</code><br>Parent runs: {{.Allocation.Runs}} allocated / {{.Reserved.Runs}} reserved / {{.Remaining.Runs}} remaining.<br>Parent deployments: {{.Allocation.Deployments}} allocated / {{.Reserved.Deployments}} reserved / {{.Remaining.Deployments}} remaining.<br>Parent workload minutes: {{.Allocation.WorkloadMinutes}} allocated / {{.Reserved.WorkloadMinutes}} reserved / {{.Remaining.WorkloadMinutes}} remaining.</p>{{else}}<p>No parent recovery links in this plan.</p>{{end}}</section>
<section><h2>Policy blockers</h2>{{range .PolicyBlocks}}<p><strong>{{.Code}}</strong>: {{.Message}}<br>{{.Action}}</p>{{else}}<p>No policy violations in the verified evidence subset.</p>{{end}}</section>
<section><h2>Diagnostics and next actions</h2>{{range .Issues}}<p><strong>{{.Code}}</strong>: {{.Message}}<br>{{.Action}}</p>{{else}}<p>No additional diagnostics.</p>{{end}}</section>
<section><h2>Reading this report</h2><ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul></section>
</body></html>`
