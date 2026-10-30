package state

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
)

// platformTenantKeySubject is the billing subject (ADR-202610171904) whose
// consumer key allowance a key on an app spends, and that subject's plan. A
// platform tenant's consumers may sit on apps of several subjects: its owner's
// personal apps and the owner's apps in companies that adopted billing.
type platformTenantKeySubject struct {
	ID   string
	Plan api.Plan
}

// checkPlatformTenantKeyQuota refuses a bundle that leaves an app it issues
// keys on, or that app's billing subject, holding more unrevoked keys than
// the subject's plan allows. subjectKept and appKept count the unrevoked keys
// the bundle keeps.
func checkPlatformTenantKeyQuota(result ApplyPlatformTenantCredentialsResult, subjects map[string]platformTenantKeySubject,
	subjectKept, appKept map[string]int) error {
	appAdded, subjectAdded := make(map[string]int), make(map[string]int)
	for _, item := range result.Keys {
		if item.Action != "create" {
			continue
		}
		subject, ok := subjects[item.Key.AppID]
		if !ok {
			return ErrNotFound
		}
		appAdded[item.Key.AppID]++
		subjectAdded[subject.ID]++
	}
	apps := slices.Sorted(maps.Keys(appAdded))
	for _, appID := range apps {
		subject := subjects[appID]
		if count, limit := subjectKept[subject.ID]+subjectAdded[subject.ID], subject.Plan.ConsumerKeysPerAccount(); count > limit {
			return &PlatformTenantCredentialQuotaError{Scope: api.PlanQuotaScopeAccount, Plan: subject.Plan, Limit: limit, Observed: count - 1}
		}
	}
	for _, appID := range apps {
		subject := subjects[appID]
		if count, limit := appKept[appID]+appAdded[appID], subject.Plan.ConsumerKeysPerApp(); count > limit {
			return &PlatformTenantCredentialQuotaError{Scope: api.PlanQuotaScopeApp, Plan: subject.Plan, Limit: limit, Observed: count - 1}
		}
	}
	return nil
}

// lockPlatformTenantKeySubjectsTx locks the billing subjects of the apps
// whose consumers the bundle issues keys to, in runtime admission's order
// (lockQuotaSubjectTx): their organizations, then the accounts, the caller's
// among them even for a bundle that only revokes, then the apps, rechecked
// under those locks. It returns each app's subject.
func lockPlatformTenantKeySubjectsTx(ctx context.Context, tx pgx.Tx, in ApplyPlatformTenantCredentialsParams) (map[string]platformTenantKeySubject, error) {
	consumerIDs := make([]string, 0, len(in.Keys))
	for _, key := range in.Keys {
		consumerIDs = append(consumerIDs, key.ConsumerID)
	}
	type appOwner struct {
		id, accountID string
		orgID         pgtype.UUID
	}
	rows, err := tx.Query(ctx, `select distinct a.id::text, a.account_id::text, a.org_id from apps a
		join api_consumers c on c.app_id = a.id
		where c.id = any($1::uuid[]) and c.account_id = $2::uuid and a.status <> 'deleted'
		order by 1`, consumerIDs, in.AccountID)
	if err != nil {
		return nil, fmt.Errorf("state: read platform tenant key apps: %w", err)
	}
	apps, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (appOwner, error) {
		var app appOwner
		return app, row.Scan(&app.id, &app.accountID, &app.orgID)
	})
	if err != nil {
		return nil, fmt.Errorf("state: read platform tenant key apps: %w", err)
	}
	var orgIDs []string
	for _, app := range apps {
		if app.orgID.Valid {
			orgIDs = append(orgIDs, pgUUIDString(app.orgID))
		}
	}
	slices.Sort(orgIDs)
	adopted := make(map[string]api.Plan)
	for _, orgID := range slices.Compact(orgIDs) {
		var isAdopted bool
		var plan string
		if err := tx.QueryRow(ctx, `select not personal_org and resource_ownership_enabled, plan from orgs where id = $1::uuid for update`,
			orgID).Scan(&isAdopted, &plan); err != nil {
			return nil, fmt.Errorf("state: lock organization %s: %w", orgID, err)
		}
		if isAdopted {
			adopted[orgID] = api.Plan(plan)
		}
	}
	accountIDs := []string{in.AccountID}
	for _, app := range apps {
		if _, ok := adopted[pgUUIDString(app.orgID)]; !ok {
			accountIDs = append(accountIDs, app.accountID)
		}
	}
	slices.Sort(accountIDs)
	plans := make(map[string]api.Plan)
	for _, accountID := range slices.Compact(accountIDs) {
		var plan string
		if err := tx.QueryRow(ctx, `select plan from accounts where id = $1::uuid for update`, accountID).Scan(&plan); err != nil {
			return nil, applyNoRows(err)
		}
		plans[accountID] = api.Plan(plan)
	}
	subjects := make(map[string]platformTenantKeySubject, len(apps))
	for _, app := range apps {
		var locked int
		if err := tx.QueryRow(ctx, `select 1 from apps where id = $1::uuid and account_id = $2::uuid
			and org_id is not distinct from $3 and status <> 'deleted' for update`, app.id, app.accountID, app.orgID).Scan(&locked); err != nil {
			return nil, applyNoRows(err)
		}
		subject := platformTenantKeySubject{ID: app.accountID, Plan: plans[app.accountID]}
		if plan, ok := adopted[pgUUIDString(app.orgID)]; ok {
			subject = platformTenantKeySubject{ID: pgUUIDString(app.orgID), Plan: plan}
		}
		subjects[app.id] = subject
	}
	return subjects, nil
}

// countPlatformTenantKeysKeptTx counts the unrevoked keys a bundle keeps on
// each app it issues keys on, and on each such app's billing subject.
func countPlatformTenantKeysKeptTx(ctx context.Context, tx pgx.Tx, subjects map[string]platformTenantKeySubject,
	result ApplyPlatformTenantCredentialsResult) (subjectKept, appKept map[string]int, err error) {
	// A nil array would bind as NULL, and "<> all(NULL)" matches nothing.
	revoking := make([]string, 0)
	for _, item := range result.Keys {
		if item.Action == "revoke" {
			revoking = append(revoking, item.Key.ID)
		}
	}
	subjectKept, appKept = make(map[string]int), make(map[string]int)
	for _, item := range result.Keys {
		appID := item.Key.AppID
		if _, counted := appKept[appID]; counted || item.Action != "create" {
			continue
		}
		subject, ok := subjects[appID]
		if !ok {
			return nil, nil, ErrNotFound
		}
		var appKeys, subjectKeys int
		if err := tx.QueryRow(ctx, `select count(*) filter (where k.app_id = $2::uuid), count(*)
			from consumer_keys k
			join apps a on a.id = k.app_id
			left join orgs o on o.id = a.org_id
			where k.revoked_at is null and k.id <> all($3::uuid[]) and (a.account_id = $1::uuid or a.org_id = $1::uuid)
			  and `+quotaSubjectSQL+` = $1::uuid and a.status <> 'deleted'`, subject.ID, appID, revoking).Scan(&appKeys, &subjectKeys); err != nil {
			return nil, nil, fmt.Errorf("state: count consumer keys of billing subject %s: %w", subject.ID, err)
		}
		appKept[appID], subjectKept[subject.ID] = appKeys, subjectKeys
	}
	return subjectKept, appKept, nil
}

// checkPlatformTenantKeyQuotaLocked checks the bundle against the plan of
// each billing subject whose apps it issues keys on.
func (m *MemStore) checkPlatformTenantKeyQuotaLocked(result ApplyPlatformTenantCredentialsResult) error {
	revoking := make(map[string]bool)
	subjects := make(map[string]platformTenantKeySubject)
	subjectIDs := make(map[string]bool)
	for _, item := range result.Keys {
		switch item.Action {
		case "revoke":
			revoking[item.Key.ID] = true
		case "create":
			app, ok := m.apps[item.Key.AppID]
			if !ok || app.Status == AppDeleted {
				return ErrNotFound
			}
			owner, _ := m.appResourceAccountLocked(app)
			subject := platformTenantKeySubject{ID: m.appBillingSubjectLocked(app.AccountID, app.ID), Plan: owner.Plan}
			subjects[app.ID], subjectIDs[subject.ID] = subject, true
		}
	}
	subjectKept, appKept := make(map[string]int), make(map[string]int)
	for _, key := range m.consumerKeys {
		if key.RevokedAt != nil || revoking[key.ID] {
			continue
		}
		if _, ok := subjects[key.AppID]; ok {
			appKept[key.AppID]++
		}
		for subjectID := range subjectIDs {
			if m.countsTowardQuotaSubjectLocked(key.AccountID, key.AppID, subjectID) {
				subjectKept[subjectID]++
			}
		}
	}
	return checkPlatformTenantKeyQuota(result, subjects, subjectKept, appKept)
}
