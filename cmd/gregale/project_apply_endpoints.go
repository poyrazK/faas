package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// projectEnvironmentStateReader reads where an environment's workloads answer.
type projectEnvironmentStateReader interface {
	GetProjectEnvironmentState(ctx context.Context, projectSlug, environmentSlug string) (api.ProjectEnvironmentStateResponse, error)
}

// renderProjectEndpointsFor prints the endpoint summary of the environment the
// deploy targeted. A non-production environment answers on its own URLs; the
// summary used to list the production URLs, so a staging deploy pointed the
// reader at production (prod 2026-11-19).
func renderProjectEndpointsFor(ctx context.Context, w io.Writer, client interface {
	projectAppReader
	projectEnvironmentStateReader
}, apply api.ApplyResponse, projectSlug, environment string) {
	if environment == "" || isProductionEnvironmentSlug(environment) || projectSlug == "" {
		renderProjectEndpoints(ctx, w, client, apply)
		return
	}
	state, err := client.GetProjectEnvironmentState(ctx, projectSlug, environment)
	if err != nil {
		_, _ = fmt.Fprintf(w, "\nEndpoints: see %s environment get %s %s\n", recoveryCLI(), recoveryArgument(projectSlug), recoveryArgument(environment))
		return
	}
	type endpoint struct{ slug, url string }
	var rows []endpoint
	width := 0
	for _, workload := range state.Workloads {
		if workload.Release.URL == "" {
			continue
		}
		rows = append(rows, endpoint{workload.WorkloadSlug, workload.Release.URL})
		width = max(width, len(workload.WorkloadSlug))
	}
	if len(rows) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nEndpoints (environment %s):\n", environment)
	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "  %-*s  %s\n", width, row.slug, row.url)
	}
}

func isProductionEnvironmentSlug(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "production", "prod", "default":
		return true
	}
	return false
}

// projectAppReader is the one client call the endpoint summary needs.
type projectAppReader interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
}

// renderProjectEndpoints repeats, under the final summary, where each public
// app of a project deploy answers and who may call it. A long project deploy
// scrolls the opening banner away, so without this the last screen held only
// deployment and build IDs. Internal apps (service mesh only) are listed by
// name; apps the CLI cannot read are left out rather than guessed.
func renderProjectEndpoints(ctx context.Context, w io.Writer, client projectAppReader, apply api.ApplyResponse) {
	type endpoint struct{ slug, url, auth string }
	var public []endpoint
	var internal, keyed []string
	for _, app := range apply.Apps {
		if strings.TrimSpace(app.Slug) == "" {
			continue
		}
		got, err := client.GetApp(ctx, app.Slug)
		if err != nil {
			continue
		}
		if got.Visibility == string(api.AppVisibilityInternal) || got.PublicAuth.Mode == api.AppPublicAuthModeInternalOnly {
			internal = append(internal, app.Slug)
			continue
		}
		public = append(public, endpoint{slug: app.Slug, url: canonicalAppURL(got), auth: formatAppAuth(got)})
		if got.RequireAuthn && got.PublicAuth.Mode != api.AppPublicAuthModeBasic {
			keyed = append(keyed, app.Slug)
		}
	}
	if len(public) == 0 && len(internal) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nEndpoints:")
	width := 0
	for _, e := range public {
		width = max(width, len(e.slug))
	}
	for _, e := range public {
		_, _ = fmt.Fprintf(w, "  %-*s  %s  (%s)\n", width, e.slug, e.url, e.auth)
	}
	if len(internal) > 0 {
		_, _ = fmt.Fprintf(w, "  internal only (service mesh): %s\n", strings.Join(internal, ", "))
	}
	// An app's visibility is set when it is created: a service first deployed
	// without ports stays internal after ports are added, and nothing said so
	// (deploy-path probe 2026-11-21, R6-3: "Endpoints:" listed no URL).
	if len(public) == 0 && len(internal) > 0 {
		_, _ = fmt.Fprintf(w, "  no app has a public URL; to give one a public URL: %s app %s --visibility public\n", recoveryCLI(), recoveryArgument(internal[0]))
	}
	for _, slug := range keyed {
		_, _ = fmt.Fprintf(w, "  %s needs a Gregale API key; to serve it publicly: %s app update %s --no-require-authn\n", slug, recoveryCLI(), recoveryArgument(slug))
	}
}
