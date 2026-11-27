// adr: 20261112223000

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// A deploy to an app that already exists replaces its running release. When
// the directory is not linked to that app, the name may belong to something
// else in the account: the 2026-11-15 agent eval picked `retail-ui` for a new
// copy of a store and replaced the owner's three-day-old app of that name.
// The preview names the target, a real deploy says so before uploading, and a
// person at a terminal confirms. Automation (--yes, --json, no terminal) only
// gets the notice: redeploying by name from an unlinked CI checkout is normal.

// sourceLinkedTo reports whether the directory's saved or committed context
// names slug as its app.
func sourceLinkedTo(dir, slug string) bool {
	link, _, err := targetProjectContext(dir)
	return err == nil && link.App == slug
}

// deployTargetSummary is the preview's Target line.
func deployTargetSummary(exists, linked bool) string {
	switch {
	case !exists:
		return "new app"
	case linked:
		return "existing app, linked to this directory"
	default:
		return "existing app, not linked to this directory; this deploy replaces its running release"
	}
}

// rememberCreatedApp links dir to the app this deploy just created when no
// saved or committed context covers dir. Without it a later plain `gregale
// deploy` there targeted an app named after the directory: on prod
// 2026-11-19, `deploy --name bh1119-probe` then `deploy` created a second app
// and took the bare global slug probe.gregale.dev. An existing app is never
// linked implicitly (see guardUnlinkedExistingApp).
func rememberCreatedApp(dir, slug string) {
	if dir == "" || slug == "" {
		return
	}
	if _, _, err := targetProjectContext(dir); !errors.Is(err, errProjectContextNotFound) && !otherAppLinkedAbove(dir, slug) {
		return
	}
	if err := rememberInitApp(dir, slug); err != nil {
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "warning: could not remember %s in %s: %v; run `gregale link --app %s` there\n", slug, dir, err, slug)
		}
		return
	}
	if !jsonOutput {
		PrintOK(osStdout, "Remembered app %s in %s", slug, projectContextPath(dir))
	}
}

func unlinkedExistingAppNotice(slug string) string {
	return fmt.Sprintf("%s already exists and this directory is not linked to it, so this deploy replaces its running release. "+
		"If it is not the app you mean to replace, stop and deploy under an unused --name (`gregale app list` shows the taken ones); "+
		"`gregale link --app %s` links this directory to it and silences this.", slug, slug)
}

// guardUnlinkedExistingApp prints the notice for a deploy that would replace
// an existing app this directory is not linked to, and asks a person at a
// terminal to confirm. It returns a non-zero exit code when they decline.
func guardUnlinkedExistingApp(ctx context.Context, client *Client, slug, dir string, yes bool) int {
	if sourceLinkedTo(dir, slug) {
		return 0
	}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return 0 // missing (a create) or unreadable: the deploy path reports it
	}
	if app.DeploymentAvailability == api.AppDeploymentAvailabilityMissing {
		// Reserved (`deploy --create-only`) or never deployed: there is no
		// running release to replace (MCP Tasks prod re-probe 2026-11-23).
		return 0
	}
	// --json keeps stderr to one machine-readable Problem; a prose note
	// before it broke every JSON deploy error to an unlinked app.
	if jsonOutput {
		return 0
	}
	_, _ = fmt.Fprintln(osStderr, "Note: "+unlinkedExistingAppNotice(slug))
	if yes || nonInteractive || !stdinIsTTY() {
		return 0
	}
	return confirmYesNo(fmt.Sprintf("Replace the running release of %s?", slug), "--yes", "replacing "+slug)
}
