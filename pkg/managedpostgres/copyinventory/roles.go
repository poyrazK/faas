package copyinventory

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// RoleCatalogue is sensitive worker input. It includes provider, predefined and
// private maintenance roles; callers must classify every role explicitly.
type RoleCatalogue struct {
	PostgresMajor        int
	DatabaseOID, RoleOID uint32
	InventoryFingerprint string
	Roles                []Role
}

func (RoleCatalogue) String() string     { return "private PostgreSQL role catalogue" }
func (r RoleCatalogue) GoString() string { return r.String() }
func (r RoleCatalogue) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Roles int }{len(r.Roles)})
}

func (i Inventory) RoleCatalogueForWorker() (RoleCatalogue, error) {
	if !hexDigest(i.fingerprint) || !validPayload(i.body) {
		return RoleCatalogue{}, pgerrors.ErrInvalid
	}
	return roleCatalogue(i.body, i.fingerprint)
}

// RoleCatalogueForWorker returns the original logical role metadata, never a
// read of today's source. A prepared transaction still blocks materialization.
func (p ExportPlan) RoleCatalogueForWorker() (RoleCatalogue, error) {
	if _, err := p.RequirementsForWorker(); err != nil {
		return RoleCatalogue{}, err
	}
	return roleCatalogue(p.logical, p.inventoryFingerprint)
}

func roleCatalogue(b payload, fingerprint string) (RoleCatalogue, error) {
	raw, err := json.Marshal(b.Roles)
	if err != nil {
		return RoleCatalogue{}, pgerrors.ErrInvalid
	}
	r := RoleCatalogue{PostgresMajor: b.PostgresMajor, DatabaseOID: b.DatabaseOID, RoleOID: b.RoleOID, InventoryFingerprint: fingerprint}
	if json.Unmarshal(raw, &r.Roles) != nil {
		return RoleCatalogue{}, pgerrors.ErrConflict
	}
	return r, nil
}
