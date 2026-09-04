// Package activity is a per-instance request activity counter for
// vmmd. It feeds the inflight_requests, last_request_at, and
// request_count_total wire fields on vmmdpb.InstanceStats. Schedd
// reads the in-flight gauge through instancestats.Reader.MaxInflightForApp
// and derives a provider-independent RPS signal from request_count_total
// for reactive scale-up.
//
// # Design
//
// Begin/End is the input shape: ForwardHTTP's defer pair calls
// Begin on validated entry and End after the bridge returns. The
// counter is *request-count*, not *connection-count*. A single
// long-lived netns-bridge netcat-style pipe over many requests
// ticks the counter many times; a single connection that idles
// between requests ticks once per request. This matches the
// customer's mental model for "concurrent_requests" — the
// scaling-policy target metric PR-A unlocked at Hobby+
// (pkg/api/limits.go Hobby row).
//
// This package does NOT implement connection counting. The
// gRPC handler chain in vmmdgrpc.ForwardHTTP is unary and the
// bridge opens a fresh netcat per request, so Begin/End and
// "concurrent bridge calls" are the same thing today.
//
// # Regression contract
//
//   - Empty instance id: Begin and End are no-ops. ForwardHTTP
//     returns InvalidArgument before either is reached, so this
//     is belt-and-suspenders for any future caller that forgets
//     the guard.
//   - Unmatched End (no Begin ever): End decrements a counter
//     that does not exist; the cache treats it as a no-op so a
//     late-arriving End (post-Destroy) cannot drive inflight
//     below zero and trip a Prometheus gauge assertion.
//   - Begin without End: counter stays elevated until Forget is
//     called from vmmdgrpc.Server.Destroy. This is intentional —
//     the gauge should reflect "what's actually in flight on
//     this instance", and a leaked goroutine between Begin and
//     End will recover on Destroy (not on End), which is the
//     last-resort cleanup vmmd owns.
//   - Concurrent Begin/End on the same instance: the mutex
//     serialises them; the counter is monotonic within the
//     mutex window. The cache_test.go TestConcurrent_BeginEnd-
//     Converges case pins this under -race.
//
// # Concurrency
//
// One mutex protects the per-instance state map. ForwardHTTP is
// unary and gRPC serialises per-RPC, so in practice Begin and End
// from a single handler never race with each other. The mutex is
// there to serialise against (a) many concurrent handler
// goroutines across different instances, and (b) a single Stats
// gRPC call reading the same map mid-Begin. Both paths are
// uncontended at the EX44 envelope (≤ a few hundred RPS/app).
// If a future profile shows contention, swap the map for
// sync.Map (mirror pkg/wire/metrics.go:2276-2298).
//
// # Wire contract
//
// Inflight returns the current count and a "seen-ever" boolean;
// LastAt returns the most recent Begin moment and a "seen-ever"
// boolean; Total returns the cumulative request count and a
// "seen-ever" boolean. Stats gRPC emits:
//
//   - row.InflightRequests = inflight when seen, else zero
//     (wrappers preserved for the schedd poller — the schedd
//     decode at pkg/sched/instancestats/poller.go:218-219
//     already handles zero as a valid "idle" reading).
//   - row.LastRequestAt = timestamppb.New(lastAt) when seen,
//     else nil.
//   - row.RequestCountTotal = wrapperspb.Int64(total) when seen,
//     else nil. Schedd treats a counter decrease as a new baseline
//     so vmmd restart or instance recreation cannot create a fake
//     scale-out burst.
//
// A never-observed instance therefore looks identical on the
// wire to today (pre-PR-B), which is the additive-merge the
// schedd poller was designed against.
//
// # Future work
//
// Issue #490 streaming introduces a bidi ForwardHTTPStream
// handler. The streaming variant must call Begin on the first
// Recv byte (NOT on connection open — a streaming RPC can live
// 900 s while idling) and End after the last Send (NOT on
// connection close). The Begin/End API is decoupled from gRPC
// stream lifecycle by design so this lands as a 4-line change in
// the streaming handler. Do not couple Begin to connection
// lifecycle in any future wiring.
package activity
