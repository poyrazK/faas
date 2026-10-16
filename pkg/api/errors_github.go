package api

import "net/http"

// CodeGitHubAccountConflict identifies an installation owned by another account.
const CodeGitHubAccountConflict = "github_account_conflict"

// ErrGitHubAccountConflict never identifies the account that owns the
// installation. The OAuth user may know the GitHub installation without
// having access to the other Gregale account.
func ErrGitHubAccountConflict() *Problem {
	const recovery = "This GitHub installation is already connected to another Gregale account. Sign in to the connected account, or contact support to move this connection to your current account."
	return NewProblem(http.StatusConflict, CodeGitHubAccountConflict,
		"GitHub account conflict", recovery).WithHint(recovery)
}
