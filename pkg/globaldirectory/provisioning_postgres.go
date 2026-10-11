package globaldirectory

// adr: 797
import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func provisionFromRow(r sqlc.ListGlobalAppProvisioningRow) (AppProvision, error) {
	var req api.CreateAppRequest
	if err := json.Unmarshal(r.Request, &req); err != nil {
		return AppProvision{}, err
	}
	raw, err := json.Marshal(req)
	return AppProvision{AppProvisionSpec: AppProvisionSpec{Region: r.Region, AccountID: r.RegionalAccountID, Request: raw}, ID: r.ProvisionID, GlobalAppID: r.GlobalAppID, Cell: Cell{ID: r.CellID, Region: r.Region, APIBase: r.ApiBase, Generation: r.CellGeneration, Enabled: true}, Status: r.Status}, err
}

func (s *PgStore) ListAppProvisioning(ctx context.Context, owner, slug string) ([]AppProvision, error) {
	if _, err := s.AppBySlug(ctx, owner, slug); err != nil {
		return nil, err
	}
	return s.listAppProvisioning(ctx, s.pool, owner, slug)
}

func (s *PgStore) listAppProvisioning(ctx context.Context, db sqlc.DBTX, owner, slug string) ([]AppProvision, error) {
	rows, err := (&sqlc.Queries{}).ListGlobalAppProvisioning(ctx, db, sqlc.ListGlobalAppProvisioningParams{OwnerID: idArg(owner), Slug: slug})
	if err != nil {
		return nil, directoryError("read provisioning", err)
	}
	out := make([]AppProvision, 0, len(rows))
	for _, r := range rows {
		p, err := provisionFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *PgStore) PlanAppProvisioning(ctx context.Context, owner, slug string, specs []AppProvisionSpec) ([]AppProvision, error) {
	canonical, err := canonicalProvisionSpecs(specs)
	if err != nil {
		return nil, err
	}
	o, err := s.OwnerByID(ctx, owner)
	if err != nil {
		return nil, err
	}
	if o.Shared() {
		return nil, ErrOrgTargetsUnavailable
	}
	var out []AppProvision
	err = s.transaction(ctx, "plan provisioning", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		a, err := q.LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{OwnerID: idArg(owner), Slug: slug})
		if err != nil {
			return err
		}
		old, err := s.listAppProvisioning(ctx, tx, owner, slug)
		if err != nil {
			return err
		}
		byRegion := make(map[string]AppProvision)
		for _, p := range old {
			byRegion[p.Region] = p
		}
		current, err := q.ListGlobalAppTargets(ctx, tx, sqlc.ListGlobalAppTargetsParams{OwnerID: idArg(owner), Slug: slug})
		if err != nil {
			return err
		}
		for _, spec := range canonical {
			if p, ok := byRegion[spec.Region]; ok {
				if !p.matches(spec) {
					return ErrConflict
				}
				out = append(out, p)
				continue
			}
			for _, t := range current {
				if t.Region == spec.Region {
					return ErrConflict
				}
			}
			if len(byRegion) >= api.GlobalImageReleaseTargetsMax {
				return &ReleaseLimitError{Kind: "targets", Limit: api.GlobalImageReleaseTargetsMax, Observed: int64(len(byRegion) + 1)}
			}
			c, err := q.GlobalDirectoryCellByRegion(ctx, tx, spec.Region)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrStaleCell
			}
			if err != nil {
				return err
			}
			cell, err := q.LockGlobalDirectoryCell(ctx, tx, sqlc.LockGlobalDirectoryCellParams{CellID: idArg(c.CellID), Generation: c.Generation})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrStaleCell
			}
			if err != nil {
				return err
			}
			if cell.CellID != spec.VerifiedCell.ID || cell.Generation != spec.VerifiedCell.Generation || cell.ApiBase != spec.VerifiedCell.APIBase || cell.AppsDomain != spec.VerifiedCell.AppsDomain {
				return ErrStaleCell
			}
			if err := q.InsertGlobalOwnerCellBinding(ctx, tx, sqlc.InsertGlobalOwnerCellBindingParams{OwnerID: idArg(owner), CellID: idArg(cell.CellID), RegionalAccountID: idArg(spec.AccountID)}); err != nil {
				return err
			}
			mapped, err := q.ReadGlobalOwnerCellBinding(ctx, tx, sqlc.ReadGlobalOwnerCellBindingParams{OwnerID: idArg(owner), CellID: idArg(cell.CellID)})
			if err != nil {
				return err
			}
			if mapped.RegionalOrgID != "" || mapped.RegionalAccountID != spec.AccountID {
				return ErrConflict
			}
			p := AppProvision{AppProvisionSpec: spec, ID: uuid.NewString(), GlobalAppID: a.GlobalAppID, Cell: Cell{ID: cell.CellID, Region: cell.Region, Generation: cell.Generation, APIBase: cell.ApiBase, Enabled: true}, Status: ProvisionPlanned}
			if err := q.InsertGlobalAppProvisioning(ctx, tx, sqlc.InsertGlobalAppProvisioningParams{GlobalAppID: idArg(p.GlobalAppID), OwnerID: idArg(owner), Region: p.Region, ProvisionID: idArg(p.ID), CellID: idArg(p.Cell.ID), CellGeneration: p.Cell.Generation, ApiBase: p.Cell.APIBase, RegionalAccountID: idArg(p.AccountID), Request: p.Request}); err != nil {
				return err
			}
			byRegion[p.Region] = p
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

func (s *PgStore) AttachProvisionTarget(ctx context.Context, owner, slug, id string, b Binding) (Target, error) {
	if b.Validate() != nil {
		return Target{}, ErrConflict
	}
	return s.attachTargetProvision(ctx, owner, "", slug, b, id)
}

var _ AppProvisionStore = (*PgStore)(nil)
