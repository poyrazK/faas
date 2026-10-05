package copyinventory

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// MembershipCatalogue is sensitive worker input. No predefined, provider or
// maintenance grant is omitted; every grantor and option remains required.
type MembershipCatalogue struct {
	Roles       RoleCatalogue
	Memberships []Membership
}

func (MembershipCatalogue) String() string     { return "private PostgreSQL membership catalogue" }
func (m MembershipCatalogue) GoString() string { return m.String() }
func (m MembershipCatalogue) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Memberships int }{len(m.Memberships)})
}

func (i Inventory) MembershipCatalogueForWorker() (MembershipCatalogue, error) {
	roles, err := i.RoleCatalogueForWorker()
	if err != nil {
		return MembershipCatalogue{}, err
	}
	return membershipCatalogue(roles, i.body.Memberships)
}

func (p ExportPlan) MembershipCatalogueForWorker() (MembershipCatalogue, error) {
	roles, err := p.RoleCatalogueForWorker()
	if err != nil {
		return MembershipCatalogue{}, err
	}
	return membershipCatalogue(roles, p.logical.Memberships)
}

func membershipCatalogue(roles RoleCatalogue, memberships []Membership) (MembershipCatalogue, error) {
	raw, err := json.Marshal(memberships)
	if err != nil {
		return MembershipCatalogue{}, pgerrors.ErrInvalid
	}
	m := MembershipCatalogue{Roles: roles}
	if json.Unmarshal(raw, &m.Memberships) != nil {
		return MembershipCatalogue{}, pgerrors.ErrConflict
	}
	return m, nil
}
