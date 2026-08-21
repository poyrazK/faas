// project_preview.go — ADR-124 dashboard view struct for the
// affected-workloads preview. One struct backs three pages:
//
//	GET  /dashboard/projects/{slug}/preview      — empty form
//	POST /dashboard/projects/{slug}/preview      — populated preview
//	POST /dashboard/projects/{slug}/preview/apply — apply-with-exclude
//
// The empty-form case sets Preview=false and PreScanProblem=""; the
// populated case sets Preview=true and the three slices. The
// apply-with-exclude handler does not render the preview template
// at all — it 302s to /dashboard/apps/{slug} after a successful
// reconcile — so the struct does not need a third variant.
//
// Why a separate type from the wire DTO: the wire DTO
// (api.PlanAffectedApp) is JSON-encoded; the dashboard view is
// HTML-rendered with action-glyph + action-label strings + an
// `Excluded` boolean that the wire has no use for. A wire-shape
// to view-shape adapter keeps templates free of fmt.Sprintf /
// switch on the action enum.
package views

// ProjectPreviewAffected is one row of the dashboard preview tables
// (WillDeploy / Skipped / Unaffected). Maps 1:1 onto
// api.PlanAffectedApp plus the dashboard-only Excluded flag (true
// for Skipped rows that were dropped by operator --exclude).
type ProjectPreviewAffected struct {
	Slug         string
	Action       string // "create" | "update" | "remove" | "noop"
	ActionGlyph  string
	ActionLabel  string
	ID           string // existing app ID, empty for create
	ExistingRoot string // populated only on root_dir drift
	Excluded     bool   // true iff this row came from operator --exclude (Skipped)
}

// ProjectPreviewView is the dashboard-local view struct for
// /dashboard/projects/{slug}/preview. When Preview is false the
// form section renders and the three slices are nil. When true,
// the form section collapses to a hidden re-submit and the three
// tables render.
//
// CSRF envelope (PreviewFormToken) is minted on every GET so the
// multipart POST re-submits with a fresh token, mirroring the
// renderAppNew / bindAppConfirmToken pattern.
type ProjectPreviewView struct {
	ProjectSlug     string
	ProjectNotFound bool
	Preview         bool
	PlanToken       string
	CanApply        bool
	NotAllowed      bool
	ObservedApps    int
	LimitApps       int

	WillDeploy []ProjectPreviewAffected
	Skipped    []ProjectPreviewAffected
	Unaffected []ProjectPreviewAffected
	Removed    []string

	// ApplyResult is populated when the apply handler renders a
	// confirmation page after a successful reconcile. The
	// dashboard renders a banner above the tables so the operator
	// sees the destructive subset (Removed) inline; the apply
	// form is hidden behind the banner so a second click is
	// impossible. Zero-valued when the page is in preview mode.
	ApplyResult *ProjectPreviewApplyResult

	// PreScanProblem carries a non-empty detail when the multipart
	// scan returned a problem (e.g. secret-scan 422, source invalid,
	// exclude_unknown_slug, preview_expired cache miss). The
	// dashboard renders it inline above the form so the operator
	// can fix the input without leaving the page.
	PreScanProblem string

	// CSRF token for the multipart POST + apply POST forms.
	PreviewFormToken  string
	PreviewApplyToken string
}

// ProjectPreviewApplyResult is the post-apply summary shown
// above the partition tables. AddedSlugs / ChangedSlugs are the
// post-insert state.App rows (per reconcile.ApplyActions);
// RemovedSlugs is the destructive subset the operator should
// see explicitly because Removed is the irreversible outcome
// (ADR-124 §3 warns that --exclude semantics do not equal
// skip-deploy when an existing app row is matched). Empty
// AppliedAt renders as a server-side time.Time — populated by
// the apply handler.
type ProjectPreviewApplyResult struct {
	AddedSlugs   []string
	ChangedSlugs []string
	RemovedSlugs []string
	AppliedAt    string
}
