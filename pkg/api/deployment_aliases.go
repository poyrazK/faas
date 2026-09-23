package api

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// DeploymentAliasNamePattern is the accepted DNS-label syntax for a
// customer-managed name that points at one immutable deployment revision.
const DeploymentAliasNamePattern = `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`

var deploymentAliasNameRE = regexp.MustCompile(DeploymentAliasNamePattern)

// ValidDeploymentAliasName reports whether name is a lowercase DNS label.
func ValidDeploymentAliasName(name string) bool {
	return deploymentAliasNameRE.MatchString(name)
}

// DeploymentAliasHostLabel returns the one-label hostname used to reach a
// named deployment alias. The immutable app UUID keeps the host unique and
// stable across app slug renames. The "tag-" namespace is reserved from
// ordinary app slugs so an alias can never shadow a production app route.
func DeploymentAliasHostLabel(appID, name string) (string, bool) {
	id, err := uuid.Parse(appID)
	if err != nil || !ValidDeploymentAliasName(name) {
		return "", false
	}
	label := "tag-" + name + "-" + strings.ReplaceAll(id.String(), "-", "")
	if len(label) > 63 {
		return "", false
	}
	return label, true
}
