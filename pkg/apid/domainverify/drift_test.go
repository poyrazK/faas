package domainverify_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/domainverify"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDriftRequiresConsecutiveCurrentClaimFailures(t *testing.T) {
	at := time.Now()
	d := state.CustomDomain{Domain: "app.example.com", VerifiedAt: at, DNSLastCheckedAt: at.Add(time.Second)}
	prev := state.DomainDoctorObservation{Domain: d.Domain, ObservedAt: at.Add(time.Second), DNSCheckedAt: at.Add(time.Second)}
	current := state.DomainDoctorObservation{Domain: d.Domain, ObservedAt: at.Add(2 * time.Second), DNSCheckedAt: at.Add(2 * time.Second)}
	if !domainverify.ConfirmDrift(d, prev, current) {
		t.Fatal("consecutive failures did not confirm drift")
	}
	for _, mutate := range []func(*state.DomainDoctorObservation){
		func(p *state.DomainDoctorObservation) { p.PointsToGregale = true },
		func(p *state.DomainDoctorObservation) { p.DNSCheckedAt = time.Time{} },
		func(p *state.DomainDoctorObservation) { p.ObservedAt = at.Add(-time.Second) },
		func(p *state.DomainDoctorObservation) { p.SurfaceID = "old-surface" },
		func(p *state.DomainDoctorObservation) { p.ObservedAt = at.Add(500 * time.Millisecond) },
	} {
		p := prev
		mutate(&p)
		if domainverify.ConfirmDrift(d, p, current) {
			t.Fatalf("invalid previous failure confirmed drift: %+v", p)
		}
	}
}

func TestObservationStampAllowsOnlyDatabasePrecisionDifference(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Microsecond)
	claim := state.CustomDomain{DNSLastCheckedAt: at}
	for _, offset := range []time.Duration{0, time.Microsecond, time.Minute} {
		obs := state.DomainDoctorObservation{ObservedAt: at.Add(offset)}
		if got := domainverify.MatchesObservationStamp(claim, obs); got != (offset <= time.Microsecond) {
			t.Fatalf("offset %s matched=%t", offset, got)
		}
	}
}
