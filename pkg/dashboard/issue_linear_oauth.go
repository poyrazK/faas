package dashboard

import "github.com/onebox-faas/faas/pkg/issuetracker"

type LinearOAuthPicker struct {
	AppSlug, IssueID, State, CSRF, WorkspaceID string
	Teams                                      []issuetracker.LinearOAuthTeam
}
