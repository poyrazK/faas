package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowgate"
)

type storedWorkflowLiveGateOptions struct {
	app              string
	baselineName     string
	resolvedBaseline *api.StoredWorkflowLiveBaseline
	storedworkflowgate.Selection
}

func cmdStoredWorkflowsLiveGate(args []string) int {
	o, err := parseStoredWorkflowLiveGate(args)
	if err != nil {
		return printErr("Invalid stored workflow gate options", err)
	}
	client, err := authedClient()
	if err != nil {
		return failStoredWorkflowLiveGate(o, "authentication_required", "Authenticate with a token that can read this app before evaluating its suite evidence.", 2, 401)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runStoredWorkflowLiveGate(ctx, client, o)
}

func parseStoredWorkflowLiveGate(args []string) (storedWorkflowLiveGateOptions, error) {
	var o storedWorkflowLiveGateOptions
	var policyPath string
	// The manifest handles help. Keep malformed flag values out of diagnostics.
	fs := newFlagSet("workflow stored live gate", flag.ContinueOnError, io.Discard)
	fs.StringVar(&policyPath, "policy", "", "versioned required-case policy JSON file")
	fs.StringVar(&o.app, "app", "", "app slug")
	fs.StringVar(&o.Name, "name", "", "stored workflow name")
	fs.StringVar(&o.baselineName, "baseline-name", "", "named baseline; exclusive with --baseline")
	fs.StringVar(&o.BaselineID, "baseline", "", "retained baseline suite UUID")
	fs.StringVar(&o.CandidateID, "candidate", "", "retained candidate suite UUID")
	fs.Int64Var(&o.Expected.Version, "expected-version", 0, "expected candidate saved version")
	fs.Int64Var(&o.Expected.PublishedVersion, "expected-published-version", 0, "expected candidate published revision")
	fs.StringVar(&o.Expected.DeploymentID, "expected-deployment-id", "", "expected candidate deployment UUID")
	if err := fs.Parse(args); err != nil {
		return o, errors.New("could not parse gate flags; use workflow stored live gate --help")
	}
	if fs.NArg() != 0 || !validCLISlug(o.app) {
		return o, errors.New("provide --app with a valid slug and no positional arguments")
	}
	if (o.BaselineID == "") == (o.baselineName == "") || o.baselineName != "" && !api.ValidStoredWorkflowLiveBaselineName(o.baselineName) {
		return o, errors.New("choose exactly one of --baseline UUID or --baseline-name NAME")
	}
	validate := o.Validate
	if o.baselineName != "" {
		validate = o.ValidateCandidate
	}
	if err := validate(); err != nil {
		return o, err
	}
	if policyPath != "" {
		policy, err := readStoredWorkflowGatePolicy(policyPath)
		if err != nil {
			return o, errors.New("could not read a valid coverage policy; use version 1 or 2 with distinct required_cases and a valid version-specific age limit in a regular JSON file")
		}
		o.Coverage = &policy
	}
	if o.BaselineID != "" {
		o.BaselineID = uuid.MustParse(o.BaselineID).String()
	}
	o.CandidateID = uuid.MustParse(o.CandidateID).String()
	o.Expected.DeploymentID = uuid.MustParse(o.Expected.DeploymentID).String()
	return o, nil
}

func runStoredWorkflowLiveGate(ctx context.Context, client *api.Client, o storedWorkflowLiveGateOptions) int {
	if ctx.Err() != nil {
		return storedWorkflowLiveGateRequestError(ctx, o, ctx.Err())
	}
	requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	defer cancel()
	if o.baselineName != "" {
		if err := resolveStoredWorkflowGateBaseline(requestCtx, client, &o); err != nil {
			if errors.Is(err, errStoredWorkflowGateBaselinePolicyMismatch) {
				return failStoredWorkflowLiveGate(o, "baseline_policy_mismatch", "The local policy differs from the named baseline's reviewed promotion policy. Use that policy or explicitly promote the new policy before gating by name.", 7, 409)
			}
			if errors.Is(err, errStoredWorkflowGateBaselineRetired) {
				return failStoredWorkflowLiveGate(o, "baseline_retired", "The named baseline has been permanently retired. Select an active baseline explicitly; its promotion and retirement audit remain available through baseline history.", 4, 410)
			}
			if errors.Is(err, errStoredWorkflowGateBaselineInvalid) {
				return failStoredWorkflowLiveGate(o, "invalid_response", "The named baseline receipt is incomplete or inconsistent. No passing result is available.", 3, 502)
			}
			return storedWorkflowLiveGateRequestError(ctx, o, err)
		}
		if o.BaselineID == o.CandidateID {
			return failStoredWorkflowLiveGate(o, "baseline_matches_candidate", "Choose a candidate suite distinct from the resolved named baseline.", 1, 400)
		}
	}
	view, err := client.CompareStoredWorkflowLiveSuites(requestCtx, o.app, o.Name, o.CandidateID, o.BaselineID)
	if err != nil {
		return storedWorkflowLiveGateRequestError(ctx, o, err)
	}
	if ctx.Err() != nil {
		return storedWorkflowLiveGateRequestError(ctx, o, ctx.Err())
	}
	result, err := storedworkflowgate.Evaluate(o.Selection, view)
	if err != nil {
		return failStoredWorkflowLiveGate(o, "invalid_response", "The comparison did not contain consistent evidence for the requested suites. No passing gate result is available.", 3, 502)
	}
	return renderStoredWorkflowLiveGate(o, &view, result, storedWorkflowLiveGateMessage(result.Outcome))
}

func storedWorkflowLiveGateRequestError(ctx context.Context, o storedWorkflowLiveGateOptions, err error) int {
	if errors.Is(ctx.Err(), context.Canceled) {
		return failStoredWorkflowLiveGate(o, "interrupted", "The read-only gate was interrupted. Rerun with the same suite IDs and expected target.", 130, 499)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failStoredWorkflowLiveGate(o, "request_timed_out", "The comparison read timed out. Rerun with the same suite IDs and expected target.", 3, 504)
	}
	code, status := 3, 503
	var failure *api.APIError
	if errors.As(err, &failure) {
		status = failure.Problem.Status
		code = exitCodeForStatus(status)
	}
	return failStoredWorkflowLiveGate(o, "request_failed", "The comparison could not be read. Resolve access or retained-evidence availability, then rerun with the same suite IDs and expected target.", code, status)
}

func readStoredWorkflowGatePolicy(path string) (storedworkflowgate.CoveragePolicy, error) {
	var empty storedworkflowgate.CoveragePolicy
	f, err := openCustomerFile(path)
	if err != nil {
		return empty, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return empty, err
	}
	if !info.Mode().IsRegular() || info.Size() > api.StoredWorkflowLiveGatePolicyMaxBytes {
		return empty, errors.New("invalid coverage policy file")
	}
	data, err := io.ReadAll(io.LimitReader(f, api.StoredWorkflowLiveGatePolicyMaxBytes+1))
	if err != nil {
		return empty, err
	}
	return storedworkflowgate.ParseCoveragePolicy(data)
}
