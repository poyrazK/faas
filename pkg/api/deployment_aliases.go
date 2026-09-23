package api

import "regexp"

// DeploymentAliasNamePattern is the accepted DNS-label syntax for a
// customer-managed name that points at one immutable deployment revision.
const DeploymentAliasNamePattern = `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`

var deploymentAliasNameRE = regexp.MustCompile(DeploymentAliasNamePattern)

// ValidDeploymentAliasName reports whether name is a lowercase DNS label.
func ValidDeploymentAliasName(name string) bool {
	return deploymentAliasNameRE.MatchString(name)
}
