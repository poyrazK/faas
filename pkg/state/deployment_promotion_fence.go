package state

// RequiresLatestRevision reports whether automatic promotion must be fenced
// against newer accepted deployments in the same app and environment scope.
// Every customer deploy kind is fenced: a CLI tarball or Dockerfile deploy
// that finishes after a newer revision was accepted used to go live and
// retire the newer one, so the release that finished last won instead of the
// one requested last. Explicit rollback uses the separate, unfenced promotion
// operation.
func (k DeploymentKind) RequiresLatestRevision() bool {
	switch k {
	case DeploymentKindGitHub, DeploymentKindPreview, DeploymentKindImage,
		DeploymentKindTarball, DeploymentKindDockerfile:
		return true
	default:
		return false
	}
}
