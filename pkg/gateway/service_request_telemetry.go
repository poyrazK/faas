// adr: 429
package gateway

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// ServiceRequestObservation is target-side debug evidence, with identity from
// the authorized resolver and selected endpoint rather than guest headers.
// Latency is time to the first upstream response header, including for
// streams; a gRPC call is observed when it completes, because its outcome is
// in its trailers.
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
	// InvocationSource, when set, marks a scheduled or queued invocation the
	// platform delivered (cron, queue, async_invoke, delayed_task). Its row's
	// client family reads "gregale-<source>" so HTTP logs tell it apart
	// from a caller's request (prod findings 2026-11-15 #7).
	InvocationSource string
}

// invocationClientFamily is the ua_family of an invocation's telemetry row.
func invocationClientFamily(source string) string {
	return "gregale-" + source
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
	if h == nil || r == nil {
		return
	}
	h.observeServiceRequestMetrics(r, observation)
	if h.requestTelemetry == nil {
		return
	}
	row, ok := serviceRequestTelemetryRow(r, observation)
	if ok {
		row.Route = h.serviceRequestRoute(r, observation)
		h.requestTelemetry.RecordFromObserve(row)
	}
}

// AppPlanResolver names an app's plan; the deployment label set caps an
// app's deployment series by plan. ok is false when the app is unknown.
type AppPlanResolver func(ctx context.Context, appID string) (plan api.Plan, ok bool)

// WithAppPlanResolver lets service calls, which arrive without a resolved
// public App, record request metrics under the target's plan.
func (h *Handler) WithAppPlanResolver(resolve AppPlanResolver) *Handler {
	h.appPlan = resolve
	return h
}

// observeServiceRequestMetrics records a service call in the request
// duration series app health, SLOs, error-rate alerts and the platform outage
// detector read. Service calls reached request telemetry only, so an
// internal-only service (every gRPC backend of a microservices app) read
// "unknown: request_evidence_unavailable" in app health and could never trip
// an SLO or outage alert (microservices-demo, 2026-11-16). The public edge
// records its own requests in observe; a service call never passes there.
func (h *Handler) observeServiceRequestMetrics(r *http.Request, o ServiceRequestObservation) {
	appID := o.Target.AppID
	if h.metrics == nil || appID == "" || o.Status < 100 || o.Status > 599 || isRouteProbe(r.Context()) {
		return
	}
	plan := api.PlanFree
	if h.appPlan != nil {
		if resolved, ok := h.appPlan(r.Context(), appID); ok {
			plan = resolved
		}
	}
	h.metrics.PreInstantiateApp(appID)
	// The SLO burn-rate rules, the platform availability figure and schedd's
	// scale-up trigger read gateway_requests_total, not the duration series:
	// without this an internal-only service never reached them (prod
	// 2026-11-24: 156 apps served requests in 24 h, 77 had request series).
	// The platform availability figure subtracts the service part.
	h.metrics.ObserveServiceRequest(appID, string(plan), statusClass(o.Status))
	deployment := h.metrics.deploymentLabels.admit(appID, plan, o.Target.DeploymentID)
	h.metrics.ObserveRequestDurationByDeploymentWithTrace(appID, statusClassBucket(o.Status), deployment,
		time.Duration(o.LatencyMS)*time.Millisecond, traceIDFromContext(r.Context()))
}

// serviceRequestRoute labels a service call with the target app's route, as
// a public request to the same path would be labeled: its declared template
// or the inferred path, admitted into the app's bounded label set once the
// app answered with a status that establishes the route. Internal callers are
// customer reach, so route health, monitoring and route removal must see them
// (ADR-20261028110044 amendment 7).
func (h *Handler) serviceRequestRoute(r *http.Request, o ServiceRequestObservation) string {
	if !routeLabelMethodAllowed(r.Method) || r.URL == nil {
		return otherRouteLabel
	}
	set := h.routeSetFor(o.Target.AppID, true)
	if set == nil {
		return otherRouteLabel
	}
	path, declared := inferredObservedPath(r.URL.Path), false
	if resolver, ok := h.declaredRoutes.(ObservedRouteResolver); ok {
		app := App{ID: o.Target.AppID, AccountID: o.AccountID}
		if template, matched, err := resolver.ResolveObservedRoute(r.Context(), app, r.URL.Path, r.Method); err == nil && matched {
			path, declared = template, true
		}
	}
	route := observedRouteLabel(r.Method, path)
	if route == otherRouteLabel {
		return route
	}
	if label := set.labelBeforeResponse(route, declared); label != "" {
		return label
	}
	if statusEstablishesRoute(o.Status) {
		return set.admit(route)
	}
	return otherRouteLabel
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
	uaFamily := telemetryUnknownDimension
	if o.InvocationSource != "" {
		uaFamily = invocationClientFamily(o.InvocationSource)
	}
	return RequestTelemetryRow{
		AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
		Route: otherRouteLabel, Method: r.Method, Status: o.Status, LatencyMS: o.LatencyMS,
		ColdBoot: o.ColdBoot, TraceID: traceID, TraceInferred: traceID != "" && traceInferredOf(r), ReceivedAt: o.ReceivedAt, WakeID: o.WakeID,
		GuestDurationMS: o.Guest.DurationMS, GuestRuntime: o.Guest.Runtime, GuestOutcome: o.Guest.Outcome, GuestErrorClass: o.Guest.ErrorClass,
		InstanceID: o.Target.InstanceID, NodeID: o.Target.NodeID, Region: o.Target.Region,
		CommitSHA: o.Target.CommitSHA, DeploymentTag: o.Target.DeploymentTag,
		DeploymentCreatedAt: o.Target.DeploymentCreatedAt, ImageDigest: o.Target.ImageDigest,
		UAFamily: uaFamily, ReferrerHost: telemetryNoReferrer, Country: telemetryUnknownDimension,
		// The receiver already uses this flag to suppress legacy usage for
		// nonfinancial rows, including rejected ingress admissions. No outbox
		// write or caller consumer/tenant identity is claimed by this debug row.
		UsageOutboxed: true, preserveExact: o.ScenarioTestRunID != "",
	}, true
}
