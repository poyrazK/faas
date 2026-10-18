package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// usageAttribution is the customer a request's usage is billed to (ADR-234):
// a consumer, or a platform tenant reached through a consumer, a verified
// tenant surface or a JWT rule. Malformed context stamps are dropped.
type usageAttribution struct {
	ConsumerID, PlatformTenantID, PlatformTenantSurfaceID, PlatformTenantJWTAuthorizationRuleID string
}

// financial reports whether usage is billed to a customer.
func (a usageAttribution) financial() bool { return a.ConsumerID != "" || a.PlatformTenantID != "" }

func requestUsageAttribution(r *http.Request) usageAttribution {
	var a usageAttribution
	if identity, ok := authmw.ConsumerFromContext(r); ok {
		// Consumer IDs are UUIDs in the control-plane store. Keep
		// malformed/legacy context stamps out of the nullable DB
		// column rather than turning telemetry into a request failure.
		if parsed, err := uuid.Parse(identity.ID); err == nil {
			a.ConsumerID = parsed.String()
		}
	}
	auth := authenticatedFrom(r.Context())
	if parsed, err := uuid.Parse(auth.PlatformTenantID); err == nil &&
		(a.ConsumerID != "" || auth.PlatformTenantSurfaceID != "" || auth.PlatformTenantJWTAuthorizationRuleID != "") {
		a.PlatformTenantID = parsed.String()
	}
	if parsed, err := uuid.Parse(auth.PlatformTenantSurfaceID); err == nil && a.ConsumerID == "" && a.PlatformTenantID != "" {
		a.PlatformTenantSurfaceID = parsed.String()
	}
	if parsed, err := uuid.Parse(auth.PlatformTenantJWTAuthorizationRuleID); err == nil &&
		a.ConsumerID == "" && a.PlatformTenantSurfaceID == "" && a.PlatformTenantID != "" {
		a.PlatformTenantJWTAuthorizationRuleID = parsed.String()
	}
	return a
}

// usageEventInput is what one request's outbox event is built from.
type usageEventInput struct {
	eventID, accountID, appID uuid.UUID
	attr                      usageAttribution
	status                    int
	at                        time.Time
	latencyMS                 int
	requestID                 string
	target                    Target
	// computeMS and responseBytes are measured at commit for billable
	// consumer and tenant-source requests (ADR-1308, ADR-1310); zero
	// elsewhere.
	computeMS, responseBytes int64
	// meterUnits are the units the app reported (ADR-1318), billed for
	// consumers and tenant surfaces and JWT rules (ADR-1319).
	meterUnits map[string]int64
}

// usageEvent builds the outbox event for one request (ADR-234). The event ID
// is shared with the request-telemetry row.
func (h *Handler) usageEvent(r *http.Request, in usageEventInput) usageoutbox.Event {
	errorCount, billableUnits := int64(0), int64(1)
	if in.status >= 400 {
		errorCount = 1
	}
	if platformFailureUnbillable(r, in.status) {
		billableUnits = 0
	}
	event := usageoutbox.Event{
		EventID: in.eventID.String(), AccountID: in.accountID.String(), AppID: in.appID.String(),
		ConsumerID: in.attr.ConsumerID, PlatformTenantID: in.attr.PlatformTenantID,
		PlatformTenantSurfaceID:              in.attr.PlatformTenantSurfaceID,
		PlatformTenantJWTAuthorizationRuleID: in.attr.PlatformTenantJWTAuthorizationRuleID,
		WindowStart:                          in.at.UTC().Truncate(time.Minute),
		RequestCount:                         1, ErrorCount: errorCount, BillableUnits: billableUnits,
	}
	// Consumers and tenant surfaces and JWT rules are billed subjects whose
	// routes and resources rate cards can price (ADR-952, ADR-1308,
	// ADR-1310).
	if event.ConsumerID != "" || event.PlatformTenantSurfaceID != "" || event.PlatformTenantJWTAuthorizationRuleID != "" {
		event.BillingRoute = billingRouteFrom(r)
		if billableUnits > 0 {
			event.ComputeMS, event.ResponseBytes = max(in.computeMS, 0), max(in.responseBytes, 0)
			if len(in.meterUnits) > 0 {
				event.MeterUnits = in.meterUnits
			}
		}
	}
	if h.requestAuditEnabled || h.apiDiscoveryEnabled {
		event.DiscoveredRoute = auditRouteFrom(r)
		if event.DiscoveredRoute == otherRouteLabel {
			event.DiscoveredRoute = ""
		}
		if event.DiscoveredRoute != "" {
			event.DiscoveredAtUnixMs = in.at.UTC().UnixMilli()
		}
	}
	if h.requestAuditEnabled {
		event.Audit = h.usageAuditEvidence(r, in)
	}
	return event
}

func (h *Handler) usageAuditEvidence(r *http.Request, in usageEventInput) *usageoutbox.AuditEvidence {
	auditRoute := auditRouteFrom(r)
	if auditRoute == "" {
		auditRoute = otherRouteLabel
	}
	auditMethod := r.Method
	if auditMethod == "" || len(auditMethod) > 16 {
		auditMethod = "OTHER"
	}
	latencyMS := min(in.latencyMS, 86_400_000)
	deploymentID := in.target.DeploymentID
	if _, err := uuid.Parse(deploymentID); err != nil {
		deploymentID = ""
	}
	commitSHA := in.target.CommitSHA
	if len(commitSHA) > 64 {
		commitSHA = ""
	}
	auditRequestID := in.requestID
	if len(auditRequestID) > 128 {
		auditRequestID = ""
	}
	return &usageoutbox.AuditEvidence{
		RouteTemplate: auditRoute, Method: auditMethod,
		HTTPStatus: in.status, LatencyMS: latencyMS,
		TraceID:      traceIDForTelemetry(r.Context()),
		DeploymentID: deploymentID, CommitSHA: commitSHA,
		OccurredAt: in.at.UTC(), RequestID: auditRequestID,
		SourceIP: auditSourceIPFrom(r),
	}
}

// usageCommit makes a monetized request's usage durable before its response
// can complete (ADR-1505). It runs once; later calls return the first result.
type usageCommit struct {
	h                  *Handler
	r                  *http.Request
	target             Target
	attr               usageAttribution
	eventID            uuid.UUID
	accountID, appID   uuid.UUID
	mu                 sync.Mutex
	done, billable     bool
	err                error
	declaredLength     int64
	declaredLengthSeen bool
	// forwardedAt is when the request left for the guest, after any wake;
	// compute time falls back to it when the runtime reports no duration.
	forwardedAt time.Time
	holder      *usageCommitHolder
	evidence    *guestExecutionEvidence
}

// errUsageNotDurable refuses the bytes that would complete a response whose
// usage could not be recorded.
var errUsageNotDurable = fmt.Errorf("gateway: consumer usage could not be made durable; response withheld")

// commit makes the usage durable; responseBytes is the response's size once
// the bytes being written complete it.
func (c *usageCommit) commit(status int, responseBytes int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return c.err
	}
	c.done = true
	now := time.Now()
	event := c.h.usageEvent(c.r, usageEventInput{eventID: c.eventID, accountID: c.accountID, appID: c.appID,
		attr: c.attr, status: status, at: now, latencyMS: int(now.Sub(startTime(c.r)) / time.Millisecond),
		requestID: requestIDFrom(c.r), target: c.target, computeMS: c.computeMS(now), responseBytes: responseBytes,
		meterUnits: c.evidence.meterUnits()})
	c.billable = event.BillableUnits > 0
	if err := c.h.usageOutbox.Enqueue(event); err != nil {
		c.err = fmt.Errorf("%w: %w", errUsageNotDurable, err)
		if c.h.metrics != nil {
			c.h.metrics.IncUsageOutboxFailure()
		}
		c.h.log.Error("consumer usage commit failed", "err", err, "event_id", c.eventID)
	}
	return c.err
}

// computeMS is the request's execution time (ADR-1308): the duration the
// runtime reported, else the time since the request was forwarded, which
// excludes any wake.
func (c *usageCommit) computeMS(now time.Time) int64 {
	if duration := c.evidence.durationMS(); duration > 0 {
		return int64(duration)
	}
	if c.forwardedAt.IsZero() {
		return 0
	}
	return int64(now.Sub(c.forwardedAt) / time.Millisecond)
}

// mustAbort reports whether the response has to be aborted: its usage is
// billable but could not be recorded.
func (c *usageCommit) mustAbort() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done && c.err != nil && c.billable
}

// usageCommitHolder lets the request's context and its status recorder share
// the commit armed once the request is about to be forwarded.
type usageCommitHolder struct {
	mu     sync.Mutex
	commit *usageCommit
	// rec is the request's recorder; a commit made after the response was
	// written reads its size from it (ADR-1308).
	rec *statusRecorder
}

// writtenBytes is how many response bytes the recorder has written.
func (c *usageCommit) writtenBytes() int64 {
	if c.holder == nil || c.holder.rec == nil {
		return 0
	}
	return c.holder.rec.Bytes
}

func (h *usageCommitHolder) armed() *usageCommit {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.commit
}

type usageCommitKey struct{}

func withUsageCommitHolder(ctx context.Context, holder *usageCommitHolder) context.Context {
	return context.WithValue(ctx, usageCommitKey{}, holder)
}

func usageCommitFrom(ctx context.Context) *usageCommit {
	holder, _ := ctx.Value(usageCommitKey{}).(*usageCommitHolder)
	return holder.armed()
}

// armUsageCommit holds a monetized request's final response bytes until its
// usage is durable (ADR-1505). Called just before the request is forwarded;
// anonymous and audit-only traffic keeps the observe-time append.
func (h *Handler) armUsageCommit(r *http.Request, target Target) {
	holder, _ := r.Context().Value(usageCommitKey{}).(*usageCommitHolder)
	if holder == nil || h.usageOutbox == nil || isRouteProbe(r.Context()) || r.Context().Value(suppressFinancialUsageKey{}) == true {
		return
	}
	accountID, appID := accountIDFromContext(r.Context()), appIDFromContext(r.Context())
	attr := requestUsageAttribution(r)
	if accountID == uuid.Nil || appID == uuid.Nil || !attr.financial() {
		return
	}
	holder.mu.Lock()
	defer holder.mu.Unlock()
	if holder.commit == nil {
		holder.commit = &usageCommit{h: h, r: r, target: target, attr: attr, eventID: uuid.New(), accountID: accountID, appID: appID,
			forwardedAt: time.Now(), holder: holder, evidence: guestExecutionEvidenceOf(r)}
	}
}

// abortUnrecordedResponse runs at the handler's exit. It commits a response
// whose completion net/http writes after the handler returns (chunked, or
// without a body), and aborts the connection when the usage of a billable
// response could not be recorded, so the client never sees it complete.
func abortUnrecordedResponse(holder *usageCommitHolder, rec *statusRecorder) {
	commit := holder.armed()
	if commit == nil {
		return
	}
	_ = commit.commit(rec.status, rec.Bytes)
	if commit.mustAbort() {
		if commit.h.metrics != nil {
			commit.h.metrics.IncUsageCommitAbort()
		}
		panic(http.ErrAbortHandler)
	}
}

// noteDeclaredLength records how many body bytes complete the response once
// its header is committed: 0 for a response without a body, the declared
// Content-Length, or -1 when net/http frames the end (chunked).
func (c *usageCommit) noteDeclaredLength(method string, status int, header http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.declaredLengthSeen {
		return
	}
	c.declaredLengthSeen = true
	c.declaredLength = -1
	switch {
	case method == http.MethodHead || status == http.StatusNoContent || status == http.StatusNotModified:
		c.declaredLength = 0
	default:
		if n, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64); err == nil && n >= 0 {
			c.declaredLength = n
		}
	}
}

// completes reports whether writing n more bytes after written completes a
// response with a declared length.
func (c *usageCommit) completes(written, n int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.declaredLengthSeen && c.declaredLength >= 0 && written+n >= c.declaredLength
}
