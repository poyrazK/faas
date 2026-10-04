// adr: 531
package hostidentity

import (
	"strings"

	"github.com/google/uuid"
)

// DeploymentAliasLabel encodes the same immutable app/name label as the SQL
// router. Allocation validates name and the DNS length separately; reads must
// retain legacy SQL labels that were allocated with a longer name.
func DeploymentAliasLabel(appID, name string) (string, bool) {
	id, err := uuid.Parse(appID)
	if err != nil {
		return "", false
	}
	return "tag-" + name + "-" + strings.ReplaceAll(id.String(), "-", ""), true
}

func DeploymentAliasLabelFromHost(suffix, host string) (string, bool) {
	label, ok := AppSlugFromHost(suffix, host)
	return label, ok && strings.HasPrefix(label, "tag-")
}
