package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// resourceConditions is the status and wait verbs of a kind read by one
// reference (ADR-20261030151810): a managed database, a custom domain or a
// cron. App and release conditions keep their own commands for their flags.
type resourceConditions struct {
	kind string
	// noun and usage are the tree spellings `gregale <noun> status|wait` and
	// the reference they take.
	noun, ref string
	// resolve turns the reference into the one the status route takes; nil
	// passes it through.
	resolve func(context.Context, *Client, string) (string, error)
	read    func(*Client, context.Context, string) (api.ResourceStatus, error)
	// fallback is the existing command a 404 points to.
	fallback string
}

var (
	postgresConditions = resourceConditions{
		kind: api.ResourceKindDatabase, noun: "postgres", ref: "<name|id>",
		resolve: func(ctx context.Context, client *Client, ref string) (string, error) {
			return postgresDatabaseID(ctx, client, ref)
		},
		read:     (*Client).GetManagedPostgresDatabaseStatus,
		fallback: "gregale postgres get",
	}
	domainConditions = resourceConditions{
		kind: api.ResourceKindDomain, noun: "domain", ref: "<domain>",
		read:     (*Client).GetDomainStatus,
		fallback: "gregale domain get",
	}
	cronConditions = resourceConditions{
		kind: api.ResourceKindCron, noun: "cron", ref: "<id>",
		resolve: func(_ context.Context, _ *Client, ref string) (string, error) {
			if !cronIDPattern.MatchString(ref) {
				return "", fmt.Errorf("%q is not a cron id", ref)
			}
			return ref, nil
		},
		read:     (*Client).GetCronStatus,
		fallback: "gregale cron get",
	}
)

// status prints the resource's conditions.
func (rc resourceConditions) status(args []string) int {
	fs := newFlagSet(rc.noun+" status", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, fmt.Sprintf("usage: gregale %s status %s", rc.noun, rc.ref), rc.noun)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ref, err := rc.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not resolve "+rc.kind, err)
	}
	status, err := rc.read(client, ctx, ref)
	if err != nil {
		return printErr("Could not read conditions", rc.statusError(ref, err))
	}
	return renderResourceStatus(status)
}

// wait polls the resource's conditions with the exit codes of `app wait`.
func (rc resourceConditions) wait(args []string) int {
	fs := newFlagSet(rc.noun+" wait", flag.ContinueOnError)
	forFlag := fs.String("for", api.ConditionReady, "condition to wait for: TYPE or TYPE=True|False|Unknown")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval (1s to 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, fmt.Sprintf("usage: gregale %s wait %s [--for CONDITION[=True|False|Unknown]] [--timeout DURATION]", rc.noun, rc.ref), rc.noun)
		return 1
	}
	want, err := parseConditionFor(rc.kind, *forFlag)
	if err != nil {
		return printErr("Invalid wait options", err)
	}
	if *timeout <= 0 || *interval < time.Second || *interval > time.Hour {
		return printErr("Invalid wait options", fmt.Errorf("--timeout must be positive and --poll-interval between 1s and 1h"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	ref, err := rc.resolveRef(ctx, client, fs.Arg(0))
	cancel()
	if err != nil {
		return printErr("Could not resolve "+rc.kind, err)
	}
	read := func(ctx context.Context) (api.ResourceStatus, error) {
		status, err := rc.read(client, ctx, ref)
		return status, rc.statusError(ref, err)
	}
	return waitForCondition(read, want, *timeout, *interval)
}

func (rc resourceConditions) resolveRef(ctx context.Context, client *Client, ref string) (string, error) {
	if rc.resolve == nil {
		return ref, nil
	}
	return rc.resolve(ctx, client, ref)
}

// statusError explains a 404 as appStatusError does.
func (rc resourceConditions) statusError(ref string, err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
		return &exitErr{code: exitCodeForStatus(http.StatusNotFound), msg: fmt.Sprintf("no conditions for %s %s: it does not exist, or conditions are not available to this account yet; `%s %s` shows it instead", rc.kind, ref, rc.fallback, ref)}
	}
	return err
}
