package gateway

import (
	"context"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

const maxGuestExecutionDurationMS = 24 * 60 * 60 * 1000

type guestExecutionEvidence struct {
	mu                     sync.Mutex
	Runtime                string
	DurationMS             int
	Outcome                string
	ErrorClass             string
	CPUTimeMS              int
	PeakRSSMB              int
	ResourceUsageAvailable bool
	FlagEvidenceJSON       string
	// MeterUnits are the units the app reported in Gregale-Usage
	// (ADR-1318), summed per meter.
	MeterUnits map[string]int64
	// usageHeader is set while API monetization is on: only then is
	// Gregale-Usage a platform header that is consumed and removed
	// (ADR-20261018090210). Off, it reaches the client untouched.
	usageHeader  bool
	cpuUsageSeen bool
	peakRSSSeen  bool
	seen         bool
}

type guestExecutionEvidenceSnapshot struct {
	Runtime                string
	DurationMS             int
	Outcome                string
	ErrorClass             string
	CPUTimeMS              int
	PeakRSSMB              int
	ResourceUsageAvailable bool
	FlagEvidenceJSON       string
}

type guestExecutionEvidenceContextKey struct{}

func withGuestExecutionEvidence(r *http.Request, usageHeader bool) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), guestExecutionEvidenceContextKey{}, &guestExecutionEvidence{usageHeader: usageHeader}))
}

// usageHeaderOwned reports whether the request's Gregale-Usage header is
// the platform's to consume.
func usageHeaderOwned(ctx context.Context) bool {
	evidence, ok := ctx.Value(guestExecutionEvidenceContextKey{}).(*guestExecutionEvidence)
	return ok && evidence != nil && evidence.usageHeader
}

// guestExecutionEvidenceOf is the request's evidence collector, or nil.
func guestExecutionEvidenceOf(r *http.Request) *guestExecutionEvidence {
	evidence, _ := r.Context().Value(guestExecutionEvidenceContextKey{}).(*guestExecutionEvidence)
	return evidence
}

// meterUnits are the units the app reported, or nil.
func (e *guestExecutionEvidence) meterUnits() map[string]int64 {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.MeterUnits)
}

// addUsageHeader adds one Gregale-Usage value: comma-separated meter=units
// pairs. Malformed pairs are ignored, and at most
// state.MaxAppMetersPerRequest meters are kept.
func (e *guestExecutionEvidence) addUsageHeader(value string) {
	for _, pair := range strings.Split(value, ",") {
		meter, raw, ok := strings.Cut(strings.TrimSpace(pair), "=")
		meter = strings.TrimSpace(meter)
		units, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if !ok || err != nil || !state.ValidAppMeter(meter) || units < 0 || units > state.MaxAppMeterUnitsPerRequest {
			continue
		}
		if e.MeterUnits == nil {
			e.MeterUnits = map[string]int64{}
		}
		if _, known := e.MeterUnits[meter]; !known && len(e.MeterUnits) >= state.MaxAppMetersPerRequest {
			continue
		}
		e.MeterUnits[meter] = min(e.MeterUnits[meter]+units, state.MaxAppMeterUnitsPerRequest)
	}
}

// durationMS is the execution time the runtime reported, or 0.
func (e *guestExecutionEvidence) durationMS() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.DurationMS
}

func guestExecutionEvidenceFromContext(ctx context.Context) (guestExecutionEvidenceSnapshot, bool) {
	evidence, ok := ctx.Value(guestExecutionEvidenceContextKey{}).(*guestExecutionEvidence)
	if !ok || evidence == nil {
		return guestExecutionEvidenceSnapshot{}, false
	}
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	return guestExecutionEvidenceSnapshot{
		Runtime: evidence.Runtime, DurationMS: evidence.DurationMS,
		Outcome: evidence.Outcome, ErrorClass: evidence.ErrorClass,
		CPUTimeMS: evidence.CPUTimeMS, PeakRSSMB: evidence.PeakRSSMB,
		ResourceUsageAvailable: evidence.ResourceUsageAvailable,
		FlagEvidenceJSON:       evidence.FlagEvidenceJSON,
	}, evidence.seen
}

// recordGuestExecutionEvidence consumes a platform-owned runner header. The
// values are closed and bounded before they enter telemetry, and the header
// is always removed from the customer-visible response when recognized.
func recordGuestExecutionEvidence(ctx context.Context, name, value string) bool {
	evidence, ok := ctx.Value(guestExecutionEvidenceContextKey{}).(*guestExecutionEvidence)
	if !ok || evidence == nil {
		return false
	}
	name = http.CanonicalHeaderKey(strings.TrimSpace(name))
	value = strings.TrimSpace(value)
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	switch name {
	case api.UsageHeader:
		if !evidence.usageHeader {
			return false
		}
		evidence.addUsageHeader(value)
		return true
	case api.FlagEvidenceHeader:
		raw, err := flags.DecodeEvidenceHeader(value)
		if err == nil {
			evidence.FlagEvidenceJSON = raw
			evidence.seen = true
		}
		return true
	case api.GuestEvidenceDurationHeader:
		duration, err := strconv.Atoi(value)
		if err != nil || duration < 0 || duration > maxGuestExecutionDurationMS {
			return true
		}
		evidence.DurationMS = duration
		evidence.seen = true
	case api.GuestEvidenceRuntimeHeader:
		if !validGuestRuntime(value) {
			return true
		}
		evidence.Runtime = value
		evidence.seen = true
	case api.GuestEvidenceOutcomeHeader:
		if !validGuestOutcome(value) {
			return true
		}
		evidence.Outcome = value
		evidence.seen = true
	case api.GuestEvidenceErrorClassHeader:
		if !validGuestErrorClass(value) {
			return true
		}
		evidence.ErrorClass = value
		evidence.seen = true
	case api.GuestEvidenceCPUTimeHeader:
		resource, err := strconv.Atoi(value)
		if err != nil || resource < 0 || resource > maxGuestExecutionDurationMS {
			return true
		}
		evidence.CPUTimeMS = resource
		evidence.cpuUsageSeen = true
		evidence.ResourceUsageAvailable = evidence.cpuUsageSeen && evidence.peakRSSSeen
		evidence.seen = true
	case api.GuestEvidencePeakRSSHeader:
		resource, err := strconv.Atoi(value)
		if err != nil || resource < 0 || resource > 65536 {
			return true
		}
		evidence.PeakRSSMB = resource
		evidence.peakRSSSeen = true
		evidence.ResourceUsageAvailable = evidence.cpuUsageSeen && evidence.peakRSSSeen
		evidence.seen = true
	default:
		return false
	}
	return true
}

func validGuestRuntime(value string) bool {
	switch value {
	case "node22", "node24", "python312", "python313", "go124", "binary":
		return true
	default:
		return false
	}
}

func validGuestOutcome(value string) bool {
	switch value {
	case "ok", "http_error", "handler_error", "timeout", "canceled", "missing":
		return true
	default:
		return false
	}
}

func validGuestErrorClass(value string) bool {
	switch value {
	case "", "http_5xx", "handler_exec", "handler_protocol", "timeout", "canceled":
		return true
	default:
		return false
	}
}

func forwardedResponseHeader(ctx context.Context, dst http.Header, name, value string) {
	forwardedResponseHeaderWithUpgrade(ctx, dst, name, value, false)
}

func forwardedResponseHeaderWithUpgrade(ctx context.Context, dst http.Header, name, value string, preserveUpgrade bool) {
	if platformOwnsLifecycleHeader(ctx, name) {
		return
	}
	// Response headers cross the internal bridge before they reach the
	// customer-facing writer. Connection-management headers belong only to
	// that hop (RFC 7230 §6.1) and must not be exposed as guest application
	// metadata. The streaming forwarder uses this helper for both initial
	// response headers and trailers, so keep the boundary in one place.
	if isHopByHop(name) {
		isUpgradeHandshakeHeader := preserveUpgrade && (strings.EqualFold(strings.TrimSpace(name), "Connection") || strings.EqualFold(strings.TrimSpace(name), "Upgrade"))
		if !isUpgradeHandshakeHeader {
			return
		}
	}
	if strings.EqualFold(strings.TrimSpace(name), api.DeploymentIDHeader) || strings.EqualFold(strings.TrimSpace(name), apihostingreceipt.ServedResponseHeader) || strings.EqualFold(strings.TrimSpace(name), api.RevisionHeader) || strings.EqualFold(strings.TrimSpace(name), api.ReleaseHeader) {
		return
	}
	if strings.EqualFold(strings.TrimSpace(name), "Sec-WebSocket-Protocol") {
		var keep bool
		value, keep = stripManagedReleaseSubprotocol(value)
		if !keep {
			return
		}
	}
	if guestSetsManagedVersionCookie(ctx, name, value) {
		return
	}
	if guestSetsManagedReleaseContextCookie(ctx, name, value) {
		return
	}
	if !recordGuestExecutionEvidence(ctx, name, value) && !platformResponseHeader(ctx, name) {
		dst.Add(name, value)
	}
}

// The legacy reverse-proxy path copies response headers in one batch rather
// than calling forwardedResponseHeader. Filter guest attempts to overwrite
// edge-owned cookies; the edge's cookies live on the downstream writer and
// are not part of resp.Header.
func stripGuestManagedPlatformCookiesResponseHeader(resp *http.Response) {
	if resp == nil || resp.Header == nil || resp.Request == nil {
		return
	}
	ctx := resp.Request.Context()
	if !managedVersionCookieProtected(ctx) && !managedReleaseContextCookieProtected(ctx) {
		return
	}
	values := resp.Header.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, value := range values {
		if !guestSetsManagedVersionCookie(ctx, "Set-Cookie", value) &&
			!guestSetsManagedReleaseContextCookie(ctx, "Set-Cookie", value) {
			resp.Header.Add("Set-Cookie", value)
		}
	}
}

// platformResponseHeader reports whether name is a platform marker the guest
// must not pass to the client.
func platformResponseHeader(ctx context.Context, name string) bool {
	if http.CanonicalHeaderKey(strings.TrimSpace(name)) == api.UsageHeader {
		return usageHeaderOwned(ctx)
	}
	return isGuestEvidenceHeader(name)
}

func isGuestEvidenceHeader(name string) bool {
	switch http.CanonicalHeaderKey(strings.TrimSpace(name)) {
	case api.FlagEvidenceHeader, api.GuestEvidenceDurationHeader, api.GuestEvidenceRuntimeHeader,
		api.GuestEvidenceOutcomeHeader, api.GuestEvidenceErrorClassHeader,
		api.GuestEvidenceCPUTimeHeader, api.GuestEvidencePeakRSSHeader, api.UsageHeader:
		return true
	default:
		return false
	}
}

func stripGuestEvidenceResponseHeaders(resp *http.Response) {
	if resp == nil || resp.Header == nil {
		return
	}
	resp.Header.Del(api.RevisionHeader)
	resp.Header.Del(api.ReleaseHeader)
	resp.Header.Del(api.DeploymentIDHeader)
	resp.Header.Del(apihostingreceipt.ServedResponseHeader)
	ctx := context.Background()
	if resp.Request != nil {
		ctx = resp.Request.Context()
	}
	for _, name := range []string{
		api.FlagEvidenceHeader,
		api.GuestEvidenceDurationHeader,
		api.GuestEvidenceRuntimeHeader,
		api.GuestEvidenceOutcomeHeader,
		api.GuestEvidenceErrorClassHeader,
		api.GuestEvidenceCPUTimeHeader,
		api.GuestEvidencePeakRSSHeader,
		api.UsageHeader,
	} {
		if !platformResponseHeader(ctx, name) {
			continue
		}
		for _, value := range resp.Header.Values(name) {
			recordGuestExecutionEvidence(ctx, name, value)
		}
		resp.Header.Del(name)
	}
	if resp.Body != nil && usageHeaderOwned(ctx) {
		resp.Body = &usageTrailerReader{ReadCloser: resp.Body, resp: resp, ctx: ctx}
	}
}

// usageTrailerReader records a Gregale-Usage trailer (ADR-1318) once the
// body is read, before the reverse proxy copies trailers to the client.
type usageTrailerReader struct {
	io.ReadCloser
	resp *http.Response
	ctx  context.Context
	done bool
}

func (r *usageTrailerReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err == io.EOF && !r.done && r.resp.Trailer != nil {
		r.done = true
		for _, value := range r.resp.Trailer.Values(api.UsageHeader) {
			recordGuestExecutionEvidence(r.ctx, api.UsageHeader, value)
		}
		r.resp.Trailer.Del(api.UsageHeader)
	}
	return n, err
}
