package state

import (
	"context"
	"errors"
	"fmt"
)

// AppOwnerReader deliberately excludes authentication and credential writers.
type AppOwnerReader interface {
	AccountByID(context.Context, string) (Account, error)
	OrgByID(context.Context, string) (Org, error)
}

// AppResourceAccount returns a policy projection, never an authenticated human.
// ID remains the immutable storage namespace used by accepted work and sealed
// bindings. ResourceOwnerID identifies the tenant for aggregate admission/billing.
// In particular, shared app operation does not read the creator's account.
func AppResourceAccount(ctx context.Context, store AppOwnerReader, app App) (Account, error) {
	if app.OrgID == "" {
		return store.AccountByID(ctx, app.AccountID)
	}
	org, err := store.OrgByID(ctx, app.OrgID)
	if err != nil {
		return Account{}, fmt.Errorf("state: app resource organization: %w", err)
	}
	if org.Personal {
		if org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != app.AccountID {
			return Account{}, ErrConflict
		}
		return store.AccountByID(ctx, app.AccountID)
	}
	if !org.ResourceOwnershipEnabled {
		legacy, err := store.AccountByID(ctx, app.AccountID)
		if err != nil {
			return Account{}, err
		}
		if org.DeletedPending {
			legacy.Status = AccountDeletedPending
		} else if org.Status != OrgStatusActive && org.Status != OrgStatusPastDue {
			legacy.Status = AccountStatus(org.Status)
		}
		if org.AbuseHoldAt != nil {
			legacy.AbuseHoldAt = org.AbuseHoldAt
			legacy.AbuseHoldReason = org.AbuseHoldReason
		}
		return legacy, nil
	}
	owner := organizationAccount(org)
	if reader, ok := store.(interface {
		OrgResourceAccount(context.Context, string) (Account, error)
	}); ok {
		owner, err = reader.OrgResourceAccount(ctx, org.ID)
		if err != nil {
			return Account{}, err
		}
	}
	owner.ID = app.AccountID
	return owner, nil
}

// ResourceOwnerID returns the organization subject on resource projections and
// the identity's personal subject otherwise. It must not select an authenticator.
func (a Account) ResourceOwnerID() string {
	if a.ResourceOrgID != "" {
		return a.ResourceOrgID
	}
	return a.ID
}

// AsyncQuotaSubjectID is the account_async_quota row a new reservation uses:
// the organization once its reservations are pooled, else the namespace.
func (a Account) AsyncQuotaSubjectID() string {
	if a.AsyncQuotaPooled && a.ResourceOrgID != "" {
		return a.ResourceOrgID
	}
	return a.ID
}

func organizationAccount(org Org) Account {
	status := AccountStatus(org.Status)
	if org.DeletedPending {
		status = AccountDeletedPending
	}
	return Account{ID: org.ID, ResourceOrgID: org.ID, ResourceOwnershipEnabled: org.ResourceOwnershipEnabled, Plan: org.Plan, Status: status,
		ProviderCustomerID: org.ProviderCustomerID, StripeSubscriptionItem: org.StripeSubscriptionItem,
		CreatedAt: org.CreatedAt, AbuseHoldAt: org.AbuseHoldAt, AbuseHoldReason: org.AbuseHoldReason}
}

// ListBillingResourceAccounts adds shared tenant subjects without adding human
// accounts. Identity inventories, authentication and grace erasure remain human
// only. Missing optional support in older adapters preserves personal behavior.
func ListBillingResourceAccounts(ctx context.Context, store interface {
	ListAllAccounts(context.Context) ([]Account, error)
}) ([]Account, error) {
	accounts, err := store.ListAllAccounts(ctx)
	if err != nil {
		return nil, err
	}
	if reader, ok := store.(interface {
		ListOrgResourceAccounts(context.Context) ([]Account, error)
	}); ok {
		orgs, err := reader.ListOrgResourceAccounts(ctx)
		if err != nil {
			return nil, err
		}
		for _, org := range orgs {
			if org.ResourceOwnershipEnabled {
				accounts = append(accounts, org)
			}
		}
	}
	return accounts, nil
}

// BillingResourceAccount resolves an explicitly authorized financial subject.
// It is separate from AccountByID so organization IDs never authenticate.
func BillingResourceAccount(ctx context.Context, store AppOwnerReader, id string) (Account, error) {
	account, err := store.AccountByID(ctx, id)
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Account{}, err
	}
	org, err := store.OrgByID(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if org.Personal {
		return Account{}, ErrNotFound
	}
	if reader, ok := store.(interface {
		OrgResourceAccount(context.Context, string) (Account, error)
	}); ok {
		return reader.OrgResourceAccount(ctx, id)
	}
	return organizationAccount(org), nil
}

// SameAppResourceOwner never infers shared ownership from a storage UUID.
func SameAppResourceOwner(a, b App) bool {
	if a.AccountID == "" || b.AccountID == "" {
		return false
	}
	if a.OrgID != "" || b.OrgID != "" {
		return a.OrgID != "" && a.OrgID == b.OrgID
	}
	return a.AccountID == b.AccountID
}
