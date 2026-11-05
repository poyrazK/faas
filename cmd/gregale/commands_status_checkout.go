package main

// `gregale status` in a linked checkout — ADR-20261026145832 step 3: plain
// status shows what this checkout targets and how that app is doing; the
// Gregale platform status is `status --platform`. Unlinked, status still
// shows the platform, since there is no app to show.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// checkoutStatus is the machine form of the linked-app view.
type checkoutStatus struct {
	Project     string               `json:"project,omitempty"`
	Environment string               `json:"environment,omitempty"`
	App         *api.AppResponse     `json:"app,omitempty"`
	Account     *api.AccountResponse `json:"account,omitempty"`
	Platform    string               `json:"platform_status_command"`
}

// statusTarget resolves the app and project `status` reports on: --app, or
// the linked checkout. ok is false when there is neither.
func statusTarget(explicitApp string) (localProjectContext, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return localProjectContext{}, false, err
	}
	linked, _, err := linkedProjectContext(cwd)
	switch {
	case errors.Is(err, errProjectContextNotFound):
		linked = localProjectContext{}
	case err != nil && explicitApp == "":
		return localProjectContext{}, false, err
	}
	if explicitApp != "" {
		linked.App = explicitApp
	}
	return linked, linked.App != "" || linked.Project != "", nil
}

func cmdStatusCheckout(target localProjectContext) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	out := checkoutStatus{Project: target.Project, Environment: target.Environment, Platform: "gregale status --platform"}
	if target.App != "" {
		app, err := client.GetApp(ctx, target.App)
		if err != nil {
			return printErr("Could not load app", err)
		}
		out.App = &app
	}
	if account, err := client.Whoami(ctx); err == nil {
		out.Account = &account
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	// One release of notice for people who expect the platform numbers
	// (ADR-20261026145832 step 3).
	_, _ = fmt.Fprintln(osStderr, "Note: status shows this checkout's app; for Gregale's platform status run `gregale status --platform`.")
	renderCheckoutStatus(out)
	return 0
}

func renderCheckoutStatus(out checkoutStatus) {
	if out.Project != "" {
		_, _ = fmt.Fprintf(osStdout, "%-12s %s (linked)\n", "Project", out.Project)
	}
	environment := out.Environment
	if environment == "" {
		environment = "production"
	}
	if out.App == nil {
		_, _ = fmt.Fprintf(osStdout, "%-12s several workloads · %s; pass --app <slug> for one app\n", "App", environment)
	} else {
		_, _ = fmt.Fprintf(osStdout, "%-12s %s · %s\n", "App", out.App.Slug, environment)
		_, _ = fmt.Fprintf(osStdout, "%-12s %s\n", "State", out.App.Status)
		if url := out.App.CanonicalURL; url != "" {
			_, _ = fmt.Fprintf(osStdout, "%-12s %s\n", "URL", url)
		} else if out.App.URL != "" {
			_, _ = fmt.Fprintf(osStdout, "%-12s %s\n", "URL", out.App.URL)
		}
	}
	if out.Account != nil {
		_, _ = fmt.Fprintf(osStdout, "%-12s %s (%s plan)\n", "Account", out.Account.Email, out.Account.Plan)
	}
	_, _ = fmt.Fprintf(osStdout, "\nHealth: gregale app health %s --explain · Platform: %s\n", appOrPlaceholder(out.App), out.Platform)
}

func appOrPlaceholder(app *api.AppResponse) string {
	if app == nil {
		return "<app>"
	}
	return app.Slug
}
