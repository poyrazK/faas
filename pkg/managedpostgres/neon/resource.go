package neon

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// A normal Gregale database maps to a Neon project. A restored database maps
// to a branch in that project and is encoded as project_id/branch_id. The
// encoding stays opaque at the managedpostgres boundary, while this adapter
// can route credentials, usage, inspection, and deletion to the right Neon
// object without changing the catalog contract.
type resourceRef struct {
	projectID string
	branchID  string
}

func parseResourceRef(value string) (resourceRef, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 1 && len(parts) != 2 {
		return resourceRef{}, managedpostgres.ErrInvalid
	}
	if !validProviderID.MatchString(parts[0]) {
		return resourceRef{}, managedpostgres.ErrInvalid
	}
	ref := resourceRef{projectID: parts[0]}
	if len(parts) == 2 {
		if !validProviderID.MatchString(parts[1]) {
			return resourceRef{}, managedpostgres.ErrInvalid
		}
		ref.branchID = parts[1]
	}
	return ref, nil
}

func (r resourceRef) String() string {
	if r.branchID == "" {
		return r.projectID
	}
	return r.projectID + "/" + r.branchID
}
