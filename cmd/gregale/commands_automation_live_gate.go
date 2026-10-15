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
	"github.com/onebox-faas/faas/pkg/automationgate"
)

type automationLiveGateOptions struct {
	app string
	automationgate.Selection
}

func cmdAutomationsLiveGate(args []string) int {
	o, err := parseAutomationLiveGate(args)
	if err != nil {
		return printErr("Invalid automation gate options", err)
	}
	client, err := authedClient()
	if err != nil {
		return failAutomationLiveGate(o, "authentication_required", "Authenticate with a token that can read this app before evaluating its suite evidence.", 2, 401)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runAutomationLiveGate(ctx, client, o)
}

func parseAutomationLiveGate(args []string) (automationLiveGateOptions, error) {
	var o automationLiveGateOptions
	// The manifest handles help. Keep malformed flag values out of diagnostics.
	fs := newFlagSet("automations live gate", flag.ContinueOnError, io.Discard)
	fs.StringVar(&o.app, "app", "", "app slug")
	fs.StringVar(&o.Name, "name", "", "automation name")
	fs.StringVar(&o.BaselineID, "baseline", "", "retained baseline suite UUID")
	fs.StringVar(&o.CandidateID, "candidate", "", "retained candidate suite UUID")
	fs.Int64Var(&o.Expected.Version, "expected-version", 0, "expected candidate saved version")
	fs.Int64Var(&o.Expected.PublishedVersion, "expected-published-version", 0, "expected candidate published revision")
	fs.StringVar(&o.Expected.DeploymentID, "expected-deployment-id", "", "expected candidate deployment UUID")
	if err := fs.Parse(args); err != nil {
		return o, errors.New("could not parse gate flags; use automations live gate --help")
	}
	if fs.NArg() != 0 || !validCLISlug(o.app) {
		return o, errors.New("provide --app with a valid slug and no positional arguments")
	}
	if err := o.Validate(); err != nil {
		return o, err
	}
	o.BaselineID = uuid.MustParse(o.BaselineID).String()
	o.CandidateID = uuid.MustParse(o.CandidateID).String()
	o.Expected.DeploymentID = uuid.MustParse(o.Expected.DeploymentID).String()
	return o, nil
}

func runAutomationLiveGate(ctx context.Context, client *api.Client, o automationLiveGateOptions) int {
	if ctx.Err() != nil {
		return automationLiveGateRequestError(ctx, o, ctx.Err())
	}
	requestCtx, cancel := context.WithTimeout(ctx, api.AutomationLiveProgressRequestTimeout)
	defer cancel()
	view, err := client.CompareAutomationLiveSuites(requestCtx, o.app, o.Name, o.CandidateID, o.BaselineID)
	if err != nil {
		return automationLiveGateRequestError(ctx, o, err)
	}
	if ctx.Err() != nil {
		return automationLiveGateRequestError(ctx, o, ctx.Err())
	}
	result, err := automationgate.Evaluate(o.Selection, view)
	if err != nil {
		return failAutomationLiveGate(o, "invalid_response", "The comparison did not contain consistent evidence for the requested suites. No passing gate result is available.", 3, 502)
	}
	return renderAutomationLiveGate(o, &view, result, automationLiveGateMessage(result.Outcome))
}

func automationLiveGateRequestError(ctx context.Context, o automationLiveGateOptions, err error) int {
	if errors.Is(ctx.Err(), context.Canceled) {
		return failAutomationLiveGate(o, "interrupted", "The read-only gate was interrupted. Rerun with the same suite IDs and expected target.", 130, 499)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failAutomationLiveGate(o, "request_timed_out", "The comparison read timed out. Rerun with the same suite IDs and expected target.", 3, 504)
	}
	code, status := 3, 503
	var failure *api.APIError
	if errors.As(err, &failure) {
		status = failure.Problem.Status
		code = exitCodeForStatus(status)
	}
	return failAutomationLiveGate(o, "request_failed", "The comparison could not be read. Resolve access or retained-evidence availability, then rerun with the same suite IDs and expected target.", code, status)
}
