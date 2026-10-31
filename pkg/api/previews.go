package api

import (
	"fmt"
	"net/http"
)

// Preview is one self-serve platform preview a customer may join (ADR
// 20261021080640), as GET /v1/previews and GET /v1/orgs/{slug}/previews
// list it.
type Preview struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// RetireBy is the date the preview is planned to end, by general
	// availability or removal.
	RetireBy string `json:"retire_by,omitempty"`
	// Joined reports whether the account (or, under /v1/orgs/{slug}, the
	// organization) opted in.
	Joined bool `json:"joined"`
	// Eligible reports whether the caller's plan may use an opt-in.
	Eligible bool `json:"eligible"`
	// Enabled is whether the preview is on for the caller now, for any
	// reason: an opt-in, an operator rule or the default.
	Enabled bool `json:"enabled"`
}

// PreviewList is GET /v1/previews.
type PreviewList struct {
	Items []Preview `json:"items"`
}

// Preview problem codes.
const (
	// CodePreviewNotOffered: the key is not a preview open to self-serve.
	CodePreviewNotOffered = "preview_not_offered"
	// CodePreviewPlanNotEligible: the caller's plan may not join it.
	CodePreviewPlanNotEligible = "preview_plan_not_eligible"
	// CodePreviewFull: the preview reached its opt-in capacity.
	CodePreviewFull = "preview_full"
	// CodePreviewOrgUnsupported: the preview is decided where the
	// organization is unknown, so only accounts may join it.
	CodePreviewOrgUnsupported = "preview_org_unsupported"
)

// ErrPreviewNotOffered is the 404 for a key that is not a joinable preview.
func ErrPreviewNotOffered(key string) *Problem {
	return NewProblem(http.StatusNotFound, CodePreviewNotOffered, "Preview not offered",
		fmt.Sprintf("%q is not a preview you can join; list them with GET /v1/previews", key)).WithDocs(docsBase + "/cli#previews")
}

// ErrPreviewPlanNotEligible is the 403 for a plan the preview excludes.
func ErrPreviewPlanNotEligible(key string, plan Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePreviewPlanNotEligible, "Plan not eligible for this preview",
		fmt.Sprintf("the %s plan cannot join the %s preview", plan, key)).WithDocs(docsBase + "/cli#previews")
}

// ErrPreviewFull is the 409 for a preview at capacity.
func ErrPreviewFull(key string, limit, observed int) *Problem {
	return NewProblem(http.StatusConflict, CodePreviewFull, "Preview is full",
		fmt.Sprintf("the %s preview admits %d accounts and organizations and has %d; try again later", key, limit, observed)).
		WithLimit(int64(limit), int64(observed)).WithDocs(docsBase + "/cli#previews")
}

// ErrPreviewOrgUnsupported is the 409 for an organization joining a preview
// that can only be released per account.
func ErrPreviewOrgUnsupported(key string) *Problem {
	return NewProblem(http.StatusConflict, CodePreviewOrgUnsupported, "Preview cannot be joined by an organization",
		fmt.Sprintf("the %s preview is released per account; join it with PUT /v1/previews/%s", key, key)).WithDocs(docsBase + "/cli#previews")
}
