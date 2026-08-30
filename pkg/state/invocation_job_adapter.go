package state

import "github.com/onebox-faas/faas/pkg/dispatch"

// InvocationJobAdapter is the dispatch.Job view of an Invocation
// (ADR-134 PR-B). It exists because Go forbids a field and a method
// of the same name on the same struct, and *state.Invocation already
// owns fields named ID, AppID, AccountID, Source, LastError, Attempts
// — names that pkg/dispatch.Job wants as methods. The adapter
// proxies each accessor to the underlying field; it carries no state
// of its own.
//
// All other Job methods (Kind, Origin, RetryPolicy, Deadline,
// CurrentAttempts, ErrorText, Snapshot) live directly on Invocation
// in types.go — they don't clash with field names (Origin vs Source,
// ErrorText vs LastError, Kind vs (no field), RetryPolicy/Deadline
// are derived from JSON/columns, Snapshot is JSON marshal).
//
// Drains and tests construct the adapter with `state.NewInvocationJob(inv)`.
// The adapter is intentionally cheap (a single Invocation field);
// callers that hold a long-lived view should not mutate the wrapped
// Invocation out from under the adapter.
type InvocationJobAdapter struct {
	Invocation
}

// Compile-time check: *InvocationJobAdapter satisfies dispatch.Job.
// If a future PR adds a required method to the interface, this line
// fails to compile and pinpoints the gap at review time, not at
// runtime inside a drain.
var _ dispatch.Job = (*InvocationJobAdapter)(nil)

// NewInvocationJob wraps inv as a dispatch.Job.
func NewInvocationJob(inv Invocation) *InvocationJobAdapter {
	return &InvocationJobAdapter{Invocation: inv}
}

// ID implements dispatch.Job. Proxies the Invocation.ID field.
func (a *InvocationJobAdapter) ID() string { return a.Invocation.ID }

// AppID implements dispatch.Job. Proxies the Invocation.AppID field.
func (a *InvocationJobAdapter) AppID() string { return a.Invocation.AppID }

// AccountID implements dispatch.Job. Proxies the Invocation.AccountID
// field. Per-account concurrency caps (added in PR-B's
// account_async_quota table) key on this.
func (a *InvocationJobAdapter) AccountID() string { return a.Invocation.AccountID }
