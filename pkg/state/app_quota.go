package state

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// appQuotaLimit preserves the separate production, developer and PR-preview
// pools. Hosting limits remain defined exclusively by api.Limits.
func appQuotaLimit(app App, limits api.Limits) (int, QuotaErrorKind) {
	if IsDeveloperApp(app) {
		if limits.DeveloperApps > 0 {
			return limits.DeveloperApps, QuotaErrorKindDeveloperApps
		}
		return limits.DeployedApps, QuotaErrorKindDeveloperApps
	}
	if IsPRPreviewApp(app) {
		if limits.PreviewApps > 0 {
			return limits.PreviewApps, QuotaErrorKindPreviewApps
		}
		return limits.DeployedApps, QuotaErrorKindPreviewApps
	}
	return limits.DeployedApps, QuotaErrorKindApps
}

// ErrOrgAppOwnershipPending prevents a personal-account purge from erasing
// shared resources that still have a legacy account storage/billing reference.
var ErrOrgAppOwnershipPending = errors.New("state: shared organization apps still reference this account")

// ErrOrgAppAdmissionUnavailable denies new shared-app reservations when the
// organization or its legacy storage account became inactive during a request.
var ErrOrgAppAdmissionUnavailable = errors.New("state: organization application admission is unavailable")
