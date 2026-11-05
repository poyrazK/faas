package api

import "net/http"

// CodePRPreviewResourceUnsupported refuses a bucket or a managed database
// binding on a PR preview (ADR-20261105090226).
const CodePRPreviewResourceUnsupported = "pr_preview_resource_unsupported"

// ErrPRPreviewResourceUnsupported is the 409 for a stateful resource asked
// of a PR preview: the preview runs unreviewed code and is torn down with its
// pull request, which would strand the resource.
func ErrPRPreviewResourceUnsupported(resource string) *Problem {
	return NewProblem(http.StatusConflict, CodePRPreviewResourceUnsupported, "PR previews cannot hold "+resource,
		"a pull request preview runs unreviewed code and is torn down with its pull request, so it cannot hold "+resource+".").
		WithHint("Give previews test credentials in the production app's pr-preview scope: gregale env set --app <app> --env pr-preview KEY=value").
		WithDocs("https://gregale.dev/docs/preview-environments")
}
