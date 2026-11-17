package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type storedWorkflowLiveOptions struct {
	verb, app, name, run, key string
	start                     api.StartStoredWorkflowLiveVerificationRequest
	wait                      bool
	timeout, interval         time.Duration
}

type storedWorkflowLiveResult struct {
	SchemaVersion int                                 `json:"schema_version"`
	App           string                              `json:"app"`
	Name          string                              `json:"name"`
	Outcome       string                              `json:"outcome"`
	ExitCode      int                                 `json:"exit_code"`
	RunID         string                              `json:"run_id,omitempty"`
	Message       string                              `json:"message"`
	Verification  *api.StoredWorkflowLiveVerification `json:"verification,omitempty"`
	Reconnect     string                              `json:"reconnect,omitempty"`
}

func cmdStoredWorkflowsLive(args []string) int {
	if len(args) > 0 && args[0] == "baseline" {
		return cmdStoredWorkflowsLiveBaseline(args[1:])
	}
	if len(args) > 0 && args[0] == "gate" {
		return cmdStoredWorkflowsLiveGate(args[1:])
	}
	if len(args) > 0 && args[0] == "suite" {
		return cmdStoredWorkflowsLiveSuite(args[1:])
	}
	o, err := parseStoredWorkflowLiveOptions(args)
	if err != nil {
		return printErr("Invalid live verification options", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.wait {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	return runStoredWorkflowLive(ctx, client, o)
}

func parseStoredWorkflowLiveOptions(args []string) (storedWorkflowLiveOptions, error) {
	var o storedWorkflowLiveOptions
	if len(args) == 0 {
		return o, errors.New("usage: gregale workflow stored live <start|list|show|check|wait|suite|gate|baseline> --app SLUG --name NAME")
	}
	o.verb = args[0]
	if o.verb != "start" && o.verb != "list" && o.verb != "show" && o.verb != "check" && o.verb != "wait" {
		return o, errors.New("choose live start, list, show, check, wait, suite, gate or baseline")
	}
	fs := newFlagSet("workflow stored live "+o.verb, flag.ContinueOnError)
	fs.StringVar(&o.app, "app", "", "app slug")
	fs.StringVar(&o.name, "name", "", "stored workflow name")
	if o.verb == "start" {
		fs.StringVar(&o.start.CaseName, "case", "", "saved case with live expectations")
		fs.Int64Var(&o.start.ExpectedVersion, "expected-version", 0, "current saved stored workflow version")
		fs.Int64Var(&o.start.ExpectedPublishedVersion, "expected-published-version", 0, "published stored workflow revision")
		fs.StringVar(&o.start.ExpectedDeploymentID, "expected-deployment-id", "", "live default deployment UUID")
		fs.StringVar(&o.key, "idempotency-key", "", "stable key; reuse with the original start flags after interruption")
		fs.BoolVar(&o.start.ConfirmRealActions, "confirm-real-actions", false, "confirm this case can perform real actions")
		fs.BoolVar(&o.wait, "wait", false, "wait for execution and check live assertions")
	} else if o.verb != "list" {
		fs.StringVar(&o.run, "run", "", "existing live verification run UUID")
	}
	if o.verb == "start" || o.verb == "wait" {
		fs.DurationVar(&o.timeout, "timeout", api.StoredWorkflowLiveCLIWaitDefault, "maximum wait (up to 1h; requires waiting)")
		fs.DurationVar(&o.interval, "poll-interval", api.StoredWorkflowLiveProgressPollInterval, "poll interval (1s to 1m; requires waiting)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return o, errors.New("could not parse live verification flags; use --help for this command")
	}
	if fs.NArg() != 0 || !storedWorkflowLiveName(o.app, api.StoredWorkflowNameMaxBytes) || !storedWorkflowLiveName(o.name, api.StoredWorkflowNameMaxBytes) {
		return o, errors.New("provide --app and --name with no positional arguments")
	}
	o.wait = o.wait || o.verb == "wait"
	if err := validateStoredWorkflowLiveOptions(o, fs); err != nil {
		return o, err
	}
	return o, nil
}

func validateStoredWorkflowLiveOptions(o storedWorkflowLiveOptions, fs *flag.FlagSet) error {
	if o.verb == "start" {
		if !o.start.ConfirmRealActions || o.start.ExpectedVersion <= 0 || o.start.ExpectedPublishedVersion <= 0 || !storedWorkflowLiveUUID(o.start.ExpectedDeploymentID) || !storedWorkflowLiveName(o.start.CaseName, api.StoredWorkflowTestCaseNameMaxBytes) || strings.TrimSpace(o.key) == "" {
			return errors.New("start requires --case, --expected-version, --expected-published-version, --expected-deployment-id, --idempotency-key and --confirm-real-actions")
		}
		if err := validateDeployIdempotencyKey(o.key); err != nil || strings.TrimSpace(o.key) != o.key {
			return errors.New("invalid idempotency key; use a nonempty printable key without surrounding whitespace")
		}
	} else if o.verb != "list" && !storedWorkflowLiveUUID(o.run) {
		return errors.New("provide --run with an existing live verification UUID")
	}
	var waitFlags bool
	fs.Visit(func(f *flag.Flag) { waitFlags = waitFlags || f.Name == "timeout" || f.Name == "poll-interval" })
	if waitFlags && !o.wait {
		return errors.New("--timeout and --poll-interval require --wait")
	}
	if o.wait && (o.timeout <= 0 || o.timeout > api.StoredWorkflowLiveCLIWaitMax || o.interval < api.StoredWorkflowLiveCLIPollMin || o.interval > api.StoredWorkflowLiveCLIPollMax) {
		return errors.New("waiting requires a positive timeout up to 1h and a poll interval between 1s and 1m")
	}
	return nil
}

func storedWorkflowLiveUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func storedWorkflowLiveName(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func renderStoredWorkflowLive(o storedWorkflowLiveOptions, view *api.StoredWorkflowLiveVerification, outcome, message string, code int) int {
	result := storedWorkflowLiveResult{SchemaVersion: 1, App: o.app, Name: o.name, Outcome: outcome, ExitCode: code, Message: message, Verification: view}
	result.RunID = o.run
	if view != nil {
		result.RunID = view.RunID
	}
	if result.RunID != "" {
		result.Reconnect = recoveryCLI() + " workflow stored live wait --app " + recoveryArgument(o.app) + " --name " + recoveryArgument(o.name) + " --run " + recoveryArgument(result.RunID)
	}
	var err error
	if jsonOutput {
		err = writeJSON(result)
	} else {
		_, err = fmt.Fprintf(osStdout, "%s\n", message)
		if err == nil && view != nil {
			_, err = fmt.Fprintf(osStdout, "Run: %s\nCase: %s\nState: %s\nPublished revision: %d\nDeployment: %s\nReconnect: %s\n", view.RunID, view.CaseName, view.RunStatus, view.PublishedVersion, view.DeploymentID, result.Reconnect)
		} else if err == nil && result.RunID != "" {
			_, err = fmt.Fprintf(osStdout, "Run: %s\nReconnect: %s\n", result.RunID, result.Reconnect)
		}
		if err == nil && view != nil && view.Report != nil {
			for _, step := range view.Report.Steps {
				if _, err = fmt.Fprintf(osStdout, "  %s: passed=%t mismatches=%s\n", step.Step, step.Passed, strings.Join(step.Mismatches, ",")); err != nil {
					break
				}
			}
		}
	}
	if err != nil {
		return printErr("Could not write live verification result", err)
	}
	return code
}
