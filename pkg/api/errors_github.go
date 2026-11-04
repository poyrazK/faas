package api

import (
	"fmt"
	"net/http"
)

// CodeGitHubAccountConflict identifies an installation owned by another account.
const CodeGitHubAccountConflict = "github_account_conflict"

// Connect GitHub outcomes a customer can act on (ADR-20261101160300). Each
// used to reach the browser as 502 "Could not reach GitHub".
const (
	// CodeGitHubAppNotInstalled: the GitHub user sees no installation of the
	// Gregale GitHub App; the connect flow continues with installing it.
	CodeGitHubAppNotInstalled = "github_app_not_installed"
	// CodeGitHubInstallationChoiceRequired: the GitHub user may connect more
	// than one installation and must choose one.
	CodeGitHubInstallationChoiceRequired = "github_installation_choice_required"
	// CodeGitHubInstallationNotAccessible: the chosen installation is not
	// visible to the GitHub user who authorized the connection.
	CodeGitHubInstallationNotAccessible = "github_installation_not_accessible"
	// CodeGitHubInstallationOwnerRequired: a personal installation can be
	// connected only by its owner.
	CodeGitHubInstallationOwnerRequired = "github_installation_owner_required"
	// CodeGitHubOrgAdminRequired: an organization installation can be
	// connected only by an organization admin.
	CodeGitHubOrgAdminRequired = "github_org_admin_required"
	// CodeGitHubPermissionPending: an organization owner has not accepted a
	// permission the Gregale GitHub App asks for.
	CodeGitHubPermissionPending = "github_permission_pending"
	// CodeGitHubInstallationRequired: the account has several connected
	// installations and the request did not name one.
	CodeGitHubInstallationRequired = "github_installation_required"
)

// GitHubConnectDocsURL documents connecting GitHub and binding an app.
const GitHubConnectDocsURL = "https://gregale.dev/docs/deploy-from-github"

// ErrGitHubAccountConflict never identifies the account that owns the
// installation. The OAuth user may know the GitHub installation without
// having access to the other Gregale account.
func ErrGitHubAccountConflict() *Problem {
	const recovery = "This GitHub installation is already connected to another Gregale account. Sign in to the connected account, or contact support to move this connection to your current account."
	return NewProblem(http.StatusConflict, CodeGitHubAccountConflict,
		"GitHub account conflict", recovery).WithHint(recovery)
}

// ErrGitHubAppNotInstalled keeps the phrase "user has no app installations",
// which an older apid matches to continue with the App installation.
func ErrGitHubAppNotInstalled() *Problem {
	return NewProblem(http.StatusNotFound, CodeGitHubAppNotInstalled,
		"Gregale GitHub App not installed",
		"The GitHub user has no app installations of the Gregale GitHub App.").
		WithHint("Install the Gregale GitHub App on your account or organization, then connect again.").
		WithDocs(GitHubConnectDocsURL)
}

// ErrGitHubInstallationChoiceRequired asks the customer to pick one of the
// installations they may connect.
func ErrGitHubInstallationChoiceRequired(count int) *Problem {
	return NewProblem(http.StatusConflict, CodeGitHubInstallationChoiceRequired,
		"Choose a GitHub installation",
		fmt.Sprintf("You can connect %d GitHub App installations. Choose the one that owns your repositories.", count)).
		WithDocs(GitHubConnectDocsURL)
}

// ErrGitHubInstallationNotAccessible reports a chosen installation the GitHub
// user cannot see.
func ErrGitHubInstallationNotAccessible(installationID int64) *Problem {
	return NewProblem(http.StatusForbidden, CodeGitHubInstallationNotAccessible,
		"GitHub installation not accessible",
		fmt.Sprintf("The GitHub user who authorized Gregale cannot access installation %d.", installationID)).
		WithHint("Sign in to GitHub as a user with access to that installation, then connect again.").
		WithDocs(GitHubConnectDocsURL)
}

// ErrGitHubInstallationOwnerRequired names the personal account's owner.
func ErrGitHubInstallationOwnerRequired(owner string) *Problem {
	return NewProblem(http.StatusForbidden, CodeGitHubInstallationOwnerRequired,
		"Only the owner can connect this installation",
		fmt.Sprintf("Only %s can connect their personal GitHub App installation.", owner)).
		WithHint("Sign in to GitHub as that user, or install the Gregale GitHub App on your own account.").
		WithDocs(GitHubConnectDocsURL)
}

// ErrGitHubOrgAdminRequired names the organization.
func ErrGitHubOrgAdminRequired(org string) *Problem {
	return NewProblem(http.StatusForbidden, CodeGitHubOrgAdminRequired,
		"Organization admin required",
		fmt.Sprintf("Connecting the %s installation requires an admin of that organization.", org)).
		WithHint("Ask an organization admin to connect GitHub, or to make you an admin.").
		WithDocs(GitHubConnectDocsURL)
}

// ErrGitHubPermissionPending reports a permission an organization owner has
// not accepted yet.
func ErrGitHubPermissionPending(org, permission string) *Problem {
	return NewProblem(http.StatusForbidden, CodeGitHubPermissionPending,
		"GitHub permission not accepted",
		fmt.Sprintf("An owner of %s must accept the Gregale GitHub App's %s permission.", org, permission)).
		WithHint("Ask an organization owner to review the pending permission request in the organization's GitHub App settings.").
		WithDocs(GitHubConnectDocsURL)
}

// ErrGitHubInstallationRequired lists the connected installations a request
// can name.
func ErrGitHubInstallationRequired(installationIDs []int64) *Problem {
	return NewProblem(http.StatusConflict, CodeGitHubInstallationRequired,
		"Choose a GitHub installation",
		fmt.Sprintf("This account has %d connected GitHub installations (%s); name one with installation_id.", len(installationIDs), formatInstallationIDs(installationIDs))).
		WithDocs(GitHubConnectDocsURL)
}

func formatInstallationIDs(ids []int64) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%d", id)
	}
	return out
}
