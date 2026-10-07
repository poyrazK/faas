package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// Interactive creation may install only the exact desired intent it read.
// Reviewed targets and existing worker leases keep their normal precedence.
type ApplicationStandardEnrollmentClaimRequest struct {
	OrgID, AppID, Owner string
	DesiredRevision     int64
}

type ApplicationStandardImmediateMaterializationStore interface {
	ApplicationStandardAutomaticMaterializationStore
	ClaimApplicationStandardEnrollmentForApp(context.Context, ApplicationStandardEnrollmentClaimRequest) (ApplicationStandardEnrollmentClaim, error)
	ReleaseApplicationStandardEnrollmentWorker(context.Context, ApplicationStandardEnrollmentClaim) error
}

func validStandardImmediateClaim(r ApplicationStandardEnrollmentClaimRequest) bool {
	return validStandardResourceRead(r.OrgID, r.AppID) && standardWorkerOwnerValid(r.Owner) && r.DesiredRevision > 0 && r.DesiredRevision <= api.ApplicationStandardMaxVersion
}
