// Tests for PGNodeVerifier (ADR-056).
//
// Coverage:
//   - Refresh replaces the snapshot on success.
//   - Loader failure keeps last-known-good (the load-bearing safety
//     property — a transient DB blip must not de-sync to "allow
//     nothing" and brick every mTLS leg).
//   - LookupCN accepts registered names and rejects unknown names.
//   - nil-receiver LookupCN returns nil (AllowAll).
//   - Refresh with nil receiver / nil loader errors loudly.
//   - Run drains the channel until ctx cancel.
//   - The drain-loop survives a loader failure on a delivered
//     notification (last-known-good).

package wire

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

// stubNodeLoader is a map-backed NodeLoader for tests. errFn
// (optional) lets a test inject a transient failure on the Nth call.
type stubNodeLoader struct {
	rows  []NodeRow
	calls atomic.Int32
	errFn func(call int32) error
}

func (s *stubNodeLoader) LoadNodes(_ context.Context) ([]NodeRow, error) {
	n := s.calls.Add(1)
	if s.errFn != nil {
		if err := s.errFn(n); err != nil {
			return nil, err
		}
	}
	// Return a copy so callers can't mutate the stub's slice.
	out := make([]NodeRow, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPGNodeVerifier_RefreshReplacesSnapshot(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
		{CN: "schedd", ID: "uuid-2"},
	}}
	v := NewPGNodeVerifier(loader, newSilentLogger())

	if got := v.Size(); got != 0 {
		t.Fatalf("Size()=%d on empty verifier; want 0", got)
	}

	n, err := v.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh err=%v; want nil", err)
	}
	if n != 2 {
		t.Fatalf("Refresh n=%d; want 2", n)
	}

	if err := v.LookupCN("vmmd"); err != nil {
		t.Errorf("LookupCN(vmmd)=%v; want nil", err)
	}
	if err := v.LookupCN("schedd"); err != nil {
		t.Errorf("LookupCN(schedd)=%v; want nil", err)
	}
	if err := v.LookupCN("unknown"); !errors.Is(err, ErrNodeVerifierCNMismatch) {
		t.Errorf("LookupCN(unknown)=%v; want ErrNodeVerifierCNMismatch", err)
	}
}

// TestPGNodeVerifier_LoaderFailureKeepsLastKnownGood is the
// load-bearing safety property: a transient loader failure on the
// SECOND Refresh must not erase the snapshot the FIRST Refresh
// populated. Without this, a single Postgres hiccup would brick
// every mTLS leg in the cluster.
func TestPGNodeVerifier_LoaderFailureKeepsLastKnownGood(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
	}}
	loader.errFn = func(call int32) error {
		if call == 2 {
			return errors.New("synthetic loader failure")
		}
		return nil
	}
	v := NewPGNodeVerifier(loader, newSilentLogger())

	// First Refresh succeeds — snapshot populated.
	if _, err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh err=%v; want nil", err)
	}
	if got := v.Size(); got != 1 {
		t.Fatalf("Size after first Refresh=%d; want 1", got)
	}

	// Second Refresh fails — snapshot must be preserved.
	_, err := v.Refresh(context.Background())
	if err == nil {
		t.Fatalf("second Refresh err=nil; want non-nil")
	}
	if got := v.Size(); got != 1 {
		t.Fatalf("Size after failed Refresh=%d; want 1 (last-known-good)", got)
	}
	if err := v.LookupCN("vmmd"); err != nil {
		t.Errorf("LookupCN(vmmd) after failed Refresh=%v; want nil (last-known-good)", err)
	}
}

func TestPGNodeVerifier_LookupCN_NilReceiverIsAllowAll(t *testing.T) {
	var v *PGNodeVerifier
	if err := v.LookupCN("anything"); err != nil {
		t.Errorf("nil receiver LookupCN()=%v; want nil (AllowAll)", err)
	}
	if got := v.Size(); got != 0 {
		t.Errorf("nil receiver Size()=%d; want 0", got)
	}
}

func TestPGNodeVerifier_Refresh_NilReceiverErrors(t *testing.T) {
	var v *PGNodeVerifier
	if _, err := v.Refresh(context.Background()); err == nil {
		t.Errorf("Refresh on nil receiver returned nil err; want non-nil")
	}
}

func TestPGNodeVerifier_Refresh_NilLoaderErrors(t *testing.T) {
	v := NewPGNodeVerifier(nil, newSilentLogger())
	if _, err := v.Refresh(context.Background()); err == nil {
		t.Errorf("Refresh with nil loader returned nil err; want non-nil")
	}
}

func TestPGNodeVerifier_Refresh_SkipsEmptyCNRows(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
		{CN: "", ID: "uuid-bad"},
		{CN: "schedd", ID: "uuid-2"},
	}}
	v := NewPGNodeVerifier(loader, newSilentLogger())
	n, err := v.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh err=%v; want nil", err)
	}
	if n != 2 {
		t.Fatalf("Refresh n=%d; want 2 (empty CN row skipped)", n)
	}
}

func TestPGNodeVerifier_Run_DrainsUntilCancel(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
	}}
	v := NewPGNodeVerifier(loader, newSilentLogger())

	// First Refresh before Run, so a CN is registered.
	if _, err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh err=%v; want nil", err)
	}

	ch := make(chan db.Notification, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- v.Run(ctx, ch) }()

	// Drain should keep refreshing on every notification.
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged}
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged}

	// Give the drain time to process at least one notification.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if loader.calls.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := loader.calls.Load(); got < 2 {
		t.Errorf("loader calls=%d; want >= 2 (Refresh + at least one notify-driven Refresh)", got)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run err=%v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestPGNodeVerifier_Run_SurvivesLoaderFailure asserts the drain
// loop keeps spinning after a loader failure on a notification
// (last-known-good preserved across multiple notify ticks).
func TestPGNodeVerifier_Run_SurvivesLoaderFailure(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
	}}
	loader.errFn = func(call int32) error {
		if call == 2 {
			return errors.New("synthetic failure")
		}
		return nil
	}
	v := NewPGNodeVerifier(loader, newSilentLogger())

	ch := make(chan db.Notification, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- v.Run(ctx, ch) }()

	// Initial Refresh inside Run was never called — but the first
	// notify drives a Refresh that fails. Last-known-good stays
	// empty (no prior snapshot), but the loop survives and tries
	// again on the next notify.
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged}
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if loader.calls.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := loader.calls.Load(); got < 2 {
		t.Errorf("loader calls=%d after failed notify; want >= 2 (loop survived failure)", got)
	}

	cancel()
	<-done
}

func TestPGNodeVerifier_Run_NilReceiverBlocksUntilCancel(t *testing.T) {
	var v *PGNodeVerifier
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- v.Run(ctx, make(chan db.Notification)) }()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run err=%v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// PR-5 / issue #911 — heartbeat-only payload discrimination.
//
// Post-00276 the channel split removes the keys-table payload from
// this consumer entirely: the verifier subscribes only to
// db.NotifyComputeNodesChanged (was the unified
// 'compute_node_changed' pre-00276, which carried both
// compute_nodes row writes AND compute_node_keys writes; the
// verifier dropped the keys payload via JSON-parse failure). The
// split means Run no longer has to filter — channel choice is the
// filter. The test below still uses the literal "compute_node_keys"
// payload as a defence-in-depth: post-00276 the verifier is
// physically subscribed to the nodes-changed channel and so should
// never see a keys payload on the wire, but the literal-string
// filter would still drop it if the publisher ever misroutes.

// TestPGNodeVerifier_Run_SkipsComputeNodeKeysPayload asserts that a
// notify carrying the literal "compute_node_keys" payload triggers
// zero loader calls. This is the discriminator fix for issue #911.
func TestPGNodeVerifier_Run_SkipsComputeNodeKeysPayload(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
	}}
	v := NewPGNodeVerifier(loader, newSilentLogger())

	// Initial Refresh to populate the snapshot (and register one
	// loader call as the baseline).
	if _, err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("initial Refresh err=%v; want nil", err)
	}
	baseline := loader.calls.Load()

	ch := make(chan db.Notification, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- v.Run(ctx, ch) }()

	// Send three keys-only notifies — none should drive a Refresh.
	for i := 0; i < 3; i++ {
		ch <- db.Notification{
			Channel: db.NotifyComputeNodesChanged,
			Payload: ComputeNodeKeysPayload,
		}
	}

	// Give the drain a moment to (not) process.
	time.Sleep(50 * time.Millisecond)
	if got := loader.calls.Load(); got != baseline {
		t.Errorf("loader calls=%d after keys-only notifies; want %d (no Refresh)",
			got, baseline)
	}

	cancel()
	<-done
}

// TestPGNodeVerifier_Run_RefreshesOnComputeNodePayload asserts that a
// notify carrying a JSON {node_id, active} payload (or any non-keys
// payload) drives a Refresh — i.e. the discriminator only filters
// the keys shape, everything else triggers the loader.
func TestPGNodeVerifier_Run_RefreshesOnComputeNodePayload(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
	}}
	v := NewPGNodeVerifier(loader, newSilentLogger())

	ch := make(chan db.Notification, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- v.Run(ctx, ch) }()

	// Two JSON-payload notifies should drive two Refresh calls.
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"uuid-1","active":true}`,
	}
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"uuid-2","active":false}`,
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if loader.calls.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := loader.calls.Load(); got < 2 {
		t.Errorf("loader calls=%d after JSON notifies; want >= 2", got)
	}

	cancel()
	<-done
}

// PR-5 / issue #911 — heartbeat-only short-circuit.
//
// Refresh's loader fetch always returns the SAME row set on every
// heartbeat-stamp write (active=true → true, no other field
// changes). The pre-PR-5 code unconditionally swapped the snapshot,
// churning downstream log lines and any consumer that diffs the
// snapshot. Refresh now short-circuits when fresh equals prior.

func TestPGNodeVerifier_Refresh_ShortCircuitsOnIdenticalSnapshot(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
		{CN: "schedd", ID: "uuid-2"},
	}}
	v := NewPGNodeVerifier(loader, newSilentLogger())

	// First Refresh populates the snapshot.
	if _, err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh err=%v; want nil", err)
	}

	// Capture the prior map address — second Refresh must NOT swap
	// it (snapshot unchanged, skip-swap contract).
	v.mu.RLock()
	priorAddr := v.snap
	v.mu.RUnlock()

	// Second Refresh with identical rows must short-circuit.
	if _, err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh err=%v; want nil", err)
	}

	// Probe the short-circuit contract via PublishSnapshot (which
	// reads the internal map under RLock). The internal map must
	// still expose the same CN set + IDs after the second Refresh.
	post := v.PublishSnapshot()
	if len(post) != len(priorAddr) {
		t.Fatalf("PublishSnapshot len=%d after identical Refresh; want %d",
			len(post), len(priorAddr))
	}
	for _, r := range post {
		if gotID, ok := priorAddr[r.CN]; !ok || gotID != r.ID {
			t.Errorf("PublishSnapshot[%q]=%q after identical Refresh; want %q (snapshot unchanged contract)",
				r.CN, r.ID, gotID)
		}
	}

	// Third Refresh with a different row set MUST swap (regression
	// check: the short-circuit must not freeze the snapshot).
	loader.rows = []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
		{CN: "schedd", ID: "uuid-2"},
		{CN: "imaged", ID: "uuid-3"},
	}
	if n, err := v.Refresh(context.Background()); err != nil || n != 3 {
		t.Fatalf("third Refresh n=%d, err=%v; want n=3, err=nil", n, err)
	}
	if got := v.Size(); got != 3 {
		t.Errorf("Size after content-changing Refresh=%d; want 3", got)
	}
}

// PR-5 / issue #911 — stable snapshot publish keys.
//
// PublishSnapshot returns a CN-sorted []NodeRow regardless of Go's
// randomized map iteration. This is the stable-publish-key contract
// for the doctor (PR-4) and release bundle (PR-3).

func TestPGNodeVerifier_PublishSnapshot_SortsByCN(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1"},
		{CN: "schedd", ID: "uuid-2"},
		{CN: "apid", ID: "uuid-3"},
	}}
	v := NewPGNodeVerifier(loader, newSilentLogger())
	if _, err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh err=%v; want nil", err)
	}

	// Run 100 iterations and assert the same CN-sorted order each time.
	var prev []NodeRow
	for i := 0; i < 100; i++ {
		got := v.PublishSnapshot()
		if len(got) != 3 {
			t.Fatalf("PublishSnapshot len=%d on iter %d; want 3", len(got), i)
		}
		if got[0].CN != "apid" || got[1].CN != "schedd" || got[2].CN != "vmmd" {
			t.Fatalf("PublishSnapshot order=%v on iter %d; want [apid,schedd,vmmd]",
				[]string{got[0].CN, got[1].CN, got[2].CN}, i)
		}
		if i == 0 {
			prev = got
			continue
		}
		// Slice identity should NOT alias the verifier's internal
		// map (mutating the returned slice must not affect future
		// PublishSnapshot calls).
		if &got[0] == &prev[0] {
			t.Errorf("PublishSnapshot aliased prior slice on iter %d", i)
		}
	}

	// Mutate the last-returned slice and re-publish — the verifier
	// must not be affected.
	last := v.PublishSnapshot()
	last[0].CN = "MUTATED"
	if again := v.PublishSnapshot(); again[0].CN != "apid" {
		t.Errorf("PublishSnapshot leaked mutation: again[0].CN=%q; want apid", again[0].CN)
	}
}

func TestPGNodeVerifier_PublishSnapshot_NilReceiver(t *testing.T) {
	var v *PGNodeVerifier
	if got := v.PublishSnapshot(); got != nil {
		t.Errorf("nil receiver PublishSnapshot=%v; want nil", got)
	}
}

func TestPGNodeVerifier_PublishSnapshot_Empty(t *testing.T) {
	loader := &stubNodeLoader{rows: nil}
	v := NewPGNodeVerifier(loader, newSilentLogger())
	// Note: no Refresh — snapshot stays empty.
	got := v.PublishSnapshot()
	if len(got) != 0 {
		t.Errorf("empty-snapshot PublishSnapshot len=%d; want 0", len(got))
	}
}

// TestPGNodeVerifier_CertFingerprintByCN covers PR-3:
//   - registered CN with a fingerprint returns the fingerprint
//   - registered CN with an empty fingerprint returns ErrCertFingerprintNotRegistered
//   - unknown CN returns ErrCertFingerprintNotRegistered
//   - nil receiver returns ErrCertFingerprintNotRegistered
//   - PublishSnapshot includes the fingerprint field (pre-PR-3
//     boxes published only CN+ID — the doctor consumes the new
//     field via PublishSnapshot too)
func TestPGNodeVerifier_CertFingerprintByCN(t *testing.T) {
	loader := &stubNodeLoader{rows: []NodeRow{
		{CN: "vmmd", ID: "uuid-1", CertFingerprint: "sha256:" + repeat64("a")},
		{CN: "schedd", ID: "uuid-2", CertFingerprint: "sha256:" + repeat64("b")},
		{CN: "imaged", ID: "uuid-3", CertFingerprint: ""}, // pre-PR-X box
	}}
	v := NewPGNodeVerifier(loader, newSilentLogger())
	if _, err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Registered CN with fingerprint.
	if got, err := v.CertFingerprintByCN("vmmd"); err != nil {
		t.Errorf("CertFingerprintByCN(vmmd) err=%v; want nil", err)
	} else if got != "sha256:"+repeat64("a") {
		t.Errorf("CertFingerprintByCN(vmmd) = %q; want %q", got, "sha256:"+repeat64("a"))
	}

	// Registered CN with empty fingerprint (pre-PR-X box).
	if got, err := v.CertFingerprintByCN("imaged"); !errors.Is(err, ErrCertFingerprintNotRegistered) {
		t.Errorf("CertFingerprintByCN(imaged) err=%v; want ErrCertFingerprintNotRegistered", err)
	} else if got != "" {
		t.Errorf("CertFingerprintByCN(imaged) = %q; want empty", got)
	}

	// Unknown CN.
	if got, err := v.CertFingerprintByCN("unknown"); !errors.Is(err, ErrCertFingerprintNotRegistered) {
		t.Errorf("CertFingerprintByCN(unknown) err=%v; want ErrCertFingerprintNotRegistered", err)
	} else if got != "" {
		t.Errorf("CertFingerprintByCN(unknown) = %q; want empty", got)
	}

	// nil receiver.
	var nilV *PGNodeVerifier
	if got, err := nilV.CertFingerprintByCN("vmmd"); !errors.Is(err, ErrCertFingerprintNotRegistered) {
		t.Errorf("nil CertFingerprintByCN err=%v; want ErrCertFingerprintNotRegistered", err)
	} else if got != "" {
		t.Errorf("nil CertFingerprintByCN = %q; want empty", got)
	}

	// PublishSnapshot must include the fingerprint.
	snap := v.PublishSnapshot()
	if len(snap) != 3 {
		t.Fatalf("PublishSnapshot len=%d; want 3", len(snap))
	}
	found := false
	for _, r := range snap {
		if r.CN == "vmmd" {
			found = true
			if r.CertFingerprint != "sha256:"+repeat64("a") {
				t.Errorf("PublishSnapshot vmmd CertFingerprint = %q; want %q", r.CertFingerprint, "sha256:"+repeat64("a"))
			}
		}
	}
	if !found {
		t.Errorf("PublishSnapshot missing vmmd row")
	}
}

// repeat64 returns the same byte repeated 64 times — a small helper
// to keep fingerprint literals readable.
func repeat64(b string) string {
	out := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		out = append(out, b[0])
	}
	return string(out)
}
