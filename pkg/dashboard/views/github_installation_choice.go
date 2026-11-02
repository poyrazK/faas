package views

// GitHubInstallationChoiceView lists the GitHub App installations the GitHub
// user may connect when there is more than one (ADR-20261101160300). Each
// option restarts Connect GitHub with that installation.
type GitHubInstallationChoiceView struct {
	Candidates                []GitHubInstallationCandidateView
	ConnectGithubConfirmToken string
	ReturnTo                  string
}

// GitHubInstallationCandidateView is one installation in the chooser.
type GitHubInstallationCandidateView struct {
	ID           int64
	Account      string
	Organization bool
}
