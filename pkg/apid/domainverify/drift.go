package domainverify

import (
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// MatchesObservationStamp ties an observation to the claim's last DNS write.
// PostgreSQL's JSON timestamp parser rounds to microseconds while pgx's binary
// timestamps truncate. Allow only that representation difference for N-1 data.
func MatchesObservationStamp(claim state.CustomDomain, obs state.DomainDoctorObservation) bool {
	return !claim.DNSLastCheckedAt.IsZero() && !obs.ObservedAt.IsZero() &&
		obs.ObservedAt.Sub(claim.DNSLastCheckedAt).Abs() <= time.Microsecond
}

// ConfirmDrift requires two consecutive, conclusive routing failures since the
// current ownership verification. Inconclusive DNS, healthy checks, and an old
// surface/claim observation break the sequence. State mutations still enforce
// claim identity and observation ordering atomically.
func ConfirmDrift(claim state.CustomDomain, previous, current state.DomainDoctorObservation) bool {
	return claim.Verified() && previous.Domain == claim.Domain && previous.SurfaceID == "" &&
		!previous.DNSCheckedAt.IsZero() && !current.DNSCheckedAt.IsZero() &&
		!previous.PointsToGregale && !current.PointsToGregale &&
		MatchesObservationStamp(claim, previous) &&
		!previous.ObservedAt.Before(claim.VerifiedAt) && current.ObservedAt.After(previous.ObservedAt)
}
