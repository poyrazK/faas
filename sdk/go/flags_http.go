package faas

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
)

type flagPropagationEnvelope struct {
	Version    int                      `json:"version"`
	CustomerID string                   `json:"customer_id"`
	Decisions  []flagPropagatedDecision `json:"decisions"`
}

type flagPropagatedDecision struct {
	FlagDecision
	Origin FlagDecisionOrigin `json:"origin"`
}

// Middleware scopes evaluation to one ingress request, trusts only Gregale's
// reserved customer/context headers, and adds bounded evidence before response
// headers are committed. Apply it only on the app's Gregale gateway listener.
func (f *GregaleFlags) Middleware(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f == nil {
			next.ServeHTTP(w, r)
			return
		}
		_ = f.refreshIfStale(r.Context())
		state := f.newRequestState(r.Header)
		request := r.WithContext(context.WithValue(r.Context(), flagRequestContextKey{}, state))
		writer, evidenceWriter := wrapFlagEvidenceWriter(w, state)
		returned := false
		defer func() {
			if returned {
				evidenceWriter.finish()
			}
		}()
		next.ServeHTTP(writer, request)
		returned = true
	})
}

func (f *GregaleFlags) newRequestState(headers http.Header) *flagRequestState {
	f.stateMu.RLock()
	bundle := f.bundle
	age := f.now().Round(0).Sub(f.refreshed)
	fresh := bundle != nil && !f.refreshed.IsZero() && age >= 0 && age <= f.maxStale
	f.stateMu.RUnlock()

	rawCustomer := singleFlagHeader(headers, GregaleFlagCustomerHeader)
	propagation := decodeFlagPropagation(singleFlagHeader(headers, GregaleFlagContextHeader))
	if propagation != nil && rawCustomer != "" && !strings.EqualFold(rawCustomer, propagation.CustomerID) {
		propagation = nil
	}
	customer := ""
	var inherited map[string]flagPropagatedDecision
	if propagation != nil {
		customer = propagation.CustomerID
		inherited = make(map[string]flagPropagatedDecision, len(propagation.Decisions))
		for _, decision := range propagation.Decisions {
			inherited[decision.Flag] = decision
		}
	} else if validFlagUUID(rawCustomer) {
		customer = rawCustomer
	}
	if inherited == nil {
		inherited = map[string]flagPropagatedDecision{}
	}
	return &flagRequestState{
		owner: f, customer: customer, bundle: bundle, fresh: fresh,
		evidence: make(map[string]FlagEvidence), inherit: inherited,
	}
}

func singleFlagHeader(headers http.Header, name string) string {
	var values []string
	for key, rows := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		values = append(values, rows...)
	}
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func decodeFlagPropagation(value string) *flagPropagationEnvelope {
	if value == "" || len(value) > maxFlagContextHeaderSize {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 || len(raw) > maxFlagContextBytes || base64.RawURLEncoding.EncodeToString(raw) != value {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var envelope flagPropagationEnvelope
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(new(any)) != io.EOF {
		return nil
	}
	if envelope.Version != 1 || !validFlagUUID(envelope.CustomerID) || len(envelope.Decisions) == 0 || len(envelope.Decisions) > maxFlagEvidence {
		return nil
	}
	seen := make(map[string]struct{}, len(envelope.Decisions))
	for i := range envelope.Decisions {
		propagated := &envelope.Decisions[i]
		decision := &propagated.FlagDecision
		if !validPropagatedDecision(decision, propagated.Origin) {
			return nil
		}
		if _, exists := seen[decision.Flag]; exists {
			return nil
		}
		seen[decision.Flag] = struct{}{}
		if decision.Type == "boolean" {
			decision.Type = ""
		}
		decision.InheritedFrom = nil
	}
	return &envelope
}

func validPropagatedDecision(decision *FlagDecision, origin FlagDecisionOrigin) bool {
	if !validFlagKey(decision.Flag) || decision.ConfigVersion < 0 || decision.ConfigVersion > maxFlagVersion || !validFlagUUID(origin.AppID) || !validFlagUUID(origin.EnvironmentID) || decision.InheritedFrom != nil {
		return false
	}
	switch decision.Type {
	case "", "boolean":
		if _, ok := decision.Value.(bool); !ok {
			return false
		}
	case "variant":
		value, ok := decision.Value.(string)
		if !ok || !validFlagKey(value) {
			return false
		}
	default:
		return false
	}
	if decision.RuleID != "" && !validFlagKey(decision.RuleID) || !validFlagBucket(decision.Bucket) || !validFlagBucket(decision.RolloutBucket) {
		return false
	}
	fallbackReason := isFlagFallbackReason(decision.Reason)
	if (decision.Source == "fallback") != fallbackReason || decision.Source != "configuration" && decision.Source != "fallback" {
		return false
	}
	if decision.Reason == "rule_match" {
		if decision.RuleID == "" {
			return false
		}
	} else if decision.Reason != "flag_missing" && decision.Reason != "default" && decision.Reason != "disabled" && decision.Reason != "customer_missing" && decision.Reason != "configuration_stale" && decision.Reason != "type_mismatch" {
		return false
	} else if decision.RuleID != "" || decision.Bucket != nil || decision.RolloutBucket != nil {
		return false
	}
	return true
}

func validFlagBucket(value *int) bool { return value == nil || *value >= 0 && *value < 10_000 }

// GregaleFlagsTransport removes caller-supplied flag context from every request
// and forwards the current request's explicitly used decisions only to managed
// Gregale services. Requests must use the incoming request's context.
type GregaleFlagsTransport struct {
	Flags *GregaleFlags
	Base  http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t GregaleFlagsTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	removeFlagHeader(clone.Header, GregaleFlagContextHeader)
	if t.Flags != nil && isGregaleManagedService(request.URL.Hostname()) {
		if value := t.Flags.PropagationHeader(request.Context()); value != "" {
			clone.Header.Set(GregaleFlagContextHeader, value)
		}
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func removeFlagHeader(headers http.Header, name string) {
	for key := range headers {
		if strings.EqualFold(key, name) {
			delete(headers, key)
		}
	}
}

func isGregaleManagedService(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(host, ".svc.gregale") && len(host) > len(".svc.gregale")
}

type flagEvidenceResponseWriter struct {
	http.ResponseWriter
	state    *flagRequestState
	wrote    bool
	hijacked bool
}

func wrapFlagEvidenceWriter(w http.ResponseWriter, state *flagRequestState) (http.ResponseWriter, *flagEvidenceResponseWriter) {
	base := &flagEvidenceResponseWriter{ResponseWriter: w, state: state}
	flusher, hasFlusher := w.(http.Flusher)
	hijacker, hasHijacker := w.(http.Hijacker)
	pusher, hasPusher := w.(http.Pusher)
	switch {
	case hasFlusher && hasHijacker && hasPusher:
		return &flagEvidenceWriterFHP{flagEvidenceResponseWriter: base, flusher: flusher, hijacker: hijacker, pusher: pusher}, base
	case hasFlusher && hasHijacker:
		return &flagEvidenceWriterFH{flagEvidenceResponseWriter: base, flusher: flusher, hijacker: hijacker}, base
	case hasFlusher && hasPusher:
		return &flagEvidenceWriterFP{flagEvidenceResponseWriter: base, flusher: flusher, pusher: pusher}, base
	case hasHijacker && hasPusher:
		return &flagEvidenceWriterHP{flagEvidenceResponseWriter: base, hijacker: hijacker, pusher: pusher}, base
	case hasFlusher:
		return &flagEvidenceWriterF{flagEvidenceResponseWriter: base, flusher: flusher}, base
	case hasHijacker:
		return &flagEvidenceWriterH{flagEvidenceResponseWriter: base, hijacker: hijacker}, base
	case hasPusher:
		return &flagEvidenceWriterP{flagEvidenceResponseWriter: base, pusher: pusher}, base
	default:
		return base, base
	}
}

func (w *flagEvidenceResponseWriter) Header() http.Header { return w.ResponseWriter.Header() }

func (w *flagEvidenceResponseWriter) WriteHeader(statusCode int) {
	if w.wrote {
		return
	}
	w.setEvidence()
	w.wrote = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *flagEvidenceResponseWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *flagEvidenceResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *flagEvidenceResponseWriter) setEvidence() {
	removeFlagHeader(w.Header(), GregaleFlagEvidenceHeader)
	rows := make([]FlagEvidence, 0, len(w.state.evidence))
	w.state.mu.Lock()
	for _, evidence := range w.state.evidence {
		evidence.FlagDecision = cloneFlagDecision(evidence.FlagDecision)
		rows = append(rows, evidence)
	}
	w.state.mu.Unlock()
	if len(rows) == 0 {
		return
	}
	// Keep the evidence order deterministic for logs and tests.
	slices.SortFunc(rows, func(a, b FlagEvidence) int { return strings.Compare(a.Flag, b.Flag) })
	raw, err := json.Marshal(rows)
	if err != nil || len(raw) > maxFlagEvidenceBytes {
		return
	}
	w.Header().Set(GregaleFlagEvidenceHeader, base64.RawURLEncoding.EncodeToString(raw))
}

func (w *flagEvidenceResponseWriter) finish() {
	if !w.wrote && !w.hijacked {
		// Leave the status uncommitted. An outer middleware may still write a
		// response after this handler returns; net/http will commit the response
		// header once the full middleware chain unwinds.
		w.setEvidence()
	}
}

type flagEvidenceWriterF struct {
	*flagEvidenceResponseWriter
	flusher http.Flusher
}

func (w *flagEvidenceWriterF) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	w.flusher.Flush()
}

type flagEvidenceWriterH struct {
	*flagEvidenceResponseWriter
	hijacker http.Hijacker
}

func (w *flagEvidenceWriterH) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.setEvidence()
	w.hijacked = true
	return w.hijacker.Hijack()
}

type flagEvidenceWriterP struct {
	*flagEvidenceResponseWriter
	pusher http.Pusher
}

func (w *flagEvidenceWriterP) Push(target string, options *http.PushOptions) error {
	w.setEvidence()
	return w.pusher.Push(target, options)
}

type flagEvidenceWriterFH struct {
	*flagEvidenceResponseWriter
	flusher  http.Flusher
	hijacker http.Hijacker
}

func (w *flagEvidenceWriterFH) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	w.flusher.Flush()
}
func (w *flagEvidenceWriterFH) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.setEvidence()
	w.hijacked = true
	return w.hijacker.Hijack()
}

type flagEvidenceWriterFP struct {
	*flagEvidenceResponseWriter
	flusher http.Flusher
	pusher  http.Pusher
}

func (w *flagEvidenceWriterFP) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	w.flusher.Flush()
}
func (w *flagEvidenceWriterFP) Push(target string, options *http.PushOptions) error {
	w.setEvidence()
	return w.pusher.Push(target, options)
}

type flagEvidenceWriterHP struct {
	*flagEvidenceResponseWriter
	hijacker http.Hijacker
	pusher   http.Pusher
}

func (w *flagEvidenceWriterHP) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.setEvidence()
	w.hijacked = true
	return w.hijacker.Hijack()
}
func (w *flagEvidenceWriterHP) Push(target string, options *http.PushOptions) error {
	w.setEvidence()
	return w.pusher.Push(target, options)
}

type flagEvidenceWriterFHP struct {
	*flagEvidenceResponseWriter
	flusher  http.Flusher
	hijacker http.Hijacker
	pusher   http.Pusher
}

func (w *flagEvidenceWriterFHP) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	w.flusher.Flush()
}
func (w *flagEvidenceWriterFHP) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.setEvidence()
	w.hijacked = true
	return w.hijacker.Hijack()
}
func (w *flagEvidenceWriterFHP) Push(target string, options *http.PushOptions) error {
	w.setEvidence()
	return w.pusher.Push(target, options)
}

var _ http.RoundTripper = GregaleFlagsTransport{}
