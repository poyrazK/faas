package dashboard

import "github.com/onebox-faas/faas/pkg/state"

type SlackOAuthConfirmation struct {
	AppSlug, State, CSRF string
	Installation         state.IssueSlackInstallation
}
