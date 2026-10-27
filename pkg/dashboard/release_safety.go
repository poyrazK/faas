package dashboard

import "github.com/onebox-faas/faas/pkg/api"

// ReleaseSafetyData is the /dashboard/apps/{slug}/release-safety page: what
// holds the app's production releases back. A section whose capability is
// off for the account is absent (nil or false); a section that could not be
// read is named in Unavailable.
type ReleaseSafetyData struct {
	AppSlug string
	// Freezes is nil when deploy freezes are off for the account.
	Freezes *api.ReleaseFreezeStatus
	// GatesEnabled reports the release-gates section; Gates may be empty.
	GatesEnabled bool
	Gates        []api.ReleaseGate
	// ApprovalsEnabled reports the release-approvals section. Policy is nil
	// when the app requires no approvals.
	ApprovalsEnabled bool
	Policy           *api.ReleaseApprovalPolicy
	Waiting          []api.ReleaseApprovalStatus
	// SchedulesEnabled reports the scheduled-releases section.
	SchedulesEnabled bool
	Schedules        []api.ScheduledRelease
	Unavailable      []string
}
