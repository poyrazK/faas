// adr: 429
package gateway

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ServiceRequestObservation is target-side debug evidence, with identity from
// the authorized resolver and selected endpoint rather than guest headers.
// Latency is time to the first upstream response header, including for streams.
type ServiceRequestObservation struct {
	AccountID         string
	ScenarioTestRunID string
	Target            Target
	ReceivedAt        time.Time
	LatencyMS         int
	Status            int
	ColdBoot          bool
}

func (p *ServiceProxy) withServiceRequestObservation(r *http.Request, target ServiceTarget, caller ServiceCaller, endpoint ServiceEndpoint, woken bool) (*http.Request, func(int)) {
	if p.observeRequest == nil {
		return r, nil
	}
	started := p.now()
	// Each retry gets its own marker. A transport error cannot inherit proof
	// that another endpoint responded on an earlier attempt.
	request := r.WithContext(WithFirstByteRecorder(r.Context(), &firstByteRecorder{}))
	return request, func(status int) {
		if _, handled := FirstByteFrom(request); !handled || status < 100 || status > 599 {
			return
		}
		p.observeRequest(request, ServiceRequestObservation{
			AccountID: caller.AccountID, ScenarioTestRunID: target.ScenarioTestRunID,
			Target: serviceEndpointTarget(target.AppID, endpoint), ReceivedAt: started,
			LatencyMS: int(max(p.now().Sub(started), 0) / time.Millisecond), Status: status, ColdBoot: woken,
		})
	}
}

// RecordServiceRequest uses the existing bounded debugger ring and publisher.
// It deliberately adds no financial usage event: internal dependency evidence
// must not change the ingress accounting contract (ADR-234/ADR-429).
func (h *Handler) RecordServiceRequest(r *http.Request, observation ServiceRequestObservation) {
	if h == nil || h.requestTelemetry == nil || r == nil {
		return
	}
	row, ok := serviceRequestTelemetryRow(r, observation)
	if ok {
		h.requestTelemetry.RecordFromObserve(row)
	}
}

func serviceRequestTelemetryRow(r *http.Request, o ServiceRequestObservation) (RequestTelemetryRow, bool) {
	accountID, accountErr := uuid.Parse(o.AccountID)
	appID, appErr := uuid.Parse(o.Target.AppID)
	deploymentID, deploymentErr := uuid.Parse(o.Target.DeploymentID)
	if accountErr != nil || appErr != nil || accountID == uuid.Nil || appID == uuid.Nil || o.Target.InstanceID == "" ||
		o.ReceivedAt.IsZero() || o.Status < 100 || o.Status > 599 || (o.Target.DeploymentID != "" && deploymentErr != nil) {
		return RequestTelemetryRow{}, false
	}
	return RequestTelemetryRow{
		AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
		Route: otherRouteLabel, Method: r.Method, Status: o.Status, LatencyMS: o.LatencyMS,
		ColdBoot: o.ColdBoot, TraceID: traceIDForTelemetry(r.Context()), ReceivedAt: o.ReceivedAt,
		InstanceID: o.Target.InstanceID, NodeID: o.Target.NodeID, Region: o.Target.Region,
		CommitSHA: o.Target.CommitSHA, DeploymentTag: o.Target.DeploymentTag,
		DeploymentCreatedAt: o.Target.DeploymentCreatedAt, ImageDigest: o.Target.ImageDigest,
		UAFamily: telemetryUnknownDimension, ReferrerHost: telemetryNoReferrer, Country: telemetryUnknownDimension,
		// The receiver already uses this flag to suppress legacy usage for
		// nonfinancial rows, including rejected ingress admissions. No outbox
		// write or caller consumer/tenant identity is claimed by this debug row.
		UsageOutboxed: true, preserveExact: o.ScenarioTestRunID != "",
	}, true
}
