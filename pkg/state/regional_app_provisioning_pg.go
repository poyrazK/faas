package state

// adr: 797
import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func provisionFromRow(r sqlc.RegionalAppProvisionReceiptRow) RegionalAppProvision {
	return RegionalAppProvision{ProvisionID: r.ProvisionID, GlobalAppID: r.GlobalAppID, CellID: r.CellID, Region: r.Region, AccountID: r.AccountID, AppID: r.AppID, AppSlug: r.AppSlug, Request: r.Request, CreatedAt: r.CreatedAt.Time}
}

func (s *PgStore) RegionalAppProvisionReceipt(ctx context.Context, cell, account, id string) (RegionalAppProvision, error) {
	r, err := (&sqlc.Queries{}).RegionalAppProvisionReceipt(ctx, s.pool, idArgProvision(id))
	p := provisionFromRow(r)
	if err != nil {
		return p, mapErr(err)
	}
	if p.CellID != cell || p.AccountID != regionalQuotaCanonicalID(account) {
		return RegionalAppProvision{}, ErrNotFound
	}
	p.Request, err = canonicalProvisionRequest(p.Request)
	return p, err
}

func idArgProvision(id string) pgtype.UUID {
	var out pgtype.UUID
	_ = out.Scan(id)
	return out
}

func (s *PgStore) CreateRegionalAppProvision(ctx context.Context, p RegionalAppProvision, app App, limits api.Limits, entry OrgActivity) (RegionalAppProvision, bool, int64, error) {
	p.AccountID = regionalQuotaCanonicalID(p.AccountID)
	if !p.valid() || p.AccountID != regionalQuotaCanonicalID(app.AccountID) || p.AppSlug != app.Slug || p.Region != app.ComputeRegion {
		return RegionalAppProvision{}, false, 0, ErrInvalidArgument
	}
	request, err := canonicalProvisionRequest(p.Request)
	if err != nil {
		return RegionalAppProvision{}, false, 0, err
	}
	p.Request = request
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RegionalAppProvision{}, false, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if _, err := q.LockRegionalProvisionAccount(ctx, tx, idArgProvision(p.AccountID)); err != nil {
		return RegionalAppProvision{}, false, 0, mapErr(err)
	}
	old, err := q.RegionalAppProvisionReceipt(ctx, tx, idArgProvision(p.ProvisionID))
	if err == nil {
		prior := provisionFromRow(old)
		prior.Request, err = canonicalProvisionRequest(prior.Request)
		if err != nil || !p.matches(prior) {
			return RegionalAppProvision{}, false, 0, ErrConflict
		}
		return prior, false, 0, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RegionalAppProvision{}, false, 0, mapErr(err)
	}
	cell, err := q.ReadControlPlaneCell(ctx, tx)
	if err != nil || cell.CellID != p.CellID || cell.ComputeRegion != p.Region {
		return RegionalAppProvision{}, false, 0, ErrConflict
	}
	created, err := createAppIfUnderQuotaTx(ctx, tx, app, limits)
	if err != nil {
		return RegionalAppProvision{}, false, 0, err
	}
	entry, err = bindOrgActivityToApp(entry, created)
	if err != nil {
		return RegionalAppProvision{}, false, 0, err
	}
	outbox, err := enqueueOrgActivityOutboxTx(ctx, tx, entry)
	if err != nil {
		return RegionalAppProvision{}, false, 0, err
	}
	p.AppID, p.CreatedAt = created.ID, time.Now().UTC().Truncate(time.Microsecond)
	err = q.InsertRegionalAppProvisionReceipt(ctx, tx, sqlc.InsertRegionalAppProvisionReceiptParams{ProvisionID: idArgProvision(p.ProvisionID), GlobalAppID: idArgProvision(p.GlobalAppID), CellID: idArgProvision(p.CellID), Region: p.Region, AccountID: idArgProvision(p.AccountID), AppID: idArgProvision(p.AppID), AppSlug: p.AppSlug, Request: p.Request, CreatedAt: pgtype.Timestamptz{Time: p.CreatedAt, Valid: true}})
	if err != nil {
		return RegionalAppProvision{}, false, 0, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RegionalAppProvision{}, false, 0, err
	}
	return p, true, outbox, nil
}

var _ RegionalAppProvisionStore = (*PgStore)(nil)

func (s *PgStore) RegionalAppProvisioned(ctx context.Context, id string) (bool, error) {
	id = regionalQuotaCanonicalID(id)
	if id == "" {
		return false, ErrInvalidArgument
	}
	managed, err := (&sqlc.Queries{}).RegionalAppProvisioned(ctx, s.pool, idArgProvision(id))
	return managed, mapErr(err)
}
