package state

import "github.com/onebox-faas/faas/pkg/api"

// validAccountReleaseWebhookFilter is the in-memory counterpart of the
// app_webhooks_scope_chk constraint. Account receivers must opt in to a
// non-empty subset of release and environment events, never a wildcard.
func validAccountReleaseWebhookFilter(events []string) bool {
	if len(events) == 0 || len(events) > len(api.AllowedAccountReleaseWebhookEvents) {
		return false
	}
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		switch AppWebhookEvent(event) {
		case AppWebhookEventDeploymentLive, AppWebhookEventDeploymentFailed,
			AppWebhookEventRolloutCompleted, AppWebhookEventRolloutAborted, AppWebhookEventEnvironmentGitOpsChanged:
		default:
			return false
		}
		if _, duplicate := seen[event]; duplicate {
			return false
		}
		seen[event] = struct{}{}
	}
	return true
}
