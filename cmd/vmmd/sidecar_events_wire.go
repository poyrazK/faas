// Wire helpers for the framework_ready receiver's sidecar
// emitter seam (issue #463 / ADR-069 / ADR-071 / PR-C).
//
// This file lives in the always-compiled surface (no build
// tag) so cmd/vmmd/main.go can call WithSidecarEmitter on
// every platform — the Linux-only receiver struct + dispatch
// loop live in framework_ready_recv.go and compile to the
// same symbol surface. The method body just stores the
// emitter on the receiver; the dispatch uses it on the
// linux-only recv loop, where SidecarEventEmitter's contract
// is consumed.

package main

// WithSidecarEmitter attaches the audit sink for sidecar
// event classes (issue #463 / ADR-069 / ADR-071 / PR-C).
// Production wires SidecarEventsThroughPlatform wrapping
// the canonical pkg/events.Platform; tests substitute a
// fake so the dispatch path is unit-testable without
// spinning up a real state.Store. A nil emitter is
// replaced by the no-op default so callers can opt out
// cleanly without nil-checking at the dispatch site.
// Receiver-pointer receiver so successive With* calls
// chain into the same listener.
//
// Defined here (not in framework_ready_recv.go) so the
// always-compiled cmd/vmmd/main.go can call it on every
// build; the underlying receiver struct + dispatch loop
// are linux-only.
func (r *FrameworkReadyReceiver) WithSidecarEmitter(emitter SidecarEventEmitter) *FrameworkReadyReceiver {
	if r == nil {
		return r
	}
	if emitter == nil {
		emitter = noopSidecarEventEmitter{}
	}
	r.emitter = emitter
	return r
}

// SidecarEmitter returns the receiver's currently-configured
// emitter, supplying the no-op default if WithSidecarEmitter
// hasn't been called. Used by tests that want to swap an
// emitter in without affecting the loop() reader (linux).
func (r *FrameworkReadyReceiver) SidecarEmitter() SidecarEventEmitter {
	if r == nil || r.emitter == nil {
		return noopSidecarEventEmitter{}
	}
	return r.emitter
}

// WithEventPublisher attaches the persistence seam for the in-guest event
// publish endpoint. The receiver supplies the trusted instance id; the
// callback is responsible for resolving account identity and writing the
// canonical event envelope. A nil callback leaves the endpoint disabled.
func (r *FrameworkReadyReceiver) WithEventPublisher(publisher GuestEventPublisher) *FrameworkReadyReceiver {
	if r == nil {
		return r
	}
	r.eventPublisher = publisher
	return r
}
