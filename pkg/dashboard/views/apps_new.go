// apps_new.go — Issue #961 / Mega-B PR-3: the dashboard's
// /dashboard/apps/new wizard view. Thin per-template data struct
// parallel to slo_account.go + slo_app.go. Lives in views/ so the
// template can stay a pure renderer (no business logic).
//
// Why this struct exists: the wizard needs both the connect-GitHub
// CTA (when env.GithubLogin is empty) AND the install/repo/template
// picker (when the §11 proof has been established). The two states
// are mutually exclusive in render — ConnectGithubConfirmToken is
// non-empty only when NeedsGithubConnect is true; Repos / Templates
// are non-empty only when NeedsGithubConnect is false. Mirroring the
// same split in the view makes the template's `{{if}}` blocks
// readable.
package views

// AppsNewTemplateView is one row in the wizard's template <select>.
// Name matches the embed.FS entry + templates.Names verbatim;
// Category is templates.CategoryFor (hello / function / stateless-
// contract / ai). Description is a one-line customer-facing blurb.
// 13 rows total today; adding a template means a new entry here.
type AppsNewTemplateView struct {
	Name        string
	Category    string
	Description string
}

// AppsNewRepoView is one row in the wizard's repo <select>.
// RepoFullName is "<owner>/<name>" — the same shape bindAppToRepo
// expects. DefaultBranch is echoed so the form's production_branch
// input can pre-fill.
type AppsNewRepoView struct {
	RepoFullName  string
	DefaultBranch string
}

// AppsNewView is the dashboard-local view struct for the
// /dashboard/apps/new page. One of three states:
//
//	NeedsGithubConnect=true                  → render the Connect GitHub CTA only
//	NeedsGithubConnect=false, GitHubDegraded → render the retry banner
//	NeedsGithubConnect=false, !GitHubDegraded → render the install/repo/template form
//
// PreFilledRepo echoes the ?repo=<owner>/<name> query param when
// present (the CLI's `gregale connect repo <owner>/<name>` deep-links
// here). ConnectGithubConfirmToken and CreateAppConfirmToken are the
// CSRF envelopes stamped at GET time for the two browser actions.
type AppsNewView struct {
	NeedsGithubConnect        bool
	NeedsGithubInstall        bool
	ConnectGithubConfirmToken string
	PreFilledRepo             string
	PreFilledInstallID        string
	PreFilledBranch           string
	PreFilledSlug             string
	ReturnTo                  string
	FormError                 string
	GitHubDegraded            bool
	GitHubDegradedMessage     string
	Templates                 []AppsNewTemplateView
	Repos                     []AppsNewRepoView
	Installations             []AppsNewInstallView
	CreateAppConfirmToken     string
}

// AppsNewInstallView is one row in the wizard's installation <select>.
// Mirrors githubdgrpc.Repo where possible; the wizard uses ID +
// AccountLogin to disambiguate a customer with multiple installations
// (personal + work).
type AppsNewInstallView struct {
	ID           int64
	AccountLogin string
	RepoCount    int
}
