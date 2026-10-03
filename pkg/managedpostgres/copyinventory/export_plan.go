package copyinventory

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// OriginalAdmission is recovered from an authenticated source fence receipt.
// Names and ownership OIDs must match the frozen catalogue. A fence changes
// ALLOW_CONNECTIONS only; other logical settings remain inventory input.
type OriginalAdmission struct {
	DatabaseOID, OwnerOID    uint32
	DatabaseName             string
	OriginalAllowConnections bool
}

// DatabaseExport is sensitive worker input, never a public API/log value.
// Every catalogue database remains required, including templates and private
// maintenance databases. Import must explicitly classify/map private resources.
type DatabaseExport struct {
	Scope                       Scope
	InventoryFingerprint        string
	Database                    Database
	CapturedAllowConnections    bool
	AuthenticatedReaderDatabase bool
	AuthenticatedReaderRoleOID  uint32
}

func (d DatabaseExport) String() string   { return "private PostgreSQL database export requirement" }
func (d DatabaseExport) GoString() string { return d.String() }
func (d DatabaseExport) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ RequiresAdmissionProjection, Template, ReaderDatabase bool }{!d.CapturedAllowConnections, d.Database.Template, d.AuthenticatedReaderDatabase})
}

type ExportPlan struct {
	scope                Scope
	inventoryFingerprint string
	captured, logical    payload
}

type ExportPlanSummary struct {
	InventoryFingerprint                                                                                        string
	Databases, Templates, AdmissionProjections, Roles, Memberships, Settings, Tablespaces, PreparedTransactions int
}

func (p ExportPlan) Summary() ExportPlanSummary {
	s := ExportPlanSummary{InventoryFingerprint: p.inventoryFingerprint, Databases: len(p.logical.Databases), Roles: len(p.logical.Roles), Memberships: len(p.logical.Memberships),
		Settings: len(p.logical.Settings), Tablespaces: len(p.logical.Tablespaces), PreparedTransactions: len(p.logical.PreparedTransactions)}
	for _, d := range p.captured.Databases {
		if d.Template {
			s.Templates++
		}
		if !d.AllowConnections {
			s.AdmissionProjections++
		}
	}
	return s
}

func (p ExportPlan) MarshalJSON() ([]byte, error) { return json.Marshal(p.Summary()) }
func (p ExportPlan) String() string {
	s := p.Summary()
	return fmt.Sprintf("private PostgreSQL export plan: databases=%d projections=%d prepared=%d", s.Databases, s.AdmissionProjections, s.PreparedTransactions)
}
func (p ExportPlan) GoString() string { return p.String() }

// PlanExports retains complete cluster metadata and every database requirement.
// Temporary source closure is projected back to original logical admission.
// Neither scope nor this plan authenticates a connection or proves copied data.
func (i Inventory) PlanExports(scope Scope, admission []OriginalAdmission) (ExportPlan, error) {
	if scope.Validate() != nil || !hexDigest(i.fingerprint) || !validPayload(i.body) {
		return ExportPlan{}, pgerrors.ErrInvalid
	}
	if scope.PostgresMajor != i.body.PostgresMajor {
		return ExportPlan{}, pgerrors.ErrConflict
	}
	raw, err := i.PayloadForSealing()
	if err != nil {
		return ExportPlan{}, err
	}
	p := ExportPlan{scope: scope, inventoryFingerprint: i.fingerprint}
	// Deep copies prevent later caller mutations from changing retained input.
	if json.Unmarshal(raw, &p.captured) != nil || json.Unmarshal(raw, &p.logical) != nil {
		return ExportPlan{}, pgerrors.ErrConflict
	}
	order := func(a, b Database) int {
		if a.OID < b.OID {
			return -1
		}
		if a.OID > b.OID {
			return 1
		}
		return 0
	}
	slices.SortFunc(p.captured.Databases, order)
	slices.SortFunc(p.logical.Databases, order)
	seen := map[uint32]bool{}
	for _, original := range admission {
		if original.DatabaseOID == 0 || original.OwnerOID == 0 || !validName(original.DatabaseName) || seen[original.DatabaseOID] {
			return ExportPlan{}, pgerrors.ErrConflict
		}
		seen[original.DatabaseOID] = true
		found := false
		for n, d := range p.captured.Databases {
			if d.OID != original.DatabaseOID {
				continue
			}
			if d.Name != original.DatabaseName || d.OwnerOID != original.OwnerOID || d.AllowConnections {
				return ExportPlan{}, pgerrors.ErrConflict
			}
			p.logical.Databases[n].AllowConnections = original.OriginalAllowConnections
			found = true
		}
		if !found {
			return ExportPlan{}, pgerrors.ErrConflict
		}
	}
	return p, nil
}

// RequirementsForWorker explicitly includes every database. Prepared
// transactions require a qualified strategy before ordinary dumps may start.
func (p ExportPlan) RequirementsForWorker() ([]DatabaseExport, error) {
	if p.scope.Validate() != nil || !hexDigest(p.inventoryFingerprint) || !validPayload(p.logical) {
		return nil, pgerrors.ErrInvalid
	}
	if len(p.logical.PreparedTransactions) > 0 {
		return nil, pgerrors.ErrUnsupported
	}
	raw, err := json.Marshal(p.logical.Databases)
	if err != nil {
		return nil, pgerrors.ErrInvalid
	}
	var dbs []Database
	if json.Unmarshal(raw, &dbs) != nil {
		return nil, pgerrors.ErrConflict
	}
	result := make([]DatabaseExport, 0, len(dbs))
	for n, d := range dbs {
		result = append(result, DatabaseExport{Scope: p.scope, InventoryFingerprint: p.inventoryFingerprint, Database: d,
			CapturedAllowConnections: p.captured.Databases[n].AllowConnections, AuthenticatedReaderDatabase: d.OID == p.captured.DatabaseOID, AuthenticatedReaderRoleOID: p.captured.RoleOID})
	}
	return result, nil
}

// LogicalMetadataForSealing preserves original admission plus all globals.
// This is projected configuration, not a replacement inventory/fingerprint.
// Passwords are absent. This plaintext must be encrypted before persistence.
func (p ExportPlan) LogicalMetadataForSealing() ([]byte, error) {
	if p.scope.Validate() != nil || !hexDigest(p.inventoryFingerprint) || !validPayload(p.logical) {
		return nil, pgerrors.ErrInvalid
	}
	return Inventory{body: p.logical, fingerprint: p.inventoryFingerprint}.PayloadForSealing()
}
