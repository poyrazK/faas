// adr: 375
package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type trafficTenantBindingTx struct {
	pgx.Tx
	before       trafficBindingClaims
	beforeViews  map[string]trafficHostAnalysis
	globalBefore trafficHostAnalysis
	accounts     []string
	hosts        []string
	account      string
	appsSuffix   string
	release      func(context.Context)
}

func trafficBindingAffectedHosts(claims trafficBindingClaims, account string, requested []string, appID string) []string {
	hosts := append([]string(nil), requested...)
	for _, claim := range claims.Tenants {
		if claim.Account == account || claim.AppAccount == account {
			hosts = append(hosts, claim.Host)
		}
	}
	for _, claim := range claims.Domains {
		if claim.Account == account || claim.RedirectAccount == account || appID != "" && claim.RedirectApp == appID {
			hosts = append(hosts, claim.Domain)
		}
	}
	for _, named := range [][]trafficNamedHostClaim{claims.Aliases, claims.Primaries} {
		for _, claim := range named {
			if claim.Account == account {
				hosts = append(hosts, claim.Host)
			}
		}
	}
	for i := range hosts {
		hosts[i] = strings.ToLower(hosts[i])
	}
	sort.Strings(hosts)
	return slices.Compact(hosts)
}

func trafficTenantOverlappingOwners(ctx context.Context, claims trafficBindingClaims, hosts []string, account string) (map[string]bool, error) {
	owners := map[string]bool{account: true}
	exact, wildcard := make(map[string][]string), make(map[string][]string)
	for _, claim := range claims.Domains {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if suffix, found := WildcardDomainSuffix(claim.Domain); found {
			wildcard[strings.ToLower(suffix)] = append(wildcard[strings.ToLower(suffix)], claim.Account)
		} else {
			exact[strings.ToLower(claim.Domain)] = append(exact[strings.ToLower(claim.Domain)], claim.Account)
		}
	}
	for _, claim := range claims.Tenants {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		exact[strings.ToLower(claim.Host)] = append(exact[strings.ToLower(claim.Host)], claim.Account)
	}
	for _, named := range [][]trafficNamedHostClaim{claims.Aliases, claims.Primaries} {
		for _, claim := range named {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			exact[strings.ToLower(claim.Host)] = append(exact[strings.ToLower(claim.Host)], claim.Account)
		}
	}
	for _, host := range hosts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, wild := WildcardDomainSuffix(host); wild {
			for _, claim := range claims.Domains {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if trafficDomainClaimsOverlap(host, claim.Domain) {
					owners[claim.Account] = true
				}
			}
			for _, claim := range claims.Tenants {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if trafficDomainClaimsOverlap(host, claim.Host) {
					owners[claim.Account] = true
				}
			}
			continue
		}
		for _, owner := range exact[host] {
			owners[owner] = true
		}
		for tail := host; strings.ContainsRune(tail, '.'); {
			tail = tail[strings.IndexByte(tail, '.')+1:]
			for _, owner := range wildcard[tail] {
				owners[owner] = true
			}
		}
	}
	return owners, nil
}

func trafficTenantBindingOwners(ctx context.Context, reader sqlc.DBTX, claims trafficBindingClaims, hosts []string, account string) ([]string, error) {
	owners, err := trafficTenantOverlappingOwners(ctx, claims, hosts, account)
	if err != nil {
		return nil, err
	}
	routes, err := sqlc.New().ReadTrafficGlobalRouteAccounts(ctx, reader, api.TrafficPolicyMaxAnalysisInputs)
	if err != nil {
		return nil, fmt.Errorf("state: read tenant global route owners: %w", mapErr(err))
	}
	for _, owner := range routes {
		owners[owner.String()] = true
	}
	delete(owners, "")
	if len(owners) > api.TrafficPolicyMaxAnalysisInputs {
		return nil, analysisLimit("inputs", "owners", api.TrafficPolicyMaxAnalysisInputs, int64(len(owners)))
	}
	accounts := make([]string, 0, len(owners))
	for owner := range owners {
		accounts = append(accounts, owner)
	}
	sort.Strings(accounts)
	return accounts, nil
}

func (s *PgStore) beginTrafficTenantBinding(ctx context.Context, account string, hosts []string) (*trafficTenantBindingTx, error) {
	return s.beginTrafficBinding(ctx, account, hosts, "")
}

func (s *PgStore) beginTrafficBinding(ctx context.Context, account string, hosts []string, appID string) (*trafficTenantBindingTx, error) {
	for {
		tx, retry, err := s.tryBeginTrafficBinding(ctx, account, hosts, appID)
		if err != nil || !retry {
			return tx, err
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

func (s *PgStore) tryBeginTrafficBinding(ctx context.Context, account string, requested []string, appID string) (*trafficTenantBindingTx, bool, error) {
	var hosts, accounts []string
	err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		claims, err := readTrafficBindingClaims(bounded, s.pool, s.trafficAppsSuffix)
		if err != nil {
			return err
		}
		hosts = trafficBindingAffectedHosts(claims, account, requested, appID)
		accounts, err = trafficTenantBindingOwners(bounded, s.pool, claims, hosts, account)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	keys := []string{globalTrafficRoutesLock}
	for _, owner := range accounts {
		keys = append(keys, "gregale.traffic.account.v1:"+owner)
	}
	conn, release, busy, err := s.acquireTrafficDomainBindingSession(ctx, keys)
	if err != nil || busy {
		return nil, busy, err
	}
	base, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		release(ctx)
		return nil, false, fmt.Errorf("state: begin tenant binding change: %w", err)
	}
	tx := &trafficTenantBindingTx{Tx: base, beforeViews: make(map[string]trafficHostAnalysis), accounts: accounts, hosts: hosts, account: account, appsSuffix: s.trafficAppsSuffix, release: release}
	retry := false
	err = boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		for _, owner := range accounts {
			if _, err := sqlc.New().LockTrafficPolicyAccount(bounded, base, uuidToPgtype(owner)); err != nil {
				return err
			}
		}
		var err error
		tx.before, err = readTrafficBindingClaims(bounded, base, s.trafficAppsSuffix)
		if err != nil {
			return err
		}
		freshHosts := trafficBindingAffectedHosts(tx.before, account, requested, appID)
		freshAccounts, err := trafficTenantBindingOwners(bounded, base, tx.before, freshHosts, account)
		if err != nil {
			return err
		}
		retry = !slices.Equal(hosts, freshHosts) || !slices.Equal(accounts, freshAccounts)
		if retry {
			return nil
		}
		tx.globalBefore, err = readTrafficHostAnalysis(bounded, base, pgtype.UUID{}, tx.appsSuffix)
		if err != nil {
			return err
		}
		for _, owner := range accounts {
			view, err := readTrafficHostAnalysis(bounded, base, uuidToPgtype(owner), tx.appsSuffix)
			if err != nil {
				return err
			}
			tx.beforeViews[owner] = view
		}
		return nil
	})
	if err != nil || retry {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == pgerrcode.LockNotAvailable || pgErr.Code == pgerrcode.SerializationFailure) {
			return nil, true, nil
		}
		return nil, retry, mapErr(err)
	}
	return tx, false, nil
}

func (tx *trafficTenantBindingTx) validate(ctx context.Context, claims trafficBindingClaims, globalAfter trafficHostAnalysis, views map[string]trafficHostAnalysis) error {
	if err := globalTrafficPolicyError(checkTrafficHostAnalysis(ctx, tx.globalBefore, globalAfter)); err != nil {
		return err
	}
	for _, owner := range tx.accounts {
		if err := checkTrafficTenantBindingOwner(ctx, trafficPrimaryOwnerView(tx.beforeViews[owner], tx.before.Aliases), trafficPrimaryOwnerView(views[owner], claims.Aliases), tx.before.Domains, claims.Domains, tx.before.Tenants, claims.Tenants, owner, tx.globalBefore, globalAfter); err != nil {
			return err
		}
	}
	return nil
}

func (tx *trafficTenantBindingTx) Validate(ctx context.Context) error {
	return boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		claims, err := readTrafficBindingClaims(bounded, tx.Tx, tx.appsSuffix)
		if err != nil {
			return err
		}
		globalAfter, err := readTrafficHostAnalysis(bounded, tx.Tx, pgtype.UUID{}, tx.appsSuffix)
		if err != nil {
			return err
		}
		views := make(map[string]trafficHostAnalysis, len(tx.accounts))
		for _, owner := range tx.accounts {
			view, err := readTrafficHostAnalysis(bounded, tx.Tx, uuidToPgtype(owner), tx.appsSuffix)
			if err != nil {
				return err
			}
			views[owner] = view
		}
		return tx.validate(bounded, claims, globalAfter, views)
	})
}

func (tx *trafficTenantBindingTx) Commit(ctx context.Context) error {
	if err := tx.Validate(ctx); err != nil {
		return err
	}
	defer tx.release(ctx)
	return tx.Tx.Commit(ctx)
}

func (tx *trafficTenantBindingTx) Rollback(ctx context.Context) error {
	defer tx.release(ctx)
	return tx.Tx.Rollback(ctx)
}
