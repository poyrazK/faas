package gateway

import (
	"errors"
	"sync"

	"github.com/google/uuid"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// Bounds on the traces one accumulator buffers between flushes. A bucket
// lives from its first span until the flush after the one that empties it,
// so these bound memory by live traces, not by every trace ever seen: the
// accumulator used to keep every bucket forever, which stopped apid's guest
// ingest for every customer once 50,000 traces had arrived since it started
// and let one OTLP POST of distinct trace IDs grow gatewayd-public without
// limit.
const (
	// SpansAccumulatorMaxTraces bounds the traces buffered by one process.
	SpansAccumulatorMaxTraces = 50000
	// SpansAccumulatorMaxTracesPerAccount bounds one account's share, so a
	// single tenant cannot use the whole buffer.
	SpansAccumulatorMaxTracesPerAccount = 5000
)

// MaxSpansPerTraceForPlan is the plan's spans-per-trace cap for a flush. An
// empty or unknown plan (platform producers, an old caller) gets the Scale
// cap, the most any row keeps.
func MaxSpansPerTraceForPlan(plan string) int {
	if limits, ok := api.LimitsFor(api.Plan(plan)); ok && limits.DebugTelemetrySpansPerTrace > 0 {
		return limits.DebugTelemetrySpansPerTrace
	}
	return api.MustLimitsFor(api.PlanScale).DebugTelemetrySpansPerTrace
}

// ErrAccumulatorFull is returned by Add when buffering a new trace would
// pass the process or the account bound. The caller drops those spans (OTLP
// answers them as rejected in a partial success).
var ErrAccumulatorFull = errors.New("spans accumulator: trace buffer full")

// SpanTarget names the request rows a buffered trace merges into: apid's
// state.SpansSummaryTarget (tracing tenancy, 2026-10-31 logs/traces audit).
// AccountID is the storage namespace the rows carry; the other fields narrow
// it, and their zero values keep the account-wide merge.
type SpanTarget struct {
	AccountID uuid.UUID
	// AppID limits the merge to the producing app's rows.
	AppID uuid.UUID
	// OrgID merges a company-bound key's spans into that company's apps.
	OrgID uuid.UUID
	// CustomerCredential marks spans a customer key held by AccountID sent.
	CustomerCredential bool
}

// narrowed reports whether the target says more than the account.
func (t SpanTarget) narrowed() bool {
	return t.AppID != uuid.Nil || t.OrgID != uuid.Nil || t.CustomerCredential
}

// Request is apid's WriteSpansSummary request for one flush to this target.
func (t SpanTarget) Request(traceID string, summaryJSON []byte) *apidpb.WriteSpansSummaryRequest {
	req := &apidpb.WriteSpansSummaryRequest{
		TraceId: traceID, SummaryJson: summaryJSON, AccountId: t.AccountID.String(),
		CustomerCredential: t.CustomerCredential,
	}
	if t.AppID != uuid.Nil {
		req.AppId = t.AppID.String()
	}
	if t.OrgID != uuid.Nil {
		req.OrgId = t.OrgID.String()
	}
	return req
}

// spanBucketKey scopes a buffered trace to the target that sent it. Two
// accounts can legitimately carry one W3C trace ID (a caller forwards its
// traceparent to another account's app), and so can two apps of one account;
// each keeps its own spans, and the writer merges each into its own rows.
type spanBucketKey struct {
	target  SpanTarget
	traceID string
}

// SpansAccumulator buffers spans per (account, trace) between flushes.
type SpansAccumulator struct {
	buckets sync.Map // spanBucketKey -> *spansAccumulator

	maxTraces           int
	maxTracesPerAccount int

	mu         sync.Mutex // guards total and perAccount
	total      int
	perAccount map[uuid.UUID]int
}

// NewSpansAccumulator returns an empty accumulator with the default bounds.
func NewSpansAccumulator() *SpansAccumulator {
	return NewSpansAccumulatorWithBounds(SpansAccumulatorMaxTraces, SpansAccumulatorMaxTracesPerAccount)
}

// NewSpansAccumulatorWithBounds returns an empty accumulator with explicit
// bounds; a bound <= 0 means unbounded (tests only).
func NewSpansAccumulatorWithBounds(maxTraces, maxTracesPerAccount int) *SpansAccumulator {
	return &SpansAccumulator{
		maxTraces:           maxTraces,
		maxTracesPerAccount: maxTracesPerAccount,
		perAccount:          map[uuid.UUID]int{},
	}
}

// Add merges a batch into the (account, trace) bucket and returns the
// post-dedupe span count. The plan is unknown; see AddForPlan.
func (s *SpansAccumulator) Add(traceID string, accountID uuid.UUID, spans []summarizedSpan) (int, error) {
	return s.AddForTarget(traceID, SpanTarget{AccountID: accountID}, "", spans)
}

// AddForPlan is Add with the account's plan, which the flush uses to bound
// the trace to that plan's spans-per-trace cap.
func (s *SpansAccumulator) AddForPlan(traceID string, accountID uuid.UUID, plan string, spans []summarizedSpan) (int, error) {
	return s.AddForTarget(traceID, SpanTarget{AccountID: accountID}, plan, spans)
}

// AddForTarget is AddForPlan for spans whose producer names the rows they
// belong to (SpanTarget). Each target of a trace buffers and flushes apart.
func (s *SpansAccumulator) AddForTarget(traceID string, target SpanTarget, plan string, spans []summarizedSpan) (int, error) {
	key := spanBucketKey{target: target, traceID: traceID}
	for {
		bucket, err := s.bucketFor(key)
		if err != nil {
			return 0, err
		}
		bucket.mu.Lock()
		if bucket.retired {
			// The flush loop removed this bucket after we loaded it; load or
			// create the live one.
			bucket.mu.Unlock()
			continue
		}
		if plan != "" {
			bucket.plan = plan
		}
		added := bucket.appendLocked(spans)
		bucket.mu.Unlock()
		return added, nil
	}
}

// bucketFor loads the live bucket for key, creating it within the bounds.
func (s *SpansAccumulator) bucketFor(key spanBucketKey) (*spansAccumulator, error) {
	if v, ok := s.buckets.Load(key); ok {
		return v.(*spansAccumulator), nil
	}
	if !s.reserve(key.target.AccountID) {
		return nil, ErrAccumulatorFull
	}
	v, loaded := s.buckets.LoadOrStore(key, &spansAccumulator{traceID: key.traceID, accountID: key.target.AccountID})
	if loaded {
		s.release(key.target.AccountID)
	}
	return v.(*spansAccumulator), nil
}

func (s *SpansAccumulator) reserve(accountID uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxTraces > 0 && s.total >= s.maxTraces {
		return false
	}
	if s.maxTracesPerAccount > 0 && s.perAccount[accountID] >= s.maxTracesPerAccount {
		return false
	}
	s.total++
	s.perAccount[accountID]++
	return true
}

func (s *SpansAccumulator) release(accountID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total--
	if n := s.perAccount[accountID] - 1; n > 0 {
		s.perAccount[accountID] = n
	} else {
		delete(s.perAccount, accountID)
	}
}

// retireIfEmpty removes an empty bucket from the map. An Add holding the
// bucket's pointer sees retired and retries against the map.
func (s *SpansAccumulator) retireIfEmpty(key spanBucketKey, bucket *spansAccumulator) {
	bucket.mu.Lock()
	if len(bucket.spans) != 0 || bucket.retired {
		bucket.mu.Unlock()
		return
	}
	bucket.retired = true
	bucket.mu.Unlock()
	if s.buckets.CompareAndDelete(key, bucket) {
		s.release(key.target.AccountID)
	}
}

// DrainAndRemove extracts one trace's account-wide spans for an account and
// removes its bucket. Returns nil and the zero UUID when there is no bucket.
func (s *SpansAccumulator) DrainAndRemove(traceID string, accountID uuid.UUID) ([]summarizedSpan, uuid.UUID) {
	return s.DrainAndRemoveTarget(traceID, SpanTarget{AccountID: accountID})
}

// DrainAndRemoveTarget is DrainAndRemove for one target's bucket.
func (s *SpansAccumulator) DrainAndRemoveTarget(traceID string, target SpanTarget) ([]summarizedSpan, uuid.UUID) {
	key := spanBucketKey{target: target, traceID: traceID}
	accountID := target.AccountID
	v, ok := s.buckets.Load(key)
	if !ok {
		return nil, uuid.Nil
	}
	bucket := v.(*spansAccumulator)
	bucket.mu.Lock()
	out := bucket.spans
	bucket.spans = nil
	bucket.seenSpans = nil
	bucket.retired = true
	bucket.mu.Unlock()
	if s.buckets.CompareAndDelete(key, bucket) {
		s.release(key.target.AccountID)
	}
	return out, accountID
}

// Len returns the number of traces buffered now.
func (s *SpansAccumulator) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}
