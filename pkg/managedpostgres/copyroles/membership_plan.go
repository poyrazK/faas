package copyroles

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type membershipPayload struct {
	Version           int                        `json:"version"`
	SeedPlan          payload                    `json:"seed_plan"`
	SeedReceipt       privateReceipt             `json:"seed_receipt"`
	SourceMemberships []copyinventory.Membership `json:"source_memberships"`
	Baseline          []copyinventory.Membership `json:"baseline"`
	Desired           []copyinventory.Membership `json:"desired"`
}

// MembershipPlan freezes the entire grant graph and the exact seeded role OIDs.
// Retain this plan encrypted before dispatch. It never enables source logins or
// authorizes provider placement, target publication or stage readiness.
type MembershipPlan struct{ body membershipPayload }
type MembershipSummary struct{ Before, Desired int }

func (p MembershipPlan) Summary() MembershipSummary {
	return MembershipSummary{len(p.body.Baseline), len(p.body.Desired)}
}
func (MembershipPlan) String() string                 { return "private PostgreSQL membership plan" }
func (p MembershipPlan) GoString() string             { return p.String() }
func (p MembershipPlan) MarshalJSON() ([]byte, error) { return json.Marshal(p.Summary()) }

// NewMembershipPlan requires the original source export input, authenticated
// role-seed receipt and independently authenticated post-seeding target catalogue.
// All source grants and target bootstrap grants remain explicit. Exact final
// materialization must follow operations needing temporary creator authority.
func NewMembershipPlan(source copyinventory.ExportPlan, seed Receipt, target copyinventory.Inventory) (MembershipPlan, error) {
	if seed.validate() != nil {
		return MembershipPlan{}, pgerrors.ErrInvalid
	}
	requirements, err := source.RequirementsForWorker()
	if err != nil {
		return MembershipPlan{}, err
	}
	if len(requirements) == 0 || !requirements[0].Scope.Equal(seed.plan.body.Target.Scope) {
		return MembershipPlan{}, pgerrors.ErrConflict
	}
	src, err := source.MembershipCatalogueForWorker()
	if err != nil {
		return MembershipPlan{}, err
	}
	base, err := target.MembershipCatalogueForWorker()
	if err != nil {
		return MembershipPlan{}, err
	}
	if !membershipSourceMatchesSeed(src, seed) || base.Roles.PostgresMajor != src.Roles.PostgresMajor || base.Roles.DatabaseOID != seed.plan.body.Target.DatabaseOID || base.Roles.RoleOID != seed.plan.body.Target.RoleOID ||
		!sameRoleCatalogue(base.Roles.Roles, seed.expectedRoles()) {
		return MembershipPlan{}, pgerrors.ErrConflict
	}
	p := MembershipPlan{body: membershipPayload{Version: 1, SeedPlan: seed.plan.body, SeedReceipt: seed.body, SourceMemberships: src.Memberships, Baseline: base.Memberships}}
	p.body.Desired, err = mappedMemberships(seed, p.body.SourceMemberships)
	if err != nil {
		return MembershipPlan{}, err
	}
	sortMemberships(p.body.SourceMemberships)
	sortMemberships(p.body.Baseline)
	sortMemberships(p.body.Desired)
	raw, err := p.PrivatePayloadForSealing()
	if err != nil {
		return MembershipPlan{}, err
	}
	// Also detach the embedded role plan/receipt from returned worker values.
	if json.Unmarshal(raw, &p.body) != nil {
		return MembershipPlan{}, pgerrors.ErrConflict
	}
	return p, nil
}

func (r Receipt) expectedRoles() []copyinventory.Role {
	roles := slices.Clone(r.plan.body.Baseline)
	bySource := map[uint32]uint32{}
	for _, id := range r.body.Created {
		bySource[id.SourceOID] = id.TargetOID
	}
	for _, role := range r.plan.body.Roles {
		if id := bySource[role.Source.OID]; id != 0 {
			x := role.Source
			x.OID, x.Login = id, false
			roles = append(roles, x)
		}
	}
	return roles
}

func sameRoleCatalogue(a, b []copyinventory.Role) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.SortFunc(a, func(a, b copyinventory.Role) int { return compareOID(a.OID, b.OID) })
	slices.SortFunc(b, func(a, b copyinventory.Role) int { return compareOID(a.OID, b.OID) })
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n].OID != b[n].OID || !sameRole(a[n], b[n]) {
			return false
		}
	}
	return true
}

func membershipSourceMatchesSeed(source copyinventory.MembershipCatalogue, seed Receipt) bool {
	if source.Roles.PostgresMajor != seed.plan.body.Target.Scope.PostgresMajor || source.Roles.InventoryFingerprint != seed.plan.body.InventoryFingerprint {
		return false
	}
	roles := make([]copyinventory.Role, 0, len(seed.plan.body.Roles))
	for _, x := range seed.plan.body.Roles {
		roles = append(roles, x.Source)
	}
	return sameRoleCatalogue(source.Roles.Roles, roles)
}

func sortMemberships(rows []copyinventory.Membership) {
	slices.SortFunc(rows, func(a, b copyinventory.Membership) int {
		for _, pair := range [][2]uint32{{a.RoleOID, b.RoleOID}, {a.MemberOID, b.MemberOID}, {a.GrantorOID, b.GrantorOID}} {
			if c := compareOID(pair[0], pair[1]); c != 0 {
				return c
			}
		}
		return 0
	})
}

func validMemberships(rows []copyinventory.Membership, roles []copyinventory.Role) bool {
	names := map[uint32]string{}
	for _, role := range roles {
		names[role.OID] = role.Name
	}
	seen := map[[3]uint32]bool{}
	graph := map[uint32][]uint32{}
	for _, m := range rows {
		key := [3]uint32{m.RoleOID, m.MemberOID, m.GrantorOID}
		if m.RoleOID == 0 || m.MemberOID == 0 || m.GrantorOID == 0 || m.RoleOID == m.MemberOID || seen[key] || names[m.RoleOID] != m.Role || names[m.MemberOID] != m.Member || names[m.GrantorOID] != m.Grantor || m.Role == "" || m.Member == "" || m.Grantor == "" {
			return false
		}
		seen[key] = true
		graph[m.MemberOID] = append(graph[m.MemberOID], m.RoleOID)
	}
	status := map[uint32]uint8{}
	var visit func(uint32) bool
	visit = func(id uint32) bool {
		if status[id] == 1 {
			return false
		}
		if status[id] == 2 {
			return true
		}
		status[id] = 1
		for _, parent := range graph[id] {
			if !visit(parent) {
				return false
			}
		}
		status[id] = 2
		return true
	}
	for id := range graph {
		if !visit(id) {
			return false
		}
	}
	return true
}

func mappedMemberships(seed Receipt, source []copyinventory.Membership) ([]copyinventory.Membership, error) {
	roles := make([]copyinventory.Role, 0, len(seed.plan.body.Roles))
	for _, role := range seed.plan.body.Roles {
		roles = append(roles, role.Source)
	}
	if !validMemberships(source, roles) {
		return nil, pgerrors.ErrConflict
	}
	ids := map[uint32]uint32{}
	for _, id := range seed.IdentitiesForWorker() {
		ids[id.SourceOID] = id.TargetOID
	}
	desired := slices.Clone(source)
	for n := range desired {
		desired[n].RoleOID, desired[n].MemberOID, desired[n].GrantorOID = ids[source[n].RoleOID], ids[source[n].MemberOID], ids[source[n].GrantorOID]
	}
	sortMemberships(desired)
	return desired, nil
}

func (p MembershipPlan) validate() error {
	seed := Receipt{plan: Plan{body: p.body.SeedPlan}, body: p.body.SeedReceipt}
	if p.body.Version != 1 || seed.validate() != nil || !validMemberships(p.body.Baseline, seed.expectedRoles()) || !validMemberships(p.body.Desired, seed.expectedRoles()) {
		return pgerrors.ErrConflict
	}
	desired, err := mappedMemberships(seed, p.body.SourceMemberships)
	if err != nil || !reflect.DeepEqual(desired, p.body.Desired) {
		return pgerrors.ErrConflict
	}
	return nil
}

func (p MembershipPlan) PrivatePayloadForSealing() ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p.body)
	if err != nil {
		return nil, pgerrors.ErrInvalid
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return nil, pgerrors.ErrQuotaExceeded
	}
	return raw, nil
}
