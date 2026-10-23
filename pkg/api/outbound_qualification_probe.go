// adr:20261019152416 — outboundd probes enforced candidates' outbound bindings itself.
package api

import (
	"regexp"

	"github.com/google/uuid"
)

var outboundQualificationRevision = regexp.MustCompile(`^[a-f0-9]{64}$`)

// OutboundQualificationProbe is one probe-ready outbound binding of an
// enforced qualification candidate. outboundd sends Policy through its own
// handler for the candidate's app and records a receipt for Revision when the
// response status equals Policy.ExpectedStatus. It is never sent to a guest.
type OutboundQualificationProbe struct {
	RequestID     string
	Attempt       int64
	AccountID     string
	AppID         string
	IntegrationID string
	Revision      string
	Policy        OutboundBindingProbePolicy
}

func (p OutboundQualificationProbe) Valid() bool {
	for _, id := range []string{p.RequestID, p.IntegrationID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	return p.Attempt > 0 && p.AccountID != "" && p.AppID != "" && outboundQualificationRevision.MatchString(p.Revision) && p.Policy.Valid()
}
