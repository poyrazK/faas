package apidserver

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// ownDomainHolderHint explains a domain conflict when the domain is held by
// one of the caller's own apps. "Domain taken" with detail "state: conflict"
// said nothing when the holder was the account's own deleted app (prod hunt
// 2026-11-21), so the owner could not tell how to free it. Another
// account's claim is not described.
func ownDomainHolderHint(ctx context.Context, store state.Store, accountID, domain string) string {
	held, err := store.DomainByName(ctx, domain)
	if err != nil || held.AppID == "" {
		return ""
	}
	holder, err := store.AppByID(ctx, held.AppID)
	if err != nil || holder.AccountID != accountID {
		return ""
	}
	if holder.DeletedAt != nil {
		return fmt.Sprintf("%s is still bound to your deleted app %s; free it with `gregale domain delete %s` or `gregale app purge %s`", domain, holder.Slug, domain, holder.Slug)
	}
	return fmt.Sprintf("%s is already bound to your app %s; run `gregale domain delete %s` first to move it", domain, holder.Slug, domain)
}
