package copyinventory

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// DatabaseCatalogue retains every logical database, tablespace and scoped GUC.
// It is sensitive worker input. Physical tablespace locations are target-specific.
type DatabaseCatalogue struct {
	PostgresMajor        int
	DatabaseOID, RoleOID uint32
	InventoryFingerprint string
	Databases            []Database
	Settings             []Setting
	Tablespaces          []Tablespace
}

func (DatabaseCatalogue) String() string     { return "private PostgreSQL database catalogue" }
func (c DatabaseCatalogue) GoString() string { return c.String() }
func (c DatabaseCatalogue) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Databases, Settings, Tablespaces int }{len(c.Databases), len(c.Settings), len(c.Tablespaces)})
}

func (i Inventory) DatabaseCatalogueForWorker() (DatabaseCatalogue, error) {
	if !hexDigest(i.fingerprint) || !validPayload(i.body) {
		return DatabaseCatalogue{}, pgerrors.ErrInvalid
	}
	return databaseCatalogue(i.body, i.fingerprint)
}

// This view uses the original admission projection, never today's source.
func (p ExportPlan) DatabaseCatalogueForWorker() (DatabaseCatalogue, error) {
	if _, err := p.RequirementsForWorker(); err != nil {
		return DatabaseCatalogue{}, err
	}
	return databaseCatalogue(p.logical, p.inventoryFingerprint)
}

func databaseCatalogue(b payload, fp string) (DatabaseCatalogue, error) {
	c := DatabaseCatalogue{PostgresMajor: b.PostgresMajor, DatabaseOID: b.DatabaseOID, RoleOID: b.RoleOID, InventoryFingerprint: fp, Databases: b.Databases, Settings: b.Settings, Tablespaces: b.Tablespaces}
	// Use a private envelope to detach nested ACL/options/settings and pointers.
	type private DatabaseCatalogue
	raw, err := json.Marshal(private(c))
	if err != nil {
		return DatabaseCatalogue{}, pgerrors.ErrInvalid
	}
	var detached private
	if json.Unmarshal(raw, &detached) != nil {
		return DatabaseCatalogue{}, pgerrors.ErrConflict
	}
	return DatabaseCatalogue(detached), nil
}
