// testhelpers_test.go — test-only re-exports of unexported seams.
//
// Lives in `package builderd` (not `package builderd_test`) so it can
// reach the unexported withSlotDecider. Production callers in
// `package builderd_test` (cmd/builderd, pkg/builderd/builderd_test.go,
// etc.) MUST NOT see this — the file's _test.go suffix is what keeps
// it out of the public Go surface.
//
// PR-B review finding M-4: previously `WithSlotDecider` was public,
// which let any caller (including scripts in `cmd/…`) swap the
// production slot decision and silently bypass the "builds never
// outrank tenant wakes" invariant. Hiding the seam behind
// package-internal access closes that hole.

package builderd

// WithSlotDecider is the test-only re-export of withSlotDecider.
// Mirrors the doc on the unexported function; callers must only use
// it from within test code.
func (b *Builderd) WithSlotDecider(f func(ResidencyProbe, int) SlotDecision) *Builderd {
	return b.withSlotDecider(f)
}
