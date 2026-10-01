// adr: 375
package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CustomDomainRemovalOwnerStore repeats the HTTP-authorized app identity
// after acquiring locks. A reclaimed name cannot detach another app's claim.
type CustomDomainRemovalOwnerStore interface {
	DeleteCustomDomainForApp(context.Context, string, string) error
	DeleteCustomDomainForAppWithActivity(context.Context, string, string, OrgActivity) (int64, error)
}

type trafficDomainRemovalTx struct {
	pgx.Tx
	beforeClaims []trafficDomainClaim
	globalBefore trafficHostAnalysis
	accounts     []string
	appsSuffix   string
	appID        pgtype.UUID
	release      func(context.Context)
}

func trafficDomainRemovalOwners(ctx context.Context, reader sqlc.DBTX, claims []trafficDomainClaim, domain, originalAccount string) ([]string, error) {
	owners := trafficDomainRemovalAccounts(claims, domain)
	owners = append(owners, originalAccount)
	routes, err := sqlc.New().ReadTrafficGlobalRouteAccounts(ctx, reader, api.TrafficPolicyMaxAnalysisInputs)
	if err != nil {
		return nil, fmt.Errorf("state: read global route owners: %w", mapErr(err))
	}
	for _, account := range routes {
		owners = append(owners, account.String())
	}
	sort.Strings(owners)
	owners = slices.Compact(owners)
	if len(owners) > api.TrafficPolicyMaxAnalysisInputs {
		return nil, analysisLimit("inputs", "owners", api.TrafficPolicyMaxAnalysisInputs, int64(len(owners)))
	}
	return owners, nil
}

func (s *PgStore) beginTrafficDomainRemoval(ctx context.Context, domain, expectedApp string) (*trafficDomainRemovalTx, error) {
	owner, err := sqlc.New().ReadDomainTrafficVerificationOwner(ctx, s.pool, sqlc.ReadDomainTrafficVerificationOwnerParams{Domain: domain})
	if err != nil {
		return nil, mapErr(err)
	}
	if expectedApp != "" && owner.AppID != uuidToPgtype(expectedApp) {
		return nil, ErrNotFound
	}
	for {
		guarded, retry, err := s.tryBeginTrafficDomainRemoval(ctx, domain, owner.AccountID, owner.AppID)
		if err != nil || !retry {
			return guarded, err
		}
		timer := time.NewTimer(api.TrafficPolicyMutationLockRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *PgStore) tryBeginTrafficDomainRemoval(ctx context.Context, domain string, originalAccount, originalApp pgtype.UUID) (*trafficDomainRemovalTx, bool, error) {
	var accounts []string
	err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		claims, err := readTrafficDomainClaims(bounded, s.pool)
		if err != nil {
			return err
		}
		accounts, err = trafficDomainRemovalOwners(bounded, s.pool, claims, domain, originalAccount.String())
		return err
	})
	if err != nil {
		return nil, false, err
	}
	keys := []string{globalTrafficRoutesLock}
	for _, account := range accounts {
		keys = append(keys, "gregale.traffic.account.v1:"+account)
	}
	conn, release, busy, err := s.acquireTrafficDomainRemovalSession(ctx, keys)
	if err != nil || busy {
		return nil, busy, err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		release(ctx)
		return nil, false, fmt.Errorf("state: begin domain removal: %w", err)
	}
	guarded := &trafficDomainRemovalTx{Tx: tx, accounts: accounts, appsSuffix: s.trafficAppsSuffix, appID: originalApp, release: release}
	retry := false
	err = boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		for _, account := range accounts {
			if _, err := sqlc.New().LockTrafficPolicyAccount(bounded, tx, uuidToPgtype(account)); err != nil {
				return err
			}
		}
		var err error
		guarded.beforeClaims, err = readTrafficDomainClaims(bounded, tx)
		if err != nil {
			return err
		}
		claim, found := trafficDomainClaimByName(guarded.beforeClaims, domain)
		if !found || claim.App != originalApp.String() {
			return ErrNotFound
		}
		fresh, err := trafficDomainRemovalOwners(bounded, tx, guarded.beforeClaims, domain, originalAccount.String())
		if err != nil {
			return err
		}
		retry = !slices.Equal(accounts, fresh)
		return nil
	})
	if err != nil || retry {
		_ = guarded.Rollback(context.WithoutCancel(ctx))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == pgerrcode.LockNotAvailable || pgErr.Code == pgerrcode.SerializationFailure) {
			return nil, true, nil
		}
		return nil, retry, mapErr(err)
	}
	err = boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var err error
		guarded.globalBefore, err = readTrafficHostAnalysis(bounded, tx, pgtype.UUID{}, s.trafficAppsSuffix)
		return err
	})
	if err != nil {
		_ = guarded.Rollback(context.WithoutCancel(ctx))
		return nil, false, err
	}
	return guarded, false, nil
}

func (tx *trafficDomainRemovalTx) Commit(ctx context.Context) error {
	var claims []trafficDomainClaim
	var globalAfter trafficHostAnalysis
	if err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var err error
		claims, err = readTrafficDomainClaims(bounded, tx.Tx)
		return err
	}); err != nil {
		return err
	}
	if err := globalTrafficPolicyError(boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var err error
		globalAfter, err = readTrafficHostAnalysis(bounded, tx.Tx, pgtype.UUID{}, tx.appsSuffix)
		if err != nil {
			return err
		}
		return checkTrafficHostAnalysis(bounded, tx.globalBefore, globalAfter)
	})); err != nil {
		return err
	}
	if err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		for _, account := range tx.accounts {
			view, err := readTrafficHostAnalysis(bounded, tx.Tx, uuidToPgtype(account), tx.appsSuffix)
			if err != nil {
				return err
			}
			if err := checkTrafficDomainRemovalOwner(bounded, view, tx.beforeClaims, claims, account, tx.globalBefore, globalAfter); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	defer tx.release(ctx)
	return tx.Tx.Commit(ctx)
}

func (s *PgStore) acquireTrafficDomainRemovalSession(ctx context.Context, keys []string) (conn *pgxpool.Conn, release func(context.Context), busy bool, err error) {
	err = boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var acquireErr error
		conn, release, busy, acquireErr = s.tryAcquireTrafficPolicySessionKeys(bounded, keys)
		return acquireErr
	})
	if err != nil && release != nil {
		release(context.WithoutCancel(ctx))
	}
	return
}

func (tx *trafficDomainRemovalTx) Rollback(ctx context.Context) error {
	defer tx.release(ctx)
	return tx.Tx.Rollback(ctx)
}

func checkTrafficDomainRemovalOwner(ctx context.Context, view trafficHostAnalysis, before, after []trafficDomainClaim, account string, globalBefore, globalAfter trafficHostAnalysis) error {
	prior, next := trafficDomainOwnerView(view, before, account), trafficDomainOwnerView(view, after, account)
	prior.AllowGlobalRoutes, next.AllowGlobalRoutes = true, true
	prior.Reservations, next.Reservations = globalBefore.Reservations, globalAfter.Reservations
	return checkTrafficHostAnalysis(ctx, prior, next)
}

func (s *PgStore) deleteTrafficCustomDomain(ctx context.Context, domain, expectedApp string, activity *OrgActivity) (int64, error) {
	tx, err := s.beginTrafficDomainRemoval(ctx, domain, expectedApp)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	changed, err := sqlc.New().DeleteTrafficCustomDomain(ctx, tx, sqlc.DeleteTrafficCustomDomainParams{Domain: domain, AppID: tx.appID})
	if err != nil {
		return 0, mapErr(err)
	}
	if changed == 0 {
		return 0, ErrNotFound
	}
	var outboxID int64
	if activity != nil {
		outboxID, err = enqueueOrgActivityOutboxTx(ctx, tx, *activity)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return outboxID, nil
}

func (s *PgStore) DeleteCustomDomainForApp(ctx context.Context, domain, app string) error {
	_, err := s.deleteTrafficCustomDomain(ctx, domain, app, nil)
	return err
}

func (s *PgStore) DeleteCustomDomainForAppWithActivity(ctx context.Context, domain, app string, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	return s.deleteTrafficCustomDomain(ctx, domain, app, &entry)
}
