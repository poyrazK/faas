// adr: 20261020150000
package rehearsalcommand

import (
	"slices"

	"github.com/google/uuid"
)

// canonicalDeployment returns the hyphenated form the envelope requires; an
// unparsable identity is returned unchanged and fails envelope validation.
func canonicalDeployment(id string) string {
	if parsed, err := uuid.Parse(id); err == nil {
		return parsed.String()
	}
	return id
}

// ReleaseArgv is the argv a deployment's release command executes. A shell
// release runs through the same /bin/sh -lc wrapper as an ordinary shell task.
func ReleaseArgv(command []string, shell bool) []string {
	if shell {
		// A shell release is one script string; anything else is ambiguous.
		if len(command) != 1 {
			return nil
		}
		return []string{"/bin/sh", "-lc", command[0]}
	}
	return append([]string(nil), command...)
}

// ContainedRelease encodes a deployment's release command as the pinned
// contained command its rehearsal run executes. The release receives the
// deployment's own environment and secrets, never extra env or files.
func ContainedRelease(runID, deploymentID string, command []string, shell bool) ([]string, error) {
	return Encode(Spec{Version: Version, RunID: runID, DeploymentID: canonicalDeployment(deploymentID), Command: ReleaseArgv(command, shell)})
}

// IsContainedReleaseFor reports whether argv is exactly the pinned contained
// form of the deployment's release command.
func IsContainedReleaseFor(argv []string, deploymentID string, command []string, shell bool) bool {
	spec, err := Decode(argv)
	return err == nil && spec.DeploymentID == canonicalDeployment(deploymentID) && len(spec.Env) == 0 && len(spec.Files) == 0 &&
		len(command) > 0 && slices.Equal(spec.Command, ReleaseArgv(command, shell))
}
