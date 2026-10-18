package state

import "github.com/onebox-faas/faas/pkg/api"

// resolveAppQuotaLocked mirrors PostgreSQL's persisted tenant resolution.
// The caller holds m.mu across resolution, counting and insertion/restoration.
func (m *MemStore) resolveAppQuotaLocked(app *App, limits api.Limits) (api.Limits, bool, error) {
	m.ensureAppOrgLocked(app)
	if err := m.checkProjectWorkloadOwnerLocked(*app); err != nil {
		return api.Limits{}, false, err
	}
	if app.OrgID == "" {
		return limits, true, nil
	}
	org, ok := m.orgs[app.OrgID]
	if !ok {
		return api.Limits{}, false, ErrNotFound
	}
	if org.Personal {
		if org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != app.AccountID {
			return api.Limits{}, false, ErrConflict
		}
		return limits, true, nil
	}
	if org.DeletedPending || org.Status != OrgStatusActive {
		return api.Limits{}, false, ErrOrgAppAdmissionUnavailable
	}
	if m.accounts[app.AccountID].Status == AccountDeletedPending {
		return api.Limits{}, false, ErrOrgAppAdmissionUnavailable
	}
	limits, ok = api.LimitsFor(org.Plan)
	if !ok {
		return api.Limits{}, false, ErrInvalidArgument
	}
	return limits, false, nil
}

func (m *MemStore) countAppQuotaLocked(app App, personal bool, kind QuotaErrorKind) int {
	observed := 0
	for _, existing := range m.apps {
		matches := app.OrgID != "" && existing.OrgID == app.OrgID || personal && existing.OrgID == "" && existing.AccountID == app.AccountID
		if !matches || existing.Status != AppActive && existing.Status != AppEvictedCold {
			continue
		}
		_, existingKind := appQuotaLimit(existing, api.Limits{})
		if existingKind == kind {
			observed++
		}
	}
	return observed
}

func (m *MemStore) checkAppQuotaLocked(app App, limits api.Limits) error {
	return m.checkAppQuotaDeltaLocked(app, limits, 1)
}

func (m *MemStore) checkAppQuotaDeltaLocked(app App, limits api.Limits, delta int) error {
	limits, personal, err := m.resolveAppQuotaLocked(&app, limits)
	if err != nil {
		return err
	}
	limit, kind := appQuotaLimit(app, limits)
	observed := m.countAppQuotaLocked(app, personal, kind)
	if observed+delta > limit {
		if delta != 1 {
			observed += delta
		}
		return &QuotaError{Kind: kind, Plan: limits.Plan, Limit: limit, Observed: observed}
	}
	return nil
}

func (m *MemStore) hasLegacySharedAppsLocked(accountID string) bool {
	for _, app := range m.apps {
		if app.AccountID == accountID && app.OrgID != "" {
			org, ok := m.orgs[app.OrgID]
			if ok && !org.Personal {
				return true
			}
		}
	}
	return false
}
