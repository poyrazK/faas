package state

// releaseTaskTurnLocked mirrors ClaimNextUnfencedAppTask's release rules
// (ADR-1448 amendment 1): one release command runs per app scope at a time,
// oldest first, and none after one whose outcome is unknown until a person
// checks it. Two migrations against one database, or an older migration after
// a newer one, can leave a schema neither release expects.
func (m *MemStore) releaseTaskTurnLocked(task AppTask) bool {
	if task.Kind != AppTaskKindRelease {
		return true
	}
	scope := normalizedDeploymentScope(task.DeploymentScope)
	for id, other := range m.appTasks {
		if id == task.ID || other.Kind != AppTaskKindRelease || other.AccountID != task.AccountID ||
			other.AppID != task.AppID || normalizedDeploymentScope(other.DeploymentScope) != scope {
			continue
		}
		// Nor after a release command whose outcome is unknown and
		// unchecked (UncertainReleaseTask).
		if m.releaseOutcomeUncertainLocked(other) {
			return false
		}
		switch other.Status {
		case AppTaskRestoring, AppTaskRunning:
			return false
		case AppTaskQueued:
			if other.CancelRequested == nil && (other.CreatedAt.Before(task.CreatedAt) ||
				other.CreatedAt.Equal(task.CreatedAt) && id < task.ID) {
				return false
			}
		}
	}
	return true
}
