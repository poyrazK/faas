package state

// RequiresLatestRevision reports whether automatic promotion must be fenced
// against newer accepted deployments in the same app and environment scope.
// Explicit rollback uses the separate, unfenced promotion operation.
func (k DeploymentKind) RequiresLatestRevision() bool {
	switch k {
	case DeploymentKindGitHub, DeploymentKindPreview, DeploymentKindImage:
		return true
	default:
		return false
	}
}
