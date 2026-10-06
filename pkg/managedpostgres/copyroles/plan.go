// Package copyroles privately seeds an independent target's role identities from
// every captured role. Memberships, scoped settings, credentials and readiness
// require subsequent protocols. Original LOGIN intent is held until activation.
package copyroles

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// Disposition must enumerate every source role. Existing roles retain exact
// names and logical attributes; a new role must not collide with the baseline.
// A name match alone never authorizes adoption. Provider/private remapping with
// different semantics needs a separately qualified strategy.
type Disposition struct {
	SourceOID         uint32
	ExistingTargetOID uint32 // zero means create; nonzero requires exact identity
}

func (Disposition) String() string     { return "private PostgreSQL role disposition" }
func (d Disposition) GoString() string { return d.String() }
func (d Disposition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Existing bool }{d.ExistingTargetOID != 0})
}

type privateTarget copyarchive.RestoreTarget
type seedRole struct {
	Source            copyinventory.Role `json:"source"`
	ExistingTargetOID uint32             `json:"existing_target_oid"`
}
type payload struct {
	Version              int                  `json:"version"`
	Target               privateTarget        `json:"target"`
	TargetFingerprint    string               `json:"target_fingerprint"`
	InventoryFingerprint string               `json:"inventory_fingerprint"`
	Baseline             []copyinventory.Role `json:"baseline"`
	Roles                []seedRole           `json:"roles"`
}

// Plan is immutable and redacted. Persist its private input through encryption
// before authorizing dispatch. Recover/reuse that exact input after lost replies;
// rebuilding it from a changed target catalogue cannot recover ownership.
type Plan struct{ body payload }
type Summary struct{ Roles, Existing, Created, DeferredLogins int }

func (p Plan) Summary() Summary {
	s := Summary{Roles: len(p.body.Roles)}
	for _, r := range p.body.Roles {
		if r.ExistingTargetOID != 0 {
			s.Existing++
		} else {
			s.Created++
			if r.Source.Login {
				s.DeferredLogins++
			}
		}
	}
	return s
}
func (Plan) String() string                 { return "private PostgreSQL role seed plan" }
func (p Plan) GoString() string             { return p.String() }
func (p Plan) MarshalJSON() ([]byte, error) { return json.Marshal(p.Summary()) }

// NewPlan requires an independently authenticated target catalogue and the
// committed bootstrap SQL pins. It neither authenticates provider placement nor
// reserves durable dispatch. Unsupported mapping fails before any mutation.
func NewPlan(source copyinventory.ExportPlan, target copyinventory.Inventory, pins copyarchive.RestoreTarget, dispositions []Disposition) (Plan, error) {
	fingerprint, err := pins.Fingerprint()
	if err != nil {
		return Plan{}, err
	}
	requirements, err := source.RequirementsForWorker()
	if err != nil {
		return Plan{}, err
	}
	if len(requirements) == 0 || !requirements[0].Scope.Equal(pins.Scope) {
		return Plan{}, pgerrors.ErrConflict
	}
	src, err := source.RoleCatalogueForWorker()
	if err != nil {
		return Plan{}, err
	}
	base, err := target.RoleCatalogueForWorker()
	if err != nil {
		return Plan{}, err
	}
	if src.PostgresMajor != pins.Scope.PostgresMajor || base.PostgresMajor != pins.Scope.PostgresMajor || base.DatabaseOID != pins.DatabaseOID || base.RoleOID != pins.RoleOID {
		return Plan{}, pgerrors.ErrConflict
	}
	if pins.Scope.PostgresMajor < 16 {
		return Plan{}, pgerrors.ErrUnsupported
	}
	p := Plan{body: payload{Version: 1, Target: privateTarget(pins), TargetFingerprint: fingerprint, InventoryFingerprint: src.InventoryFingerprint, Baseline: base.Roles}}
	choices := make(map[uint32]uint32, len(dispositions))
	for _, d := range dispositions {
		if _, duplicate := choices[d.SourceOID]; d.SourceOID == 0 || duplicate {
			return Plan{}, pgerrors.ErrConflict
		}
		choices[d.SourceOID] = d.ExistingTargetOID
	}
	if len(choices) != len(src.Roles) {
		return Plan{}, pgerrors.ErrConflict
	}
	for _, r := range src.Roles {
		id, ok := choices[r.OID]
		if !ok {
			return Plan{}, pgerrors.ErrConflict
		}
		p.body.Roles = append(p.body.Roles, seedRole{Source: r, ExistingTargetOID: id})
	}
	slices.SortFunc(p.body.Baseline, func(a, b copyinventory.Role) int { return compareOID(a.OID, b.OID) })
	slices.SortFunc(p.body.Roles, func(a, b seedRole) int { return compareOID(a.Source.OID, b.Source.OID) })
	if err := p.validate(); err != nil {
		return Plan{}, err
	}
	return p, nil
}

func compareOID(a, b uint32) int {
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
func digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}
func validRole(r copyinventory.Role) bool {
	if r.OID == 0 || !name(r.Name) || r.ConnectionLimit < -1 {
		return false
	}
	if r.ValidUntil != nil && (!utf8.ValidString(*r.ValidUntil) || strings.ContainsRune(*r.ValidUntil, 0) || *r.ValidUntil == "") {
		return false
	}
	seen := map[string]bool{}
	for _, c := range r.Config {
		key, value, ok := strings.Cut(c, "=")
		if !ok || !name(key) || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
func sameRole(a, b copyinventory.Role) bool {
	a.OID, b.OID = 0, 0
	if len(a.Config) == 0 {
		a.Config = nil
	}
	if len(b.Config) == 0 {
		b.Config = nil
	}
	return reflect.DeepEqual(a, b)
}

func (p Plan) validate() error {
	t := copyarchive.RestoreTarget(p.body.Target)
	fingerprint, err := t.Fingerprint()
	if err != nil || p.body.Version != 1 || fingerprint != p.body.TargetFingerprint || !digest(p.body.InventoryFingerprint) || len(p.body.Roles) == 0 || len(p.body.Baseline) == 0 {
		return pgerrors.ErrInvalid
	}
	if t.Scope.PostgresMajor < 16 {
		return pgerrors.ErrUnsupported
	}
	byOID, byName := map[uint32]copyinventory.Role{}, map[string]copyinventory.Role{}
	for _, r := range p.body.Baseline {
		if !validRole(r) || byOID[r.OID].OID != 0 || byName[r.Name].OID != 0 {
			return pgerrors.ErrConflict
		}
		byOID[r.OID], byName[r.Name] = r, r
	}
	if r := byOID[t.RoleOID]; r.Name != t.RoleName || !r.Login {
		return pgerrors.ErrConflict
	}
	seen := map[uint32]bool{}
	sourceNames := map[string]bool{}
	for _, r := range p.body.Roles {
		if !validRole(r.Source) || seen[r.Source.OID] || sourceNames[r.Source.Name] {
			return pgerrors.ErrConflict
		}
		seen[r.Source.OID] = true
		sourceNames[r.Source.Name] = true
		if r.ExistingTargetOID != 0 {
			base, ok := byOID[r.ExistingTargetOID]
			if !ok || !sameRole(base, r.Source) {
				return pgerrors.ErrUnsupported
			}
		} else if byName[r.Source.Name].OID != 0 || strings.HasPrefix(r.Source.Name, "pg_") {
			return pgerrors.ErrConflict
		}
	}
	raw, err := json.Marshal(p.body)
	if err != nil {
		return pgerrors.ErrInvalid
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return pgerrors.ErrQuotaExceeded
	}
	return nil
}

// PrivatePayloadForSealing is sensitive metadata, never ordinary JSON/log data.
// The caller must retain it encrypted with the exact operation and target pins.
func (p Plan) PrivatePayloadForSealing() ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p.body)
}
