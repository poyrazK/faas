package main

// commands_labels.go — ADR-20261031100025: app labels and label selectors.
// `app label list|set|unset` read and write an app's labels; -l/--selector
// narrows `app list`, `domain list` and `app wait`. Labels are a preview
// behind the resource-labels capability.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
)

const subLabel = "label"

// addSelectorFlag registers --selector and its -l shorthand on fs.
func addSelectorFlag(fs *flag.FlagSet) *string {
	selector := fs.String("selector", "", "only resources whose labels match, e.g. team=billing,tier in (web,api)")
	fs.StringVar(selector, "l", "", "shorthand for --selector")
	return selector
}

// hasSelectorFlag reports whether args carry -l or --selector.
func hasSelectorFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if strings.HasPrefix(arg, "-") && (name == "l" || name == "selector") {
			return true
		}
	}
	return false
}

// selectedApps lists the apps selector picks. The server filters; the CLI
// matches the returned labels again, because an apid without labels ignores
// the selector and returns every app.
func selectedApps(ctx context.Context, client *Client, selector string) ([]api.AppResponse, error) {
	sel, err := labels.Parse(selector)
	if err != nil {
		return nil, fmt.Errorf("--selector: %w", err)
	}
	apps, err := client.ListAppsSelected(ctx, sel.String())
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(apps, func(a api.AppResponse) bool { return !sel.Matches(a.Labels) }), nil
}

// cmdAppLabel runs `app <slug> label list|set|unset`.
func cmdAppLabel(slug string, args []string) int {
	usage := func() int {
		PrintUsage(osStderr, "usage: gregale app label list <app> | set <app> KEY=VALUE... | unset <app> KEY...", "apps")
		return 1
	}
	if len(args) == 0 {
		return usage()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch args[0] {
	case subList:
		if len(args) != 1 {
			return usage()
		}
		app, err := client.GetApp(ctx, slug)
		if err != nil {
			return printErr("Could not read app", err)
		}
		return renderLabels(slug, app.Labels)
	case "set", "unset":
		change, err := parseLabelChange(args[0], args[1:])
		if err != nil {
			return printErr("Invalid labels", err)
		}
		out, err := client.UpdateAppLabels(ctx, slug, change)
		if err != nil {
			return printErr("Could not change labels", labelsError(slug, err))
		}
		return renderLabels(slug, out.Labels)
	}
	return usage()
}

// parseLabelChange reads KEY=VALUE arguments for set and KEY arguments for
// unset, and checks them as the server will.
func parseLabelChange(verb string, args []string) (api.LabelChange, error) {
	if len(args) == 0 {
		return api.LabelChange{}, fmt.Errorf("name at least one label")
	}
	var change labels.Change
	if verb == "unset" {
		change.Remove = args
	} else {
		change.Set = make(map[string]string, len(args))
		for _, arg := range args {
			key, value, ok := strings.Cut(arg, "=")
			if !ok {
				return api.LabelChange{}, fmt.Errorf("%q: set takes KEY=VALUE (an empty value is KEY=)", arg)
			}
			change.Set[key] = value
		}
	}
	if err := change.Validate(); err != nil {
		return api.LabelChange{}, err
	}
	return api.LabelChange(change), nil
}

// labelsError explains a 404, keeping the not-found exit code: the app may
// be missing, or labels are not available to the account or server yet.
func labelsError(slug string, err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
		return &exitErr{code: exitCodeForStatus(http.StatusNotFound), msg: fmt.Sprintf("no labels for app %s: the app does not exist, or labels (a preview) are not available to this account yet", slug)}
	}
	return err
}

func renderLabels(slug string, l map[string]string) int {
	if jsonOutput {
		return jsonOut(writeJSON(api.LabelsResponse{Labels: l}))
	}
	if len(l) == 0 {
		_, _ = fmt.Fprintf(osStdout, "App %s has no labels.\n", slug)
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tVALUE")
	for _, key := range slices.Sorted(maps.Keys(l)) {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", key, l[key])
	}
	_ = tw.Flush()
	return 0
}

// cmdAppWaitSelected runs `app wait -l SELECTOR`: it reads the matching apps
// once and waits on each in turn within one timeout. It exits 0 when the
// condition holds on every app, 1 when one is terminal without it, 3 on
// timeout and 4 when the selector picks no app.
func cmdAppWaitSelected(args []string) int {
	fs := newFlagSet("app wait", flag.ContinueOnError)
	selector := addSelectorFlag(fs)
	forFlag := fs.String("for", api.ConditionReady, "condition to wait for: TYPE or TYPE=True|False|Unknown")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait for every app")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval (1s to 1h)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	want, err := parseConditionFor(api.ResourceKindApp, *forFlag)
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
	listCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	apps, err := selectedApps(listCtx, client, *selector)
	cancel()
	if err != nil {
		return printErr("Could not list apps", err)
	}
	if len(apps) == 0 {
		return printErr("Nothing to wait for", &exitErr{code: exitCodeForStatus(http.StatusNotFound), msg: fmt.Sprintf("no app matches %q", *selector)})
	}
	deadline := time.Now().Add(*timeout)
	for _, app := range apps {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			_ = printErr("Wait timed out", fmt.Errorf("%s did not hold on every app within %s", want, *timeout))
			return exitWaitTimeout
		}
		read := func(ctx context.Context) (api.ResourceStatus, error) {
			status, err := client.GetAppStatus(ctx, app.Slug, "")
			return status, appStatusError(app.Slug, err)
		}
		if code := waitForCondition(read, want, remaining, *interval); code != 0 {
			return code
		}
	}
	return 0
}
