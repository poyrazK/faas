package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type forwardedHTTPResponseObservation struct {
	Status     int
	AppHandled bool
}

type forwardedHTTPResponseObserverKey struct{}

func withForwardedHTTPResponseObserver(ctx context.Context, observe func(status int, appHandled bool)) context.Context {
	if observe == nil {
		return ctx
	}
	return context.WithValue(ctx, forwardedHTTPResponseObserverKey{}, observe)
}

// recordForwardedHTTPResponse is called only for the bridge's initial
// response frame. A non-empty bridge error means vmmd synthesized the
// response before the guest handled the request.
func recordForwardedHTTPResponse(ctx context.Context, status int, appHandled bool) {
	if observe, ok := ctx.Value(forwardedHTTPResponseObserverKey{}).(func(int, bool)); ok && observe != nil {
		observe(status, appHandled)
	}
}

func (p *ServiceProxy) recordServiceRequestTelemetry(request *http.Request, target ServiceTarget,
	caller ServiceCaller, endpoint ServiceEndpoint, status int, coldBoot bool, startedAt time.Time) {
	if p.recordRequestTelemetry == nil || request == nil || endpoint.InstanceID == "" || status < 100 || status > 599 {
		return
	}
	method := request.Method
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions:
	default:
		// The telemetry schema deliberately accepts a closed, bounded method
		// set. Unsupported extension methods remain routable but are omitted
		// from this optional diagnostic stream.
		return
	}
	accountID, err := uuid.Parse(caller.AccountID)
	if err != nil {
		return
	}
	appID, err := uuid.Parse(target.AppID)
	if err != nil {
		return
	}
	deploymentID, err := uuid.Parse(endpoint.DeploymentID)
	if err != nil {
		return
	}

	receivedAt := p.now().UTC()
	latencyMS := receivedAt.Sub(startedAt).Milliseconds()
	if latencyMS < 0 {
		latencyMS = 0
	}
	traceID := traceIDForTelemetry(request.Context())
	if traceID == "" {
		traceID = telemetryTraceID(request.Header.Get(api.RequestIDHeader))
	}
	p.recordRequestTelemetry(RequestTelemetryRow{
		AccountID:           accountID,
		AppID:               appID,
		DeploymentID:        deploymentID,
		Route:               api.RequestTelemetryRouteServiceProxy,
		Method:              method,
		Status:              status,
		LatencyMS:           int(latencyMS),
		ColdBoot:            coldBoot,
		TraceID:             traceID,
		ReceivedAt:          receivedAt,
		Count:               1,
		InstanceID:          endpoint.InstanceID,
		UsageOutboxed:       true,
		UAFamily:            "__unknown__",
		ReferrerHost:        "__none__",
		Country:             "__unknown__",
		NodeID:              endpoint.NodeID,
		Region:              endpoint.Region,
		CommitSHA:           endpoint.CommitSHA,
		DeploymentTag:       endpoint.DeploymentTag,
		DeploymentCreatedAt: endpoint.DeploymentCreatedAt,
		ImageDigest:         endpoint.ImageDigest,
	})
}
