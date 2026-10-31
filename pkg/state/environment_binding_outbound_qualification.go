// adr:20261019152416 — outboundd probe receipts for candidate qualification.
package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// EnvironmentQualificationOutboundStore lists the probe-ready outbound
// bindings of enforced candidates with a live qualification lease and no
// receipt for their current revision, and records outboundd's receipts.
// Recording fails closed with ErrBindingPromotionChanged on any change.
type EnvironmentQualificationOutboundStore interface {
	PendingEnvironmentQualificationOutboundProbes(ctx context.Context, limit int) ([]api.OutboundQualificationProbe, error)
	RecordEnvironmentQualificationOutboundReceipt(ctx context.Context, probe api.OutboundQualificationProbe, status int) error
}

// candidateOutboundBinding is one outbound binding of a candidate's app.
// Inactive integrations are skipped; an active binding without a revision is
// not probe-ready and blocks activation.
type candidateOutboundBinding struct {
	IntegrationID string
	Active        bool
	Revision      string
	Policy        api.OutboundBindingProbePolicy
}

func qualificationOutboundReceiptKey(requestID string, attempt int64, integrationID, revision string) string {
	return qualificationSmokeReceiptKey(requestID, attempt) + "/" + integrationID + "/" + revision
}

func validOutboundQualificationReceipt(probe api.OutboundQualificationProbe, status int) bool {
	return probe.Valid() && status == probe.Policy.ExpectedStatus
}

// candidateOutboundEvidenceDeadline mirrors environment_binding_outbound_deadline.
// It returns the zero time when the candidate has no active outbound binding.
func candidateOutboundEvidenceDeadline(request EnvironmentWorkloadQualificationRequest, bindings []candidateOutboundBinding,
	receipts map[string]time.Time, age time.Duration, now time.Time) (time.Time, error) {
	var until time.Time
	for _, b := range bindings {
		if !b.Active {
			continue
		}
		recorded, ok := receipts[qualificationOutboundReceiptKey(request.ID, request.Attempt, b.IntegrationID, b.Revision)]
		if b.Revision == "" || !ok || recorded.IsZero() || recorded.After(now) {
			return time.Time{}, ErrBindingReleaseRequired
		}
		if end := recorded.Add(age); until.IsZero() || end.Before(until) {
			until = end
		}
	}
	return until, nil
}
