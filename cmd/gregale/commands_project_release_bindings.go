package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type bindingPolicyExpectedRevisionKey struct{}

func readCheckedReleaseFile(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("release file must contain exactly one JSON object")
	}
	return nil
}

func cmdProjectsCheckedRelease(args []string) int {
	action := args[0]
	flags, positional := splitArgsForFlags(args[1:], "dry-run", "object-storage-write", "queue-publish", "durable")
	fs := newFlagSet("projects-environments-release-sets-"+action, flag.ContinueOnError)
	durable := fs.Bool("durable", false, "persist a recoverable graph verification run")
	runID := fs.String("run-id", "", "caller-selected graph run UUID")
	progressMode := fs.String("progress", "auto", "durable progress on stderr: auto, plain, ndjson or off")
	statusRetries := fs.Int("status-read-retries", api.BindingVerificationRunnerDefaultStatusReadRetries, "transient durable status GET retries (0–8)")
	concurrency := fs.Int("concurrency", bindingVerificationConcurrencyDefault, "maximum concurrent probes across the graph (1–8)")
	file := fs.String("file", "", "release selection or prepared manifest JSON file")
	dry := fs.Bool("dry-run", false, "preview all member qualification plans")
	queueConsent := fs.Bool("queue-publish", false, "consent to planned safe queue messages")
	consent := fs.Bool("object-storage-write", false, "consent to planned disposable storage canaries")
	timeout := fs.Duration("wait-timeout", bindingProbeWaitTimeoutDefault, "maximum time for the complete graph qualification")
	interval := fs.Duration("poll-interval", executionPollIntervalDefault, "probe polling interval")
	if err := fs.Parse(flags); err != nil || fs.NArg() != 0 || len(positional) != 2 || (*file == "" && action != "prepare-deactivation") || (action == "prepare-deactivation" && *file != "") || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) || *timeout <= 0 || *interval <= 0 || (action != "verify" && (*dry || *consent)) {
		PrintUsage(osStderr, "usage: gregale environment release-sets <prepare|prepare-rollback|prepare-deactivation|verify|activate|rollback|deactivate> <project> <environment> [--file FILE; required except prepare-deactivation] [--dry-run --object-storage-write --wait-timeout DURATION --poll-interval DURATION --json]", "projects environments")
		return 1
	}
	explicitProgress := false
	fs.Visit(func(f *flag.Flag) { explicitProgress = explicitProgress || f.Name == "progress" })
	if !validBindingProgress(*progressMode) || explicitProgress && (!*durable || *dry || action != "verify") {
		return printErr("Invalid progress option", errors.New("--progress auto|plain|ndjson|off requires durable verify execution"))
	}
	explicitConcurrency := false
	fs.Visit(func(f *flag.Flag) { explicitConcurrency = explicitConcurrency || f.Name == "concurrency" })
	explicitRetries := false
	fs.Visit(func(f *flag.Flag) { explicitRetries = explicitRetries || f.Name == "status-read-retries" })
	if *statusRetries < 0 || *statusRetries > api.BindingVerificationRunnerMaxStatusReadRetries || explicitRetries && !*durable {
		return printErr("Invalid status read retries", errors.New("--status-read-retries (0–8) requires --durable"))
	}
	if *concurrency < 1 || *concurrency > bindingVerificationConcurrencyMax || action != "verify" && (explicitConcurrency || *queueConsent) {
		return printErr("Invalid verification options", fmt.Errorf("--concurrency (1–8) and --queue-publish require verify"))
	}
	if *durable && (action != "verify" || *timeout < time.Second) || *runID != "" && !*durable {
		return printErr("Invalid durable verification options", errors.New("durable runs require verify and at least one second"))
	}
	if *runID != "" {
		id, err := uuid.Parse(*runID)
		if err != nil || id == uuid.Nil {
			return printErr("Invalid graph run ID", errors.New("run ID must be a UUID"))
		}
		*runID = id.String()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	interrupted, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(withBindingProgress(withBindingStatusReadRetries(withBindingVerificationBudget(interrupted, *concurrency), statusRetries), *progressMode), *timeout)
	defer cancel()
	if action == "prepare-deactivation" {
		manifest, err := client.PrepareCheckedProjectDeactivation(ctx, positional[0], positional[1])
		if err != nil {
			return printErr("Could not prepare graph deactivation", err)
		}
		return jsonOut(writeJSON(manifest))
	}
	if action == "prepare" || action == "prepare-rollback" {
		if action == "prepare-rollback" {
			var request api.PrepareProjectRollbackRequest
			if err := readCheckedReleaseFile(*file, &request); err != nil {
				return printErr("Invalid rollback selection", err)
			}
			manifest, err := client.PrepareCheckedProjectRollback(ctx, positional[0], positional[1], request)
			if err != nil {
				return printErr("Could not prepare graph rollback", err)
			}
			return jsonOut(writeJSON(manifest))
		}
		var request api.PublishProjectReleaseSetRequest
		if err := readCheckedReleaseFile(*file, &request); err != nil {
			return printErr("Invalid release selection", err)
		}
		manifest, err := client.PrepareCheckedProjectRelease(ctx, positional[0], positional[1], request)
		if err != nil {
			return printErr("Could not prepare release graph", err)
		}
		return jsonOut(writeJSON(manifest))
	}
	var manifest api.CheckedProjectReleaseManifest
	if err := readCheckedReleaseFile(*file, &manifest); err != nil {
		return printErr("Invalid prepared release manifest", err)
	}
	if manifest.Project != positional[0] || manifest.Environment != positional[1] || manifest.ExpectedActiveReleaseID == nil || len(manifest.Deployments) == 0 {
		return printErr("Invalid prepared release manifest", fmt.Errorf("manifest must match the selected project and environment and include expected routing"))
	}
	if action == "activate" || action == "rollback" || action == "deactivate" {
		if (action == "deactivate") != manifest.Deactivate || (action == "rollback") != (manifest.RollbackReleaseID != "") || (manifest.Deactivate && manifest.RollbackReleaseID != "") {
			return printErr("Invalid release action", fmt.Errorf("manifest intent must match activate, rollback or deactivate"))
		}
		var response api.CheckedProjectReleaseResponse

		switch action {
		case "deactivate":
			response, err = client.DeactivateCheckedProjectRelease(ctx, positional[0], positional[1], manifest)
		case "rollback":
			response, err = client.RollbackCheckedProjectRelease(ctx, positional[0], positional[1], manifest)
		default:
			response, err = client.ActivateCheckedProjectRelease(ctx, positional[0], positional[1], manifest)
		}
		if err != nil {
			return printErr("Release graph change blocked", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(response))
		}
		if action == "deactivate" {
			PrintOK(osStdout, "Deactivated release %s with checked fallback routing", response.Release.ID)
			return 0
		}
		if action == "rollback" {
			PrintOK(osStdout, "Rolled back to release %s from historical graph %s", response.Release.ID, manifest.RollbackReleaseID)
			return 0
		}
		PrintOK(osStdout, "Activated release %s for %s/%s with checked binding evidence", response.Release.ID, manifest.Project, manifest.Environment)
		return 0
	}
	if *dry {
		return previewProjectVerification(ctx, client, manifest, *consent, *queueConsent)
	}
	if *durable && !*dry {
		return createDurableProjectVerification(ctx, client, manifest, *runID, *consent, *queueConsent, *interval, *timeout)
	}
	apps := make([]string, 0, len(manifest.Deployments))
	for app := range manifest.Deployments {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	reports := map[string]bindingPolicyVerificationReport{}
	snapshots := map[string]*bindingPolicySnapshotClient{}
	// Review every member before admitting any canary for the graph.
	for _, app := range apps {
		memberCtx := context.WithValue(context.WithValue(ctx, queueCanaryConsentKey{}, *queueConsent), bindingPolicyExpectedRevisionKey{}, manifest.ExpectedPolicyRevisions[app])
		snapshot, err := prepareBindingPolicyVerification(memberCtx, client, app, manifest.Deployments[app], manifest.ServiceTargets[app])
		if err != nil {
			return printErr("Could not prepare member verification", err)
		}
		snapshots[app] = snapshot
		memberCtx = context.WithValue(memberCtx, bindingVerificationSnapshotKey{}, snapshot)
		report, code := executeBindingPolicyVerification(memberCtx, client, app, manifest.Deployments[app], true, *consent, *interval, *timeout, manifest.ServiceTargets[app])
		reports[app] = report
		if code != 0 {
			return renderCheckedReleaseVerification(manifest, reports, *dry, code)
		}
		if !*dry && report.QueuePublishConsentRequired && !*queueConsent {
			return printErr("Queue publication consent required", fmt.Errorf("review --dry-run, then supply --queue-publish"))
		}
		if !*dry && report.WriteConsentRequired && !*consent {
			return printErr("Storage write consent required", fmt.Errorf("review graph verification with --dry-run, then supply --object-storage-write"))
		}
	}
	if *dry {
		return renderCheckedReleaseVerification(manifest, reports, true, 0)
	}
	results := make([]bindingPolicyVerificationReport, len(apps))
	codes := make([]int, len(apps))
	runBindingVerificationJobs(ctx, len(apps), func(jobCtx context.Context, index int) error {
		app := apps[index]
		memberCtx := context.WithValue(context.WithValue(jobCtx, queueCanaryConsentKey{}, *queueConsent), bindingPolicyExpectedRevisionKey{}, manifest.ExpectedPolicyRevisions[app])
		memberCtx = context.WithValue(memberCtx, bindingVerificationSnapshotKey{}, snapshots[app])
		results[index], codes[index] = executeBindingPolicyVerification(memberCtx, client, app, manifest.Deployments[app], false, *consent, *interval, *timeout, manifest.ServiceTargets[app])
		if codes[index] != 0 {
			return errors.New("graph member verification failed")
		}
		return nil
	})
	failedCode := 0
	for index, app := range apps {
		if results[index].App != "" {
			reports[app] = results[index]
		}
		if codes[index] != 0 && (failedCode == 0 || failedCode == 130) {
			failedCode = codes[index]
		}
	}
	if failedCode != 0 {
		return renderCheckedReleaseVerification(manifest, reports, false, failedCode)
	}
	// Earlier members can expire while later members finish. Recheck every
	// original snapshot and its evidence after all probes have joined.
	for _, app := range apps {
		report, code := finalizeBindingPolicyVerification(ctx, snapshots[app], reports[app], *interval)
		reports[app] = report
		if code != 0 {
			return renderCheckedReleaseVerification(manifest, reports, false, code)
		}
	}
	return renderCheckedReleaseVerification(manifest, reports, false, 0)
}

func renderCheckedReleaseVerification(manifest api.CheckedProjectReleaseManifest, reports map[string]bindingPolicyVerificationReport, dry bool, code int, reviews ...api.ProjectBindingVerificationReview) int {
	if jsonOutput {
		var digest string
		var digests map[string]string
		if len(reviews) > 0 {
			digest = reviews[0].ReviewDigest
			digests = reviews[0].ReviewDigests
		}
		returnCode := jsonOut(writeJSON(struct {
			Project       string                                     `json:"project"`
			Environment   string                                     `json:"environment"`
			DryRun        bool                                       `json:"dry_run"`
			Passed        bool                                       `json:"passed"`
			Members       map[string]bindingPolicyVerificationReport `json:"members"`
			ReviewDigest  string                                     `json:"review_digest,omitempty"`
			ReviewDigests map[string]string                          `json:"review_digests,omitempty"`
		}{manifest.Project, manifest.Environment, dry, !dry && code == 0, reports, digest, digests}))
		if returnCode != 0 {
			return returnCode
		}
		return code
	}
	apps := make([]string, 0, len(reports))
	for app := range reports {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	for _, app := range apps {
		renderBindingPolicyVerification(reports[app])
	}
	return code
}
