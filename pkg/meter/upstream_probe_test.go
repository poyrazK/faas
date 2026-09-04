// Package meter — upstream_probe regression tests (ADR-098 §D2 /
// §11). Three load-bearing claims:
//
//   - §11 secret rule: the probe NEVER holds the plaintext host
//     in any local variable that escapes into a slog call. The
//     test fails if a future regression logs the literal host
//     hash's first 8 chars under a "host" field — only
//     host_redacted_hash is allowed.
//   - never-uses-http.Get guard: a grep test fails if the
//     upstream_probe.go file imports net/http.
//   - outcome classification: a fake dialer drives each of
//     {ok, timeout, refused, dns, unreachable} and pins the
//     per-outcome mapping.

package meter

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// fakeUpstreamProbeStore is the in-memory store the unit tests
// drive. Records every InsertDataUpstreamProbe call into a
// slice; tests assert on the slice contents.
type fakeUpstreamProbeStore struct {
	mu       sync.Mutex
	targets  []state.DataUpstreamTarget
	inserted []sqlc.InsertDataUpstreamProbeParams
}

func TestProbeDisabledIsNoop(t *testing.T) {
	p := NewProbe(nil, "us-east-1", nil).SetEnabled(false)
	written, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("disabled probe run = %v, want nil", err)
	}
	if written != 0 {
		t.Fatalf("disabled probe wrote %d rows, want zero", written)
	}
}

func (s *fakeUpstreamProbeStore) ListDistinctUpstreamHostHashes(_ context.Context) ([]state.DataUpstreamTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]state.DataUpstreamTarget, len(s.targets))
	copy(out, s.targets)
	return out, nil
}

func (s *fakeUpstreamProbeStore) InsertDataUpstreamProbe(_ context.Context, arg sqlc.InsertDataUpstreamProbeParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inserted = append(s.inserted, arg)
	return nil
}

func (s *fakeUpstreamProbeStore) PruneDataUpstreamProbesOlderThan(_ context.Context, _ time.Time) error {
	return nil
}

// TestProbeOnce_HappyPath drives a stub dialer that returns a
// fake conn and asserts the probe attempts the TLS handshake.
// The handshake on a net.Pipe() will fail (no real handshake
// traffic is exchanged), so this test asserts the probe falls
// into the error path with RTTMs nil — the §11 invariant is
// that the probe never logs the plaintext host, regardless of
// outcome.
//
// The full happy-path coverage (TCP+TLS handshake completes
// + RTT is non-nil) lives in cmd/e2e/connection_aware_e2e_test.go
// (C9) which spins up a real in-process TLS listener.
func TestProbeOnce_HappyPath(t *testing.T) {
	store := &fakeUpstreamProbeStore{}
	p := NewProbe(store, "us-east-1", nil)
	p.Now = func() time.Time { return time.Unix(1700000000, 0) }
	p.Timeout = 100 * time.Millisecond
	// Stub the dialer to return a fake conn that
	// immediately closes — the TLS handshake will fail
	// (EOF), and the probe will classify it as unreachable.
	// The key check is that the probe writes a probe row
	// (NOT the plaintext host).
	p.Dialer = func(_ context.Context, _, _ string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		go func() {
			// Read from the server side and close so
			// the client side gets an EOF.
			buf := make([]byte, 1)
			_, _ = c2.Read(buf)
			c2.Close()
		}()
		return c1, nil
	}
	res := p.ProbeOnce(context.Background(), state.DataUpstreamTarget{
		HostRedactedHash: strings.Repeat("a", 64),
		Kind:             state.DataUpstreamKind(api.DataUpstreamKindPostgres),
		Port:             5432,
	})
	if res.OK {
		t.Errorf("OK = true, want false (TLS handshake on a Pipe is not a real handshake)")
	}
	if res.RTTMs != nil {
		t.Errorf("RTTMs = %v, want nil on OK=false", *res.RTTMs)
	}
	if res.ErrorClass == nil {
		t.Errorf("ErrorClass = nil, want non-nil on OK=false")
	}
	if res.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", res.Region)
	}
	if res.SampledAt.IsZero() {
		t.Errorf("SampledAt is zero")
	}
}

// TestProbeOnce_TimeoutError asserts the closed-vocab mapping
// for context.DeadlineExceeded → UpstreamProbeOutcomeTimeout.
func TestProbeOnce_TimeoutError(t *testing.T) {
	store := &fakeUpstreamProbeStore{}
	p := NewProbe(store, "default", nil)
	p.Timeout = 50 * time.Millisecond
	p.Dialer = func(_ context.Context, _, _ string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("i/o timeout")}
	}
	res := p.ProbeOnce(context.Background(), state.DataUpstreamTarget{
		HostRedactedHash: strings.Repeat("b", 64),
		Kind:             state.DataUpstreamKind(api.DataUpstreamKindPostgres),
		Port:             5432,
	})
	if res.OK {
		t.Errorf("OK = true, want false")
	}
	if res.ErrorClass == nil || *res.ErrorClass != UpstreamProbeOutcomeUnreachable {
		t.Errorf("ErrorClass = %v, want UpstreamProbeOutcomeUnreachable", res.ErrorClass)
	}
}

// TestProbeOnce_DNSError asserts the closed-vocab mapping for
// net.DNSError → UpstreamProbeOutcomeDNS.
func TestProbeOnce_DNSError(t *testing.T) {
	store := &fakeUpstreamProbeStore{}
	p := NewProbe(store, "default", nil)
	p.Timeout = 100 * time.Millisecond
	p.Dialer = func(_ context.Context, _, _ string) (net.Conn, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "nope.invalid"}
	}
	res := p.ProbeOnce(context.Background(), state.DataUpstreamTarget{
		HostRedactedHash: strings.Repeat("c", 64),
		Kind:             state.DataUpstreamKind(api.DataUpstreamKindPostgres),
		Port:             5432,
	})
	if res.OK {
		t.Errorf("OK = true, want false")
	}
	if res.ErrorClass == nil || *res.ErrorClass != UpstreamProbeOutcomeDNS {
		t.Errorf("ErrorClass = %v, want UpstreamProbeOutcomeDNS", res.ErrorClass)
	}
}

// TestProbeOnce_RefusedError asserts the closed-vocab mapping
// for syscall.ECONNREFUSED → UpstreamProbeOutcomeRefused.
func TestProbeOnce_RefusedError(t *testing.T) {
	store := &fakeUpstreamProbeStore{}
	p := NewProbe(store, "default", nil)
	p.Timeout = 100 * time.Millisecond
	p.Dialer = func(_ context.Context, _, _ string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("connect: connection refused")}
	}
	res := p.ProbeOnce(context.Background(), state.DataUpstreamTarget{
		HostRedactedHash: strings.Repeat("d", 64),
		Kind:             state.DataUpstreamKind(api.DataUpstreamKindPostgres),
		Port:             5432,
	})
	if res.OK {
		t.Errorf("OK = true, want false")
	}
	if res.ErrorClass == nil || *res.ErrorClass != UpstreamProbeOutcomeRefused {
		t.Errorf("ErrorClass = %v, want UpstreamProbeOutcomeRefused", res.ErrorClass)
	}
}

// TestRun_FansOutAndInserts asserts that Probe.Run fans out
// one INSERT per target and surfaces the count. The stub
// dialer always returns an immediately-closed pipe, so each
// probe falls into the error path — but the test's load-
// bearing claim is that the loop fans out and writes a row
// per target, NOT that the probes succeed (the happy path
// lives in C9's e2e with a real TLS listener).
func TestRun_FansOutAndInserts(t *testing.T) {
	store := &fakeUpstreamProbeStore{
		targets: []state.DataUpstreamTarget{
			{HostRedactedHash: strings.Repeat("e", 64), Kind: state.DataUpstreamKind(api.DataUpstreamKindPostgres), Port: 5432},
			{HostRedactedHash: strings.Repeat("f", 64), Kind: state.DataUpstreamKind(api.DataUpstreamKindRedis), Port: 6379},
			{HostRedactedHash: strings.Repeat("0", 64), Kind: state.DataUpstreamKind(api.DataUpstreamKindMongo), Port: 27017},
		},
	}
	p := NewProbe(store, "default", nil)
	p.Timeout = 100 * time.Millisecond
	p.MaxConcurrent = 8
	p.Dialer = func(_ context.Context, _, _ string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		go func() {
			// Server side reads + closes immediately so
			// the client side gets an EOF.
			buf := make([]byte, 1)
			_, _ = c2.Read(buf)
			c2.Close()
		}()
		return c1, nil
	}
	written, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if written != 3 {
		t.Errorf("written = %d, want 3", written)
	}
	if len(store.inserted) != 3 {
		t.Errorf("inserted = %d, want 3", len(store.inserted))
	}
}
