package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

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
	for _, slug := range keyed {
		_, _ = fmt.Fprintf(w, "  %s needs a Gregale API key; to serve it publicly: %s app update %s --no-require-authn\n", slug, recoveryCLI(), recoveryArgument(slug))
	}
}
