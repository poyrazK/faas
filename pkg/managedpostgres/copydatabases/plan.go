// Package copydatabases privately prepares every captured database identity.
// It retains original configuration but does not restore data, apply final ACLs/
// settings, activate admission, authenticate provider placement or publish a stage.
package copydatabases

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// PostgreSQL reserves lower OIDs for system objects. This is a catalogue domain,
// not a capacity quota; source OIDs are never reused as new target identities.
const firstNormalOID uint32 = 16384

type Disposition struct{ SourceOID, ExistingTargetOID, CreateTargetOID uint32 }
type TablespaceMapping struct{ SourceOID, TargetOID uint32 }

func (Disposition) String() string     { return "private PostgreSQL database disposition" }
func (d Disposition) GoString() string { return d.String() }
func (d Disposition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Existing bool }{d.ExistingTargetOID != 0})
}
func (TablespaceMapping) String() string               { return "private PostgreSQL tablespace mapping" }
func (m TablespaceMapping) GoString() string           { return m.String() }
func (TablespaceMapping) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

type privateDisposition Disposition
type privateTablespaceMapping TablespaceMapping
type privateCatalogue copyinventory.DatabaseCatalogue
type privateTarget copyarchive.RestoreTarget
type payload struct {
	Version                                 int           `json:"version"`
	Target                                  privateTarget `json:"target"`
	InventoryFingerprint, TargetFingerprint string
	Source, Baseline                        privateCatalogue
	RoleProof                               json.RawMessage
	Databases                               []privateDisposition
	Tablespaces                             []privateTablespaceMapping
}
type Plan struct{ body payload }
type Summary struct{ Databases, Existing, Created, Templates, Settings, Tablespaces int }

func (p Plan) MatchesRolePlanForWorker(source copyinventory.ExportPlan, parent copyroles.Plan) bool {
	if p.validate(source) != nil {
		return false
	}
	seed, err := copyroles.RecoverReceiptForWorker(source, copyarchive.RestoreTarget(p.body.Target), p.body.RoleProof)
	return err == nil && seed.MatchesPlanForWorker(parent)
}

func (p Plan) Summary() Summary {
	s := Summary{Databases: len(p.body.Databases), Settings: len(p.body.Source.Settings), Tablespaces: len(p.body.Tablespaces)}
	for _, d := range p.body.Databases {
		if d.ExistingTargetOID != 0 {
			s.Existing++
		} else {
			s.Created++
		}
	}
	for _, d := range p.body.Source.Databases {
		if d.Template {
			s.Templates++
		}
	}
	return s
}
func (Plan) String() string                 { return "private PostgreSQL database creation plan" }
func (p Plan) GoString() string             { return p.String() }
func (p Plan) MarshalJSON() ([]byte, error) { return json.Marshal(p.Summary()) }

// Classify every database and tablespace, including templates/provider/private
// entries. Existing database OIDs require a pinned pre-write catalogue; immutable
// locale/encoding/tablespace semantics must match. Mutable logical properties are
// retained for final materialization after imports. New databases start closed,
// non-template and bootstrap-owned so no source login or premature cloning occurs.
func NewPlan(source copyinventory.ExportPlan, seed copyroles.Receipt, target copyinventory.Inventory, databases []Disposition, spaces []TablespaceMapping) (Plan, error) {
	if !seed.MatchesSourceForWorker(source) {
		return Plan{}, pgerrors.ErrConflict
	}
	pins, err := seed.TargetForWorker()
	if err != nil {
		return Plan{}, err
	}
	src, err := source.DatabaseCatalogueForWorker()
	if err != nil {
		return Plan{}, err
	}
	base, err := target.DatabaseCatalogueForWorker()
	if err != nil {
		return Plan{}, err
	}
	if base.DatabaseOID != pins.DatabaseOID || base.RoleOID != pins.RoleOID || base.PostgresMajor != pins.Scope.PostgresMajor {
		return Plan{}, pgerrors.ErrConflict
	}
	proof, err := seed.PrivateProofForSealing()
	if err != nil {
		return Plan{}, err
	}
	fp, err := pins.Fingerprint()
	if err != nil {
		return Plan{}, err
	}
	p := Plan{payload{Version: 1, Target: privateTarget(pins), InventoryFingerprint: src.InventoryFingerprint, TargetFingerprint: fp, Source: privateCatalogue(src), Baseline: privateCatalogue(base), RoleProof: proof, Databases: make([]privateDisposition, len(databases)), Tablespaces: make([]privateTablespaceMapping, len(spaces))}}
	for n, d := range databases {
		p.body.Databases[n] = privateDisposition(d)
	}
	for n, m := range spaces {
		p.body.Tablespaces[n] = privateTablespaceMapping(m)
	}
	canonical(&p.body)
	if err := p.validate(source); err != nil {
		return Plan{}, err
	}
	raw, err := p.PrivatePayloadForSealing()
	if err != nil {
		return Plan{}, err
	}
	var detached payload
	if json.Unmarshal(raw, &detached) != nil {
		return Plan{}, pgerrors.ErrConflict
	}
	p.body = detached
	return p, nil
}

func canonical(p *payload) {
	slices.SortFunc(p.Databases, func(a, b privateDisposition) int { return compare(a.SourceOID, b.SourceOID) })
	slices.SortFunc(p.Tablespaces, func(a, b privateTablespaceMapping) int { return compare(a.SourceOID, b.SourceOID) })
	for _, c := range []*privateCatalogue{&p.Source, &p.Baseline} {
		slices.SortFunc(c.Databases, func(a, b copyinventory.Database) int { return compare(a.OID, b.OID) })
		slices.SortFunc(c.Tablespaces, func(a, b copyinventory.Tablespace) int { return compare(a.OID, b.OID) })
		slices.SortFunc(c.Settings, func(a, b copyinventory.Setting) int {
			if x := compare(a.DatabaseOID, b.DatabaseOID); x != 0 {
				return x
			}
			return compare(a.RoleOID, b.RoleOID)
		})
	}
}
func compare(a, b uint32) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
func name(s string) bool {
	return s != "" && len(s) <= 63 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func text(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
func sameImmutable(a, b copyinventory.Database) bool {
	return a.Encoding == b.Encoding && a.Collation == b.Collation && a.CType == b.CType && reflect.DeepEqual(a.LocaleProvider, b.LocaleProvider) && reflect.DeepEqual(a.Locale, b.Locale) && reflect.DeepEqual(a.ICURules, b.ICURules) && reflect.DeepEqual(a.CollationVersion, b.CollationVersion)
}
func catalogueValid(c privateCatalogue) bool {
	if c.PostgresMajor < 16 || c.DatabaseOID == 0 || c.RoleOID == 0 || len(c.Databases) == 0 || len(c.Tablespaces) == 0 {
		return false
	}
	ids := map[uint32]bool{}
	names := map[string]bool{}
	found := false
	for _, d := range c.Databases {
		if d.OID == 0 || ids[d.OID] || !name(d.Name) || names[d.Name] || d.OwnerOID == 0 || !name(d.Owner) || d.Encoding < 0 || d.ConnectionLimit < -1 || !text(d.Collation) || !text(d.CType) {
			return false
		}
		for _, s := range []*string{d.LocaleProvider, d.Locale, d.ICURules, d.CollationVersion} {
			if s != nil && !text(*s) {
				return false
			}
		}
		if d.LocaleProvider == nil || (*d.LocaleProvider != "c" && *d.LocaleProvider != "i") || (*d.LocaleProvider == "i" && (d.Locale == nil || *d.Locale == "")) || (*d.LocaleProvider == "c" && (d.Locale != nil || d.ICURules != nil)) {
			return false
		}
		ids[d.OID] = true
		names[d.Name] = true
		found = found || d.OID == c.DatabaseOID
	}
	if !found {
		return false
	}
	spaces := map[uint32]bool{}
	spaceNames := map[string]bool{}
	for _, s := range c.Tablespaces {
		if s.OID == 0 || spaces[s.OID] || !name(s.Name) || spaceNames[s.Name] || s.OwnerOID == 0 || !name(s.Owner) {
			return false
		}
		spaces[s.OID] = true
		spaceNames[s.Name] = true
	}
	for _, d := range c.Databases {
		if !spaces[d.TablespaceOID] {
			return false
		}
	}
	return true
}
func (p Plan) validate(source copyinventory.ExportPlan) error {
	if err := p.validateBody(); err != nil {
		return err
	}
	c, err := source.DatabaseCatalogueForWorker()
	if err != nil {
		return err
	}
	other := payload{Source: privateCatalogue(c)}
	canonical(&other)
	if !reflect.DeepEqual(other.Source, p.body.Source) {
		return pgerrors.ErrConflict
	}
	seed, err := copyroles.RecoverReceiptForWorker(source, copyarchive.RestoreTarget(p.body.Target), p.body.RoleProof)
	if err != nil {
		return err
	}
	ids := map[uint32]uint32{}
	for _, id := range seed.IdentitiesForWorker() {
		ids[id.SourceOID] = id.TargetOID
	}
	for _, d := range p.body.Source.Databases {
		if ids[d.OwnerOID] == 0 {
			return pgerrors.ErrConflict
		}
	}
	for _, m := range p.body.Tablespaces {
		var a, b copyinventory.Tablespace
		for _, s := range p.body.Source.Tablespaces {
			if s.OID == m.SourceOID {
				a = s
			}
		}
		for _, s := range p.body.Baseline.Tablespaces {
			if s.OID == m.TargetOID {
				b = s
			}
		}
		if b.OwnerOID != ids[a.OwnerOID] {
			return pgerrors.ErrConflict
		}
	}
	return nil
}
func (p Plan) validateBody() error {
	t := copyarchive.RestoreTarget(p.body.Target)
	fp, err := t.Fingerprint()
	if err != nil || p.body.Version != 1 || fp != p.body.TargetFingerprint || p.body.InventoryFingerprint != p.body.Source.InventoryFingerprint || !catalogueValid(p.body.Source) || !catalogueValid(p.body.Baseline) || p.body.Source.PostgresMajor != t.Scope.PostgresMajor || p.body.Baseline.PostgresMajor != t.Scope.PostgresMajor || p.body.Baseline.DatabaseOID != t.DatabaseOID || p.body.Baseline.RoleOID != t.RoleOID {
		return pgerrors.ErrConflict
	}
	if len(p.body.Databases) != len(p.body.Source.Databases) || len(p.body.Tablespaces) != len(p.body.Source.Tablespaces) {
		return pgerrors.ErrConflict
	}
	spaceMap := map[uint32]uint32{}
	targetSpaces := map[uint32]bool{}
	for _, m := range p.body.Tablespaces {
		if m.SourceOID == 0 || m.TargetOID == 0 || spaceMap[m.SourceOID] != 0 || targetSpaces[m.TargetOID] {
			return pgerrors.ErrConflict
		}
		var a, b copyinventory.Tablespace
		for _, s := range p.body.Source.Tablespaces {
			if s.OID == m.SourceOID {
				a = s
			}
		}
		for _, s := range p.body.Baseline.Tablespaces {
			if s.OID == m.TargetOID {
				b = s
			}
		}
		if a.OID == 0 || b.OID == 0 || a.Name != b.Name || a.Owner != b.Owner || !reflect.DeepEqual(a.ACL, b.ACL) || !reflect.DeepEqual(a.Options, b.Options) {
			return pgerrors.ErrUnsupported
		}
		spaceMap[m.SourceOID] = m.TargetOID
		targetSpaces[m.TargetOID] = true
	}
	seen := map[uint32]bool{}
	sourceIDs := map[uint32]bool{}
	for _, d := range p.body.Source.Databases {
		sourceIDs[d.OID] = true
	}
	targets := map[uint32]bool{}
	for _, choice := range p.body.Databases {
		var src copyinventory.Database
		for _, d := range p.body.Source.Databases {
			if d.OID == choice.SourceOID {
				src = d
			}
		}
		if src.OID == 0 || seen[src.OID] || (choice.ExistingTargetOID == 0) == (choice.CreateTargetOID == 0) {
			return pgerrors.ErrConflict
		}
		seen[src.OID] = true
		id := choice.ExistingTargetOID
		if id == 0 {
			id = choice.CreateTargetOID
		}
		if targets[id] {
			return pgerrors.ErrConflict
		}
		targets[id] = true
		var byName, byID copyinventory.Database
		for _, d := range p.body.Baseline.Databases {
			if d.Name == src.Name {
				byName = d
			}
			if d.OID == id {
				byID = d
			}
		}
		if choice.ExistingTargetOID != 0 {
			if byID.OID == 0 || byName.OID != id || byID.TablespaceOID != spaceMap[src.TablespaceOID] || !sameImmutable(src, byID) {
				return pgerrors.ErrUnsupported
			}
		} else if id < firstNormalOID || sourceIDs[id] || byName.OID != 0 || byID.OID != 0 {
			return pgerrors.ErrConflict
		}
	}
	return nil
}
func (p Plan) PrivatePayloadForSealing() ([]byte, error) {
	if err := p.validateBody(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p.body)
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return nil, pgerrors.ErrQuotaExceeded
	}
	return raw, err
}
func (p Plan) creationDatabase(sourceOID uint32) (copyinventory.Database, bool, error) {
	var source copyinventory.Database
	var choice privateDisposition
	for _, d := range p.body.Source.Databases {
		if d.OID == sourceOID {
			source = d
		}
	}
	for _, d := range p.body.Databases {
		if d.SourceOID == sourceOID {
			choice = d
		}
	}
	if source.OID == 0 {
		return source, false, pgerrors.ErrNotFound
	}
	if choice.ExistingTargetOID != 0 {
		for _, d := range p.body.Baseline.Databases {
			if d.OID == choice.ExistingTargetOID {
				return d, true, nil
			}
		}
	}
	source.OID = choice.CreateTargetOID
	source.OwnerOID = p.body.Target.RoleOID
	source.Owner = p.body.Target.RoleName
	source.AllowConnections = false
	source.Template = false
	source.ACL = nil
	for _, m := range p.body.Tablespaces {
		if m.SourceOID == source.TablespaceOID {
			source.TablespaceOID = m.TargetOID
			break
		}
	}
	return source, false, nil
}
