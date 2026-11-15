package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func evolutionID(p Pipeline, c api.DataPipelineEvolution) string {
	c.ID, c.State = "", ""
	c.Retry = 0
	b, _ := json.Marshal(struct {
		Account, Pipeline string
		Change            api.DataPipelineEvolution
	}{p.AccountID, p.ID, c})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Authoritative memberships and their expiry are observations, not owner
// configuration. Routine reconciliation must not invalidate a reviewed mapping.
func sameEvolutionIntent(a, b *api.DataPipelineServing) bool {
	a, b = normalizeServing(a), normalizeServing(b)
	for _, s := range []*api.DataPipelineServing{a, b} {
		if s != nil && s.AuthOrganizations != nil && s.TenantAccess != nil {
			s.TenantAccess.Memberships = nil
			s.TenantAccess.ValidUntil = ""
		}
	}
	return sameServing(a, b)
}

// CanEvolve describes when another owner preview can be reviewed.
func CanEvolve(p Pipeline) bool {
	repairAuth := p.State == "blocked" && p.Reason == "auth_membership_unconfirmed" && p.Serving != nil && p.Serving.AuthOrganizations != nil
	repairRelationship := p.State == "blocked" && p.Reason == "relationship_contract_unavailable"
	return p.Initialized && (p.State == "live" || repairAuth || repairRelationship) && p.DesiredState == "running" && !p.VerifyRequested &&
		(p.SchemaChange == nil || p.SchemaChange.State == "complete" || p.SchemaChange.State == "withdrawn") &&
		(p.ReaderChange == nil || p.ReaderChange.State == "applied") &&
		(p.Evolution == nil || p.Evolution.State != "approved" || repairAuth && p.Evolution.Kind == "auth_organizations" || repairRelationship && p.Evolution.Kind == "relationships") && !p.ServingInvalid
}

func evolutionPlan(p Pipeline, r api.DataPipelineEvolutionRequest) (*api.DataPipelineEvolution, error) {
	if !CanEvolve(p) {
		return nil, ErrConflict
	}
	repairAuth := p.Reason == "auth_membership_unconfirmed" && r.Kind == "auth_organizations"
	repairRelationship := p.Reason == "relationship_contract_unavailable" && r.Kind == "relationships"
	if p.State != "live" && !repairAuth && !repairRelationship {
		return nil, ErrConflict
	}
	if snapshotMode(p) && r.Kind == "projection" {
		return nil, ErrInvalid
	}
	after := normalizeServing(p.Serving)
	if after == nil {
		after = &api.DataPipelineServing{}
	}
	after.Version = 2
	c := &api.DataPipelineEvolution{State: "preview", Kind: r.Kind, Generation: p.Generation, PreviewRevision: p.Revision + 1, Before: normalizeServing(p.Serving), After: after}
	switch r.Kind {
	case "projection":
		if p.Serving == nil || len(p.Serving.Columns) == 0 || len(r.Columns) == 0 || r.AuthOrganizations != nil || len(r.Relationships) > 0 {
			return nil, ErrInvalid
		}
		after.Columns = r.Columns
		changed := false
		for _, table := range p.Tables {
			before := p.Serving.Columns[table]
			for _, col := range before {
				if !slices.Contains(r.Columns[table], col) {
					return nil, ErrInvalid
				}
			}
			changed = changed || len(before) != len(r.Columns[table])
		}
		for table := range r.Columns {
			if !slices.Contains(p.Tables, table) {
				c.AddedTables = append(c.AddedTables, table)
			}
		}
		slices.Sort(c.AddedTables)
		// Separate source-schema and table-copy receipts remain independently reviewable.
		if changed && len(c.AddedTables) > 0 || !changed && len(c.AddedTables) == 0 {
			return nil, ErrInvalid
		}
	case "auth_organizations":
		if tenantAccess(p) == nil || r.AuthOrganizations == nil || len(r.Columns) > 0 || len(r.Relationships) > 0 {
			return nil, ErrInvalid
		}
		after.AuthOrganizations = r.AuthOrganizations
	case "relationships":
		if len(r.Columns) > 0 || r.AuthOrganizations != nil {
			return nil, ErrInvalid
		}
		after.Relationships = r.Relationships
	default:
		return nil, ErrInvalid
	}
	c.After = normalizeServing(after)
	tables := append(slices.Clone(p.Tables), c.AddedTables...)
	if validatePipeline(api.CreateDataPipelineRequest{Name: p.Name, SourceConnectionID: p.SourceConnectionID, DatabaseID: p.DatabaseID, Tables: tables, ReadSubjects: p.ReadSubjects}) != nil {
		return nil, ErrInvalid
	}
	if !validServing(c.After, tables) || sameServing(p.Serving, c.After) {
		return nil, ErrInvalid
	}
	c.ID = evolutionID(p, *c)
	return c, nil
}

func validEvolutionServing(s *api.DataPipelineServing, tables []string) bool {
	if s.Version == 1 {
		return s.AuthOrganizations == nil && len(s.Relationships) == 0
	}
	if s.Version != 2 || len(s.Relationships) > api.DataPipelineRelationshipsMax {
		return false
	}
	if a := s.AuthOrganizations; a != nil {
		if s.TenantAccess == nil || !validID(a.AuthAppID) || !validID(a.BindingID) || !validID(a.DatabaseID) || len(a.Mappings) == 0 || len(a.Mappings) > api.DataPipelineOrganizationMappingsMax {
			return false
		}
		seen := map[string]bool{}
		for _, m := range a.Mappings {
			if !validID(m.OrganizationID) || seen[m.OrganizationID] || !validMemberships([]api.DataPipelineTenantMembership{{Subject: "validation", Tenant: m.Tenant}}) {
				return false
			}
			seen[m.OrganizationID] = true
		}
	}
	seen := map[string]bool{}
	for _, r := range s.Relationships {
		if !identifier.MatchString(r.Name) || strings.HasPrefix(r.Name, "gregale_") || seen[r.Name] || slices.Contains(tables, r.Name) || !slices.Contains(tables, r.SourceTable) || !slices.Contains(tables, r.TargetTable) || len(r.SourceColumns) == 0 || len(r.SourceColumns) > api.DataPipelineRelationshipKeysMax || len(r.SourceColumns) != len(r.TargetColumns) {
			return false
		}
		seen[r.Name] = true
		for _, cols := range [][]string{r.SourceColumns, r.TargetColumns} {
			names := map[string]bool{}
			for _, col := range cols {
				if !validServingColumn(col) || names[col] {
					return false
				}
				names[col] = true
			}
		}
		for i, table := range []string{r.SourceTable, r.TargetTable} {
			cols := r.SourceColumns
			if i == 1 {
				cols = r.TargetColumns
			}
			for _, col := range cols {
				if len(s.Columns) > 0 && !slices.Contains(s.Columns[table], col) {
					return false
				}
			}
		}
	}
	return true
}

func (s *Service) Evolve(ctx context.Context, account, id string, r api.DataPipelineEvolutionRequest) error {
	if err := s.admit(ctx, account); err != nil {
		return err
	}
	if !validID(account) || !validID(id) || r.ExpectedRevision < 1 {
		return ErrInvalid
	}
	p, err := s.Store.Get(ctx, account, id)
	if err != nil {
		return err
	}
	if p.Revision != r.ExpectedRevision {
		return ErrConflict
	}
	if p.ServingInvalid {
		return ErrConflict
	}
	if r.Action == "approve" && p.Evolution != nil && p.Evolution.After.AuthOrganizations != nil {
		if s.ResolveAuthOrganizations == nil {
			return ErrUnavailable
		}
		approved := p.Evolution.After.AuthOrganizations
		current, resolveErr := s.ResolveAuthOrganizations(ctx, account, p.DatabaseID, *approved)
		if resolveErr != nil {
			return resolveErr
		}
		if current.BindingID != approved.BindingID || current.DatabaseID != approved.DatabaseID {
			return ErrConflict
		}
	}
	if r.Action == "preview" && r.Kind == "auth_organizations" {
		if s.ResolveAuthOrganizations == nil || r.AuthOrganizations == nil {
			return ErrUnavailable
		}
		input := *r.AuthOrganizations
		input.AuthAppID = canonicalID(input.AuthAppID)
		r.AuthOrganizations, err = s.ResolveAuthOrganizations(ctx, account, p.DatabaseID, input)
		if err != nil {
			return err
		}
	}
	store, ok := s.Store.(interface {
		Evolve(context.Context, string, string, api.DataPipelineEvolutionRequest) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.Evolve(ctx, account, id, r)
}

func (s *PostgresStore) Evolve(ctx context.Context, account, id string, r api.DataPipelineEvolutionRequest) error {
	p, err := s.Get(ctx, account, id)
	if err != nil {
		return err
	}
	if p.Revision != r.ExpectedRevision || r.Action != "retry" && !CanEvolve(p) {
		return ErrConflict
	}
	c := p.Evolution
	serving, plan := servingJSON(p.Serving), []byte("{}")
	switch r.Action {
	case "retry":
		if r.Kind != "" || len(r.Columns) > 0 || r.AuthOrganizations != nil || len(r.Relationships) > 0 {
			return ErrInvalid
		}
		if c == nil || c.ID != r.ChangeID || !slices.Contains([]string{"approved", "applied", "superseded"}, c.State) || p.Serving == nil || (len(p.Serving.Relationships) == 0 && c.Kind != "relationships") || p.Reason != "relationship_api_refresh_failed" || c.Retry >= api.DataPipelineSchemaRetriesMax {
			return ErrConflict
		}
		copy := *c
		c = &copy
		c.Retry++
	case "preview":
		c, err = evolutionPlan(p, r)
		if err != nil {
			return err
		}
	case "approve":
		if r.Kind != "" || len(r.Columns) > 0 || r.AuthOrganizations != nil || len(r.Relationships) > 0 {
			return ErrInvalid
		}
		if c == nil || c.State != "preview" || c.ID != r.ChangeID || c.Generation != p.Generation || c.PreviewRevision != p.Revision || c.ID != evolutionID(p, *c) || !sameEvolutionIntent(c.Before, p.Serving) {
			return ErrConflict
		}
		copy := *c
		c = &copy
		c.State = "approved"
		after := normalizeServing(c.After)
		if after.AuthOrganizations != nil && tenantAccess(p) != nil {
			after.TenantAccess.Memberships = slices.Clone(tenantAccess(p).Memberships)
			after.TenantAccess.ValidUntil = tenantAccess(p).ValidUntil
		}
		serving = servingJSON(after)
		if len(c.AddedTables) > 0 {
			plan, _ = json.Marshal(SchemaChange{SelectionID: uuid.NewString(), State: "requested", AddedTables: c.AddedTables, RequestedTables: sortedTables(p.Tables, c.AddedTables)})
		}
	case "discard":
		if c == nil || c.State != "preview" || r.ChangeID != c.ID {
			return ErrConflict
		}
		c = nil
	default:
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if (r.Action == "preview" || r.Action == "approve") && c != nil && c.Kind == "relationships" {
		if err = s.checkedRelationships(ctx, tx, p, c.After); err != nil {
			return err
		}
	}
	if r.Action == "approve" && c.Kind == "projection" {
		candidate := p
		candidate.Serving = c.After
		if err = s.checkedTables(ctx, tx, candidate, sortedTables(p.Tables, c.AddedTables)); err != nil {
			return err
		}
	}
	encoded := []byte("{}")
	if c != nil {
		encoded, _ = json.Marshal(c)
	}
	var readers []byte
	if r.Action == "approve" && c.Kind == "auth_organizations" {
		// Retire manual or previously synchronized grants before accepting a
		// proof from the newly approved organization mapping.
		reset := authReaderChange(p, nil, "1970-01-01T00:00:00Z", "")
		reset.ResetAuth = true
		reset.ID = readerChangeID(p, *reset)
		readers, _ = json.Marshal(reset)
	}
	n, err := s.q.PipelineEvolutionSave(ctx, tx, sqlc.PipelineEvolutionSaveParams{AccountID: account, ID: id, Revision: r.ExpectedRevision, Evolution: encoded, Serving: serving, Plan: plan, Approve: r.Action == "approve", ReaderChange: readers, Retry: r.Action == "retry"})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return storeError(tx.Commit(ctx))
}

func sortedTables(before, added []string) []string {
	out := append(slices.Clone(before), added...)
	slices.Sort(out)
	return out
}
