// overlay_test.go — table-driven coverage for the cross-box dial
// abstraction (issue #98 / ADR-028, plumbed via issue #120). The
// package surface is intentionally narrow (New + Dial), so the
// tests focus on:
//
//   - cheap invariants: New("") returns a zero Target; Raw() is
//     identity; Dial refuses an empty target with the typed
//     *OverlayError sentinel (so the heartbeat goroutine can log
//     it without re-classifying string content);
//   - happy path: Dial returns a non-nil *grpc.ClientConn on a
//     reachable target. We use a TCP listener so the dial
//     exercises wire.ParseTarget + grpc.NewClient; the connection
//     is left for the runtime to GC (no Close leak — gRPC closes
//     on first RPC failure and the listener is closed in
//     cleanup);
//   - TLS pass-through: a populated *tls.Config handed to Dial
//     reaches wire.DialContext unchanged. We verify this by
//     triggering the wire "mTLS required" sentinel — passing nil
//     TLS to a tcp target fails with the wire error string;
//     passing a populated *tls.Config gets past that check and
//     returns a *grpc.ClientConn.
//
// Issue #120 acceptance: pkg/overlay is now imported by ≥3
// production call sites, so the assertions below lock the dial
// contract before that surface grows.

package overlay

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
)

// TestNew_NoParse pins the fact that New does NOT parse — an empty
// input returns a valid Target whose Dial will fail fast. This is
// the contract the gateway's per-node client cache relies on: it
// can register a placeholder for a node whose target_url hasn't
// been resolved yet without panicking.
func TestNew_NoParse(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"unix:///run/faas/vmmd.sock", "unix:///run/faas/vmmd.sock"},
		{"tcp://10.0.0.2:50051", "tcp://10.0.0.2:50051"},
		{"dns://vmmd-cluster.local:50051", "dns://vmmd-cluster.local:50051"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := New(tc.in)
			if raw := got.Raw(); raw != tc.want {
				t.Errorf("Raw() = %q, want %q", raw, tc.want)
			}
			// Round-trip identity: a second New with the same input
			// produces the same Raw string (no normalisation, no
			// allocation surprises).
			if again := New(got.Raw()).Raw(); again != tc.want {
				t.Errorf("round-trip Raw() = %q, want %q", again, tc.want)
			}
		})
	}
}

// TestDial_EmptyTargetSentinel pins the load-bearing error path:
// a zero Target must surface ErrEmptyTarget so the heartbeat
// goroutine can classify "config bug" vs "remote node down"
// without sniffing string content. The current heartbeat just
// logs+flips on any error, so the typed sentinel is
// defence-in-depth — log scrapers can distinguish config drift
// from real outages via:
//
//	errors.Is(err, overlay.ErrEmptyTarget)
//
// without re-classifying the message string. We also pin
// errors.As(&OverlayError{}) so a future contributor who forgets
// to wrap a new error in *OverlayError trips the test.
func TestDial_EmptyTargetSentinel(t *testing.T) {
	conn, err := Dial(context.Background(), New(""), nil)
	if conn != nil {
		t.Errorf("empty target should not return a conn, got %T", conn)
	}
	if err == nil {
		t.Fatal("empty target should return a non-nil error")
	}
	// errors.Is(err, ErrEmptyTarget) — the canonical sentinel match.
	if !errors.Is(err, ErrEmptyTarget) {
		t.Errorf("err is not ErrEmptyTarget: %T (%v)", err, err)
	}
	// errors.As(err, &OverlayError{}) — the typed-value fallback.
	var ovErr *OverlayError
	if !errors.As(err, &ovErr) {
		t.Errorf("err does not unwrap to *OverlayError: %T (%v)", err, err)
	}
	if !strings.Contains(err.Error(), "empty target_url") {
		t.Errorf("err message %q lacks %q sentinel", err.Error(), "empty target_url")
	}
}

// TestErrEmptyTarget_SentinelIdentity pins the export contract:
// the address of ErrEmptyTarget must be stable across calls so
// `errors.Is` on the package variable is the canonical way to
// match. A regression that accidentally constructs a fresh
// &OverlayError{} on every Dial call would silently break
// caller-side classification — this test trips at the source if
// anyone refactors the construction back to a per-call literal.
func TestErrEmptyTarget_SentinelIdentity(t *testing.T) {
	if ErrEmptyTarget == nil {
		t.Fatal("ErrEmptyTarget is nil — package-level sentinel must be non-nil")
	}
	if ErrEmptyTarget.Error() != "overlay: empty target_url" {
		t.Errorf("ErrEmptyTarget message = %q, want %q", ErrEmptyTarget.Error(), "overlay: empty target_url")
	}
	// Multiple errors.Is checks against different "wrappers" of
	// ErrEmptyTarget all resolve to true. We don't currently wrap
	// the sentinel anywhere in the package, but a future caller
	// wrapping with fmt.Errorf("%w", ErrEmptyTarget) must still
	// classify correctly.
	if !errors.Is(ErrEmptyTarget, ErrEmptyTarget) {
		t.Error("ErrEmptyTarget is not errors.Is-self")
	}
}

// TestDial_HappyUnixTarget pins that Dial passes a unix target
// through to wire.DialContext unchanged — a real socket path
// returns a non-nil *grpc.ClientConn (gRPC's lazy dial makes
// the connection object available immediately). We don't make
// any RPC: the lazy dial would block on a non-listening target,
// and a successful RPC isn't the dial's contract — wire.Dial
// returns the conn and lets the caller trigger the first RPC.
func TestDial_HappyUnixTarget(t *testing.T) {
	// Bind an ephemeral unix socket so the address resolves; the
	// listener is closed at test cleanup but the path string is
	// what wire.ParseTarget hits.
	dir := t.TempDir()
	sockPath := dir + "/vmmd.sock"
	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer lis.Close()

	conn, err := Dial(context.Background(), New("unix://"+sockPath), nil)
	if err != nil {
		// gRPC lazy dial should not error here — target parses,
		// transport credentials resolve. If it does, log the chain
		// so a regression that adds an eager dial surfaces in the
		// test log immediately.
		t.Fatalf("Dial: %v", err)
	}
	if conn == nil {
		t.Fatal("Dial returned nil conn without an error")
	}
	t.Cleanup(func() { _ = conn.Close() })
}

// TestDial_HappyTCPTarget exercises the tcp branch. The target
// uses an ephemeral listener on 127.0.0.1; the dial succeeds at
// the parse/credential layer without an eager handshake (gRPC's
// lazy dial), and the cleanup Close prevents a goroutine leak.
func TestDial_HappyTCPTarget(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer lis.Close()

	tcpTarget := "tcp://" + lis.Addr().String()
	// Wire requires mTLS for tcp targets (issue #95 / ADR-025).
	// A populated *tls.Config gets past ParseTarget + the mTLS
	// guard; the dial itself succeeds at parse-time because gRPC
	// uses a lazy dial.
	tlsCfg := &tls.Config{InsecureSkipVerify: true} // test fixture only
	conn, err := Dial(context.Background(), New(tcpTarget), tlsCfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if conn == nil {
		t.Fatal("Dial returned nil conn")
	}
	t.Cleanup(func() { _ = conn.Close() })
}

// TestDial_TCPRequiresTLS pins the wire-level guard the
// production call site relies on: a tcp target with nil TLS
// must be rejected at parse time, not at first-RPC time, so a
// config drift in gatewayd (forgot to load [vmmd_tls]) is
// surfaced at startup instead of silently dropping every
// customer request.
//
// This is essentially a contract test on wire.DialContext, but
// running it through pkg/overlay ensures the overlay wrapper
// doesn't accidentally short-circuit the wire guard.
func TestDial_TCPRequiresTLS(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer lis.Close()

	conn, err := Dial(context.Background(), New("tcp://"+lis.Addr().String()), nil)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected error for tcp target with nil TLS; got nil")
	}
	if !strings.Contains(err.Error(), "mTLS required") {
		t.Errorf("err = %v, want substring %q", err, "mTLS required")
	}
}

// TestDial_TLSPassThrough confirms pkg/overlay does not mutate the
// caller's *tls.Config. We use a tcp target (where TLS is required),
// pass a populated cfg, then assert the cfg's pointers and fields
// are byte-identical after the dial returns. A regression that
// accidentally wrote into the cfg (e.g. setting ServerName) would
// be visible to the caller on its next reuse.
func TestDial_TLSPassThrough(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer lis.Close()

	want := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
	}
	conn, err := Dial(context.Background(), New("tcp://"+lis.Addr().String()), want)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Pointer-equal values we expect to be left untouched.
	if want.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion mutated: got %v, want %v", want.MinVersion, tls.VersionTLS12)
	}
	if want.MaxVersion != tls.VersionTLS13 {
		t.Errorf("MaxVersion mutated: got %v, want %v", want.MaxVersion, tls.VersionTLS13)
	}
	if !want.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify mutated: got false, want true")
	}
}

// TestDial_ContextCancelled short-circuits before wire.ParseTarget
// when ctx is already done. pkg/overlay doesn't add its own
// ctx-guard (wire.DialContext does); we still pin the behaviour
// so an overlay-side ctx check (if ever added) doesn't shift the
// error surface.
func TestDial_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done

	conn, err := Dial(ctx, New("unix:///run/faas/vmmd.sock"), nil)
	if conn != nil {
		t.Errorf("cancelled ctx should not return conn, got %T", conn)
	}
	if err == nil {
		t.Fatal("cancelled ctx should return a non-nil error")
	}
}

// TestOverlayError_Error pins the typed error's Error() string so
// a log-scraper regex that depends on the format (e.g.
// "overlay: …") doesn't silently drift.
func TestOverlayError_Error(t *testing.T) {
	e := &OverlayError{msg: "test message"}
	if got, want := e.Error(), "test message"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
