// adr: 429
package gateway

import (
	"context"
	"net/http"
	"sync"
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
	// WakeID is the wake this call started or joined when it found the
	// target parked, so the trace view can show how long the restore took.
	WakeID string
	// Guest is the execution evidence the target's runtime reported, as on
	// public requests; runtimes that report nothing leave it empty.
	Guest guestExecutionEvidenceSnapshot
}

// serviceWakeRecorder carries the wake a service call started or joined back
// to the call's telemetry row: the waker seam returns only an error.
type serviceWakeRecorder struct {
	mu sync.Mutex
	id string
}

type serviceWakeRecorderKey struct{}

func withServiceWakeRecorder(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), serviceWakeRecorderKey{}, &serviceWakeRecorder{}))
}

// recordServiceWakeID keeps the first wake recorded for the call; ctx without
// a recorder (wake-ahead, private TCP) records nothing.
func recordServiceWakeID(ctx context.Context, id string) {
	recorder, _ := ctx.Value(serviceWakeRecorderKey{}).(*serviceWakeRecorder)
	if recorder == nil || id == "" {
		return
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.id == "" {
		recorder.id = id
	}
}

func serviceWakeIDFrom(ctx context.Context) string {
	recorder, _ := ctx.Value(serviceWakeRecorderKey{}).(*serviceWakeRecorder)
	if recorder == nil {
		return ""
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.id
}

// serviceWakeIDOf and guestExecutionEvidenceSnapshotOf read the call's
// evidence from the request, like FirstByteFrom, for observers that run after
// the response.
func serviceWakeIDOf(r *http.Request) string { return serviceWakeIDFrom(r.Context()) }

func guestExecutionEvidenceSnapshotOf(r *http.Request) guestExecutionEvidenceSnapshot {
	snapshot, _ := guestExecutionEvidenceFromContext(r.Context())
	return snapshot
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
		observation := ServiceRequestObservation{
			AccountID: caller.AccountID, ScenarioTestRunID: target.ScenarioTestRunID,
			Target: serviceEndpointTarget(target.AppID, endpoint), ReceivedAt: started,
			LatencyMS: int(max(p.now().Sub(started), 0) / time.Millisecond), Status: status, ColdBoot: woken,
		}
		if woken {
			observation.WakeID = serviceWakeIDOf(request)
		}
		observation.Guest = guestExecutionEvidenceSnapshotOf(request)
		p.observeRequest(request, observation)
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
	traceID := traceIDForTelemetry(r.Context())
	return RequestTelemetryRow{
		AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
		Route: otherRouteLabel, Method: r.Method, Status: o.Status, LatencyMS: o.LatencyMS,
		ColdBoot: o.ColdBoot, TraceID: traceID, TraceInferred: traceID != "" && traceInferredOf(r), ReceivedAt: o.ReceivedAt, WakeID: o.WakeID,
		GuestDurationMS: o.Guest.DurationMS, GuestRuntime: o.Guest.Runtime, GuestOutcome: o.Guest.Outcome, GuestErrorClass: o.Guest.ErrorClass,
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
