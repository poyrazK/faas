// mirror_dispatch.go — issue #72 / ADR-124 / ADR-125 PR-A3
// per-request mirror goroutine.
//
// The dispatch goroutine fires per customer request, schedules
// the mirror VM via Backend.ScheduleMirror (which routes to
// schedd and stamps mode='mirror' on the new instances row),
// then forwards a stripped copy of the source request to the
// mirror VM via an injected MirrorRoundTripper. The result is
// classified (status_diff / schema_diff / bodyDiff / crashed)
// via pkg/gateway/mirror_redact.go::ClassifyResult and the
// outcome is exposed via the gateway_mirror_dispatched_total
// metric.
//
// Detached-ctx discipline (ADR-098): the goroutine derives its
// own context from context.Background with a
// MirrorMaxLifetimeSeconds timeout so the customer's request
// cancellation never reaches the mirror — the customer response
// is already on the wire by the time dispatchMirror starts.
//
// No panic recovery (matches the WakeGate leader contract at
// pkg/gateway/gate.go:172-175 — "ensure never panics"). A panic
// in dispatchMirror propagates to slog.Panic and aborts the
// daemon; the deploy gate catches it.
//
// Test seam: MirrorRoundTripper lets tests inject a stub
// RoundTrip without standing up an httptest.Server +
// ReverseProxy. Production wires a default that uses
// http.Client.Do against the mirror target's host:port.

package gateway

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// MirrorRoundTripper (issue #72 / ADR-124 PR-A3) is the seam the
// dispatch goroutine uses to forward the mirror request to the
// mirror VM. Production wires NewDefaultMirrorRoundTripper
// (http.Client.Do against the target URL); tests inject a stub
// to assert on the request shape without standing up a real
// upstream.
type MirrorRoundTripper interface {
	RoundTripMirror(ctx context.Context, target *url.URL, req *http.Request) (*http.Response, error)
}

// defaultMirrorRoundTripper (issue #72 / ADR-124 PR-A3) uses
// http.Client.Do against the target URL. The mirror VM's
// host:port is whatever the gateway's local picker reports for
// the mirror deployment; the goroutine derives a fresh http.Client
// per request (the mirror goroutine fires once per customer
// request, not per second — the cost of a Client allocation is
// negligible against the cold-boot budget).
type defaultMirrorRoundTripper struct {
	client *http.Client
}

// mirrorResponseBodyCap bounds the response snapshot retained for mirror
// classification. The source request snapshot uses the same cap; retaining
// more bytes here would let a customer-controlled mirror response consume
// unbounded gateway memory.
const mirrorResponseBodyCap = int64(api.MirrorBodySnapshotCap)

// NewDefaultMirrorRoundTripper (issue #72 / ADR-124 PR-A3) is
// the production MirrorRoundTripper. nil client = the per-request
// http.DefaultClient (no Transport override). The per-request
// http.Client avoids sharing idle-conn state between distinct
// mirror goroutines on distinct targets.
func NewDefaultMirrorRoundTripper(c *http.Client) MirrorRoundTripper {
	if c == nil {
		c = http.DefaultClient
	}
	return &defaultMirrorRoundTripper{client: c}
}

func (d *defaultMirrorRoundTripper) RoundTripMirror(ctx context.Context, target *url.URL, req *http.Request) (*http.Response, error) {
	if target == nil {
		return nil, errors.New("mirror round-trip: nil target")
	}
	// Rewrite the request's URL to the target's scheme+host so
	// http.Client.Do dials target.Host with the request's path.
	req2 := req.Clone(ctx)
	req2.URL = &url.URL{Scheme: target.Scheme, Host: target.Host, Path: req.URL.Path, RawQuery: req.URL.RawQuery}
	req2.Host = target.Host
	req2.RequestURI = ""
	return d.client.Do(req2)
}

// dispatchMirror (issue #72 / ADR-124 / ADR-125 PR-A3) is the
// per-customer-request mirror goroutine. Fire-and-forget — the
// caller (handler fanout) launches it via `go` and does not
// block on its return.
//
// Contract:
//
//  1. Detached ctx (ADR-098) with MirrorMaxLifetimeSeconds budget.
//  2. ScheduleMirror via Backend → returns instanceID + wakeID on
//     admit, or an error wrapping sched.ErrMirrorSlotAtCapacity
//     on cap-at-max (mapped to metric result="cap_at_max").
//  3. Best-effort HTTP round-trip via the injected
//     MirrorRoundTripper. A round-trip failure surfaces as
//     crashed=true on the metric counter.
//  4. Classify result (status_diff / schema_diff / bodyDiff /
//     crashed) via mirror_redact.ClassifyResult using the
//     source-side snapshot the handler captured BEFORE fanout
//     (sourceBody bytes + rec.captureStatusForMirror()).
//  5. Metric increment (gateway_mirror_dispatched_total{result=...}
//     + gateway_mirror_latency_seconds + gateway_mirror_body_diff_total).
//
// Source snapshot discipline (PR-A3 code-review fixes #1 + #2):
// the handler reads r.Body into a bounded []byte and installs a
// mirrorStatusSink *atomic.Int32 on rec BEFORE the fanout goroutine
// is scheduled. We pass both to dispatchMirror so the goroutine
// does NOT touch r.Body again (which would race with the
// downstream ReverseProxy — code-review #2) and does NOT need to
// wait for the proxy to commit a status (the proxy is local + fast,
// so by the time the round-trip returns the sink has the status;
// code-review #1).
//
// The durable mirror_invocation_results ledger row insert is a
// commit-4 follow-on (the rollup goroutine owns the write path;
// the per-request ledger insert is a future optimisation to
// avoid a two-step record+rollup).
func (h *Handler) dispatchMirror(parentCtx context.Context, sourceInstanceID string, sourceTarget *Target, rule MirrorRuleRow, srcReq *http.Request, sourceBody []byte, rec *statusRecorder) {
	// The mirror goroutine outlives the customer's request. The goroutine's
	// own ctx is rooted at context.Background() with a MirrorMaxLifetimeSeconds
	// deadline (ADR-098 detached-ctx pattern, mirrors pkg/gateway/gate.go:172);
	// parentCtx is captured only so we can pull the committed status off the
	// proxy after WriteHeader fired. Per-call //nolint:contextcheck below.
	if h == nil || h.backend == nil {
		return
	}

	// 0. Per-rule concurrent mirror-VM cap (PR-A3 code-review fix #3).
	// Acquired BEFORE backend.ScheduleMirror so a cap-at-max goroutine
	// never burns a schedd wake on a request we're about to drop. The
	// slot is released on goroutine completion (the defer below) so
	// the cap reflects "VMs in flight" through round-trip complete —
	// not "admit attempts", which would under-count by orders of
	// magnitude (a cold-boot + 50ms serve takes ~10x the admit
	// window). The release runs even on the error path so a failed
	// round-trip / build doesn't leak the slot.
	if !h.tryAcquireMirrorSlot(rule.ID) {
		if h.metrics != nil {
			h.metrics.ObserveMirrorDispatched(rule.AppID, rule.ID, "cap_at_max")
		}
		if h.log != nil {
			h.log.Warn("mirror: cap at max", "rule_id", rule.ID, "app_id", rule.AppID,
				"source_instance_id", sourceInstanceID)
		}
		return
	}
	defer h.releaseMirrorSlot(rule.ID)

	timeout := time.Duration(api.MirrorMaxLifetimeSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 1. Schedule the mirror VM and retain its complete forwarding target.
	// Production implements MirrorTargetBackend so the request is delivered to
	// the admitted shadow instance, including its node and runtime port. Legacy
	// adapters keep the original single-box fallback but still stamp the mirror
	// instance/deployment identity onto the source target copy.
	var mirrorTarget Target
	var instanceID string
	var err error
	if targetBackend, ok := h.backend.(MirrorTargetBackend); ok {
		//nolint:contextcheck // ctx is rooted at context.Background() (ADR-098 detached-ctx pattern)
		mirrorTarget, err = targetBackend.ScheduleMirrorTarget(ctx, rule.AppID, rule.MirrorDeploymentID, rule.ID)
		instanceID = mirrorTarget.InstanceID
	} else {
		var wakeID string
		//nolint:contextcheck // ctx is rooted at context.Background() (ADR-098 detached-ctx pattern)
		instanceID, wakeID, err = h.backend.ScheduleMirror(ctx, rule.AppID, rule.MirrorDeploymentID, rule.ID)
		if sourceTarget != nil {
			mirrorTarget = *sourceTarget
		}
		mirrorTarget.AppID = rule.AppID
		mirrorTarget.InstanceID = instanceID
		mirrorTarget.DeploymentID = rule.MirrorDeploymentID
		mirrorTarget.WakeID = wakeID
	}
	if err != nil {
		resultLabel := "sched_error"
		if errors.Is(err, sched.ErrMirrorSlotAtCapacity) {
			resultLabel = "cap_at_max"
		} else if isCapAtMaxCode(err) {
			// liftErr (pkg/scheddgrpc/client.go:341-346) rebuilds
			// gRPC status errors as *api.Problem{Code: api.CodeMirrorSlotAtCapacity}
			// instead of wrapping sched.ErrMirrorSlotAtCapacity, so
			// errors.Is above misses the sentinel. Check the stable
			// Code too (PR-A3 code-review #5 fix).
			resultLabel = "cap_at_max"
		}
		if h.metrics != nil {
			h.metrics.ObserveMirrorDispatched(rule.AppID, rule.ID, resultLabel)
		}
		if h.log != nil {
			h.log.Warn("mirror: schedule failed", "rule_id", rule.ID, "app_id", rule.AppID,
				"err", err.Error(), "result", resultLabel, "source_instance_id", sourceInstanceID)
		}
		_ = instanceID
		return
	}

	// 2. Build the mirror request. We pass sourceBody directly (NOT
	// a request whose Body we re-read) — the bytes are already
	// captured, the proxy owns r.Body downstream, and reading
	// srcReq.Body here would close it on the source side (code-review
	// #2 fix).
	//nolint:contextcheck // ctx is detached (ADR-098)
	mirrorReq, buildErr := h.buildMirrorRequest(ctx, rule, srcReq, sourceBody)
	if buildErr != nil {
		if h.metrics != nil {
			h.metrics.ObserveMirrorDispatched(rule.AppID, rule.ID, "build_request_error")
		}
		if h.log != nil {
			h.log.Warn("mirror: build request failed", "rule_id", rule.ID, "err", buildErr.Error())
		}
		return
	}

	// 3. Round-trip through the same per-node HTTP→vmmd bridge as ordinary
	// customer traffic. Tests and legacy single-box deployments can still
	// inject/use the URL round-tripper fallback.
	start := time.Now()
	var resp *http.Response
	if rt := h.mirrorRoundTripper; rt != nil {
		//nolint:contextcheck // ctx is detached (ADR-098)
		resp, err = rt.RoundTripMirror(ctx, mirrorTargetURL(&mirrorTarget), mirrorReq)
	} else if h.proxyByNode != nil {
		identity := mirrorTarget.PlatformIdentity("", mirrorReq.Header.Get(api.RequestIDHeader))
		identity.AppID = rule.AppID
		identity.ApplyGuestHeaders(mirrorReq.Header)
		capture := newMirrorResponseCapture()
		h.proxyByNode(mirrorTarget).ServeHTTP(capture, mirrorReq)
		resp = capture.response()
	} else {
		//nolint:contextcheck // ctx is detached (ADR-098)
		resp, err = NewDefaultMirrorRoundTripper(nil).RoundTripMirror(ctx, mirrorTargetURL(&mirrorTarget), mirrorReq)
	}
	latency := time.Since(start)
	if err != nil {
		if h.metrics != nil {
			h.metrics.ObserveMirrorDispatched(rule.AppID, rule.ID, "mirror_roundtrip_error")
			h.metrics.ObserveMirrorLatency(rule.AppID, rule.ID, latency.Seconds())
		}
		if h.log != nil {
			h.log.Warn("mirror: round-trip failed", "rule_id", rule.ID, "err", err.Error())
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	mirrorBody, readErr := readMirrorResponseBody(resp.Body)
	if readErr != nil {
		if h.metrics != nil {
			h.metrics.ObserveMirrorDispatched(rule.AppID, rule.ID, "mirror_roundtrip_error")
			h.metrics.ObserveMirrorLatency(rule.AppID, rule.ID, latency.Seconds())
		}
		if h.log != nil {
			h.log.Warn("mirror: response body read failed", "rule_id", rule.ID, "err", readErr)
		}
		return
	}

	// 4. Classify. Source side: read the committed status from
	// rec's mirrorStatusSink (the proxy committed it via WriteHeader
	// microseconds ago — code-review #1 fix). bodyBytes were
	// captured before the proxy read them — safe to compare
	// directly (no body-close race — code-review #2 fix).
	srcStatus := rec.captureStatusForMirror()
	statusDiff, schemaDiff, bodyDiff, crashed := ClassifyResult(srcStatus, sourceBody, resp.StatusCode, mirrorBody)
	_ = schemaDiff
	_ = sourceInstanceID

	// 5. Metric.
	resultLabel := "ok"
	if crashed {
		resultLabel = "mirror_5xx"
	} else if statusDiff {
		resultLabel = "status_diff"
	} else if bodyDiff {
		resultLabel = "body_diff"
	}
	if h.metrics != nil {
		h.metrics.ObserveMirrorDispatched(rule.AppID, rule.ID, resultLabel)
		h.metrics.ObserveMirrorLatency(rule.AppID, rule.ID, latency.Seconds())
		if bodyDiff {
			h.metrics.ObserveMirrorBodyDiff(rule.AppID, rule.ID)
		}
	}
}

// mirrorResponseCapture is the bounded ResponseWriter used when a mirror is
// forwarded through the production vmmd bridge. It retains only the bytes the
// classifier can consume while reporting successful writes to the forwarder,
// preventing a customer-controlled mirror response from growing gateway RAM.
type mirrorResponseCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newMirrorResponseCapture() *mirrorResponseCapture {
	return &mirrorResponseCapture{header: make(http.Header)}
}

func (w *mirrorResponseCapture) Header() http.Header { return w.header }

func (w *mirrorResponseCapture) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *mirrorResponseCapture) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	remaining := int(mirrorResponseBodyCap) - w.body.Len()
	if remaining > 0 {
		_, _ = w.body.Write(p[:min(len(p), remaining)])
	}
	return len(p), nil
}

func (w *mirrorResponseCapture) response() *http.Response {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     w.header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(w.body.Bytes())),
	}
}

func readMirrorResponseBody(body io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(body, mirrorResponseBodyCap))
}

// isCapAtMaxCode (PR-A3 code-review #5 fix) is the *api.Problem
// sentinel-aware fallback for schedd errors that already crossed
// the gRPC boundary. liftErr in pkg/scheddgrpc/client.go:341-346
// rebuilds gRPC status errors as *api.Problem{Code: ...} — the
// stable Code (api.CodeMirrorSlotAtCapacity) survives but the
// sentinel sched.ErrMirrorSlotAtCapacity does NOT get wrapped.
// errors.Is alone misses the cap-at-max branch; check the Code
// too. Returns false for nil errors or non-problem errors.
func isCapAtMaxCode(err error) bool {
	if err == nil {
		return false
	}
	var prob *api.Problem
	if errors.As(err, &prob) && prob != nil {
		return prob.Code == api.CodeMirrorSlotAtCapacity
	}
	return false
}

// buildMirrorRequest (issue #72 / ADR-124 PR-A3, code-review
// fix PR-A3 #2) builds the http.Request the goroutine forwards
// to the mirror VM. Strips the always-stripped + customer-
// supplied redact_headers set (mirror_redact.StrippedRequestHeaders),
// preserves the source method / path / body. The Host header is
// intentionally NOT copied — the mirror VM is a sibling
// deployment of the same app; the gateway's bridge handles the
// host → netns mapping.
//
// The body bytes are passed in as sourceBody []byte (captured by
// the handler boundary BEFORE the proxy ran) — we do NOT read
// srcReq.Body here, because srcReq.Body is owned by the
// httputil.ReverseProxy downstream. Reading it here would Close()
// the source body and starve the customer's proxy (code-review
// #2). The bytes are referenced via bytes.NewReader so the
// mirror's http.Client can re-read them on a retry / chunked
// transfer without disturbing the source.
func (h *Handler) buildMirrorRequest(ctx context.Context, rule MirrorRuleRow, srcReq *http.Request, sourceBody []byte) (*http.Request, error) {
	if srcReq == nil {
		return nil, errors.New("mirror dispatch: nil source request")
	}
	rstateRule := state.MirrorRule{
		ID:                 rule.ID,
		AppID:              rule.AppID,
		MirrorDeploymentID: rule.MirrorDeploymentID,
		RedactHeaders:      rule.RedactHeaders,
	}
	stripped := StrippedRequestHeaders(rstateRule, srcReq.Header)

	method := srcReq.Method
	path := srcReq.URL.Path
	if path == "" {
		path = "/"
	}
	mirrorReq, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(sourceBody))
	if err != nil {
		return nil, fmt.Errorf("mirror dispatch: build mirror request: %w", err)
	}
	mirrorReq.Header = stripped
	mirrorReq.ContentLength = int64(len(sourceBody))
	return mirrorReq, nil
}

// mirrorTargetURL (issue #72 / ADR-124 PR-A3) derives the
// upstream URL the default round-tripper dials. For the A3
// single-box posture the source target's NodeID (host:port
// written by the wake path) is reused (the mirror VM is a
// sibling inside the same app's bridge); multi-box / cross-node
// mirror is an A4 follow-on. Nil target or empty NodeID falls
// back to the wire-default 127.0.0.1:8080 so a routing miss
// can't panic on URL construction.
func mirrorTargetURL(t *Target) *url.URL {
	if t == nil || t.NodeID == "" {
		return &url.URL{Scheme: "http", Host: "127.0.0.1:8080"}
	}
	return &url.URL{Scheme: "http", Host: t.NodeID}
}

// shouldMirrorRequest (issue #72 / ADR-124 PR-A3) returns true
// with probability rule.Percent/100. Cryptographic sampling via
// crypto/rand keeps the distribution uniform across requests
// rather than biased to clock-rate bursts. pickedDeploymentID is
// a salt so a customer can't game the sampler by sending bursts
// — the same request still has the same per-rule outcome across
// retries, but different requests land on different sides of the
// threshold.
//
// percent == 0 → always false (no spawn). percent >= 100 → always
// true (no spawn suppression).
func shouldMirrorRequest(percent int, pickedDeploymentID string) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	var b [1]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		// Sampling is best-effort. On a /dev/urandom failure,
		// mirror always — the per-rule cap (default 5) bounds
		// the cost and the customer's customer-facing request
		// is already on the wire.
		return true
	}
	threshold := uint64(percent) * 256 / 100
	return uint64(b[0]) < threshold
}

// cloneRequestForMirror (issue #72 / ADR-124 PR-A3) returns a
// snapshotRequestForMirror (issue #72 / ADR-133 / ADR-125 PR-A3
// code-review fix) returns a deep copy of the source request whose
// Body is a fresh bytes.Reader over the already-captured body
// snapshot. The handler boundary reads r.Body ONCE into a
// bounded []byte and hands both the snapshot and the bytes
// reader to the dispatch goroutine — that way the goroutine
// does NOT need to read srcReq.Body at all (which would race
// with the customer's downstream ReverseProxy, code-review #2:
// io.ReadAll then Close on the source body starved the
// httputil.ReverseProxy of its read).
func snapshotRequestForMirror(src *http.Request) *http.Request {
	if src == nil {
		return nil
	}
	clone := src.Clone(src.Context())
	clone.Header = src.Header.Clone()
	return clone
}

// snapshotSourceBody (issue #72 / ADR-133 / ADR-125 PR-A3
// code-review fix) reads the source request body into a bounded
// []byte and returns it alongside a restore closure that wires
// r.Body back to a fresh bytes.Reader over the SAME underlying
// bytes. The dispatch goroutine reads from a separate reader
// (its own bytes.Reader from the snapshot), so the source VM's
// ReverseProxy downstream sees an intact Body regardless of
// goroutine scheduling.
//
// The cap is api.MirrorBodySnapshotCap bytes (default 64 KiB)
// — enough to detect status_diff / body_diff on a typical JSON
// response, but bounded so a 1 GiB POST doesn't OOM the
// gateway. Bodies exceeding the cap return a short snapshot
// (truncated to cap) and the dispatch goroutine treats the
// truncation as a soft "unknown body" (ClassifyResult emits
// statusDiff=true to surface the "we don't know what the
// source did" shape rather than a silent no-diff).
func snapshotSourceBody(r *http.Request) (body []byte, restore func()) {
	if r == nil || r.Body == nil {
		return nil, func() {}
	}
	original := r.Body
	cap := int64(api.MirrorBodySnapshotCap)
	limited := io.LimitReader(original, cap)
	buf, err := io.ReadAll(limited)
	restore = func() {
		// Replay the captured prefix and then continue from the original
		// admitted body. Keeping the unread tail is load-bearing for bodies
		// larger than MirrorBodySnapshotCap; replacing the body with buf alone
		// would silently truncate the customer request.
		r.Body = &prefixReplayReadCloser{
			Reader: io.MultiReader(bytes.NewReader(buf), original),
			Closer: original,
		}
	}
	if err != nil {
		// Capture failed (MaxBytesReader trip, network blip).
		// Return nil to the mirror classifier, but still replay any prefix
		// already consumed so the source request is not corrupted.
		return nil, restore
	}
	return buf, restore
}

type prefixReplayReadCloser struct {
	io.Reader
	io.Closer
}

// tryAcquireMirrorSlot (issue #72 / ADR-133 / ADR-125 PR-A3
// code-review fix #3 — moved from schedd Engine) is the
// per-rule concurrent mirror-VM cost circuit. Returns true when
// the slot was acquired (count is now 1..cap inclusive); false
// when the slot is already at cap and the goroutine should drop
// the request. The slot is released by releaseMirrorSlot, which
// the dispatch goroutine fires via defer when the round-trip
// completes (NOT when AdmitMirrorInstance returns — see
// pkg/sched/engine.go::AdmitMirrorInstance for the rationale).
//
// The cap (h.MirrorMaxConcurrentPerRule, default 5 via
// api.MirrorMaxConcurrentPerRule) bounds the concurrent mirror
// VM count per rule so a runaway customer rule cannot pin the
// gateway's wake-coord budget.
//
// sync.Map's LoadOrStore handles the first-write-under-contention
// race: whichever goroutine lands first allocates the
// *atomic.Int64; concurrent callers reuse the winner's pointer.
// nil-receiver safe — returns false (no acquire) when called on
// a nil Handler.
func (h *Handler) tryAcquireMirrorSlot(ruleID string) bool {
	if h == nil {
		return false
	}
	cap := h.MirrorMaxConcurrentPerRule
	if cap <= 0 {
		cap = api.MirrorMaxConcurrentPerRule // safety: never 0 or negative
	}
	fresh := &atomic.Int64{}
	actual, _ := h.mirrorSlots.LoadOrStore(ruleID, fresh)
	slot := actual.(*atomic.Int64)
	cur := slot.Add(1)
	if cur > cap {
		// Over-cap — undo and report failure.
		slot.Add(-1)
		return false
	}
	return true
}

// releaseMirrorSlot (issue #72 / ADR-133 / ADR-125 PR-A3
// code-review fix #3 — moved from schedd Engine) is the
// round-trip-complete callback the dispatch goroutine fires
// via defer. Decrements the per-rule counter; never below zero
// (a defensive floor — a release without a matching acquire
// would be a goroutine bug, but the floor keeps the invariant
// observable at the goroutine level rather than as a panicking
// atomic underflow). The rule ID is also removed from the
// mirrorSlots map when the count hits zero so a customer who
// disables and re-enables the rule (via PATCH
// /v1/apps/{slug}/mirrors/{id}) doesn't accumulate stale
// entries. LoadAndDelete is best-effort — a concurrent release
// from a second goroutine on the same rule just succeeds in
// releasing its own slot without claiming the deletion.
// nil-receiver safe — no-op when called on a nil Handler.
func (h *Handler) releaseMirrorSlot(ruleID string) {
	if h == nil {
		return
	}
	v, ok := h.mirrorSlots.Load(ruleID)
	if !ok {
		return
	}
	slot := v.(*atomic.Int64)
	for {
		cur := slot.Load()
		if cur <= 0 {
			return
		}
		if slot.CompareAndSwap(cur, cur-1) {
			if cur-1 == 0 {
				h.mirrorSlots.Delete(ruleID)
			}
			return
		}
	}
}
