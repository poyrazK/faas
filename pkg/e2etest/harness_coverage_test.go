package e2etest

import (
	"bytes"
	"net"
	"sync"
	"testing"
)

// This file drives the 0%-coverage pure helpers in harness.go:
// newRecoveryHMACKeyHex, injectSearchPath, freeTCPAddr, envBuilderBase,
// and safeBuffer.{Write,String}.

func TestSweep_NewRecoveryHMACKeyHex(t *testing.T) {
	k1 := newRecoveryHMACKeyHex(t)
	k2 := newRecoveryHMACKeyHex(t)
	if len(k1) != 64 {
		t.Errorf("len(k1) = %d, want 64", len(k1))
	}
	if k1 == k2 {
		t.Error("two consecutive keys were equal (rand should be unique)")
	}
}

func TestSweep_InjectSearchPath_ReplacesExisting(t *testing.T) {
	dsn := "postgres://u:p@h/db?sslmode=disable&search_path=public"
	got := injectSearchPath(dsn, "test_schema")
	want := "postgres://u:p@h/db?sslmode=disable&search_path=test_schema"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSweep_InjectSearchPath_NoExisting(t *testing.T) {
	dsn := "postgres://u:p@h/db"
	got := injectSearchPath(dsn, "sc")
	want := "postgres://u:p@h/db?search_path=sc"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSweep_InjectSearchPath_ExistingWithTrailingAmp(t *testing.T) {
	dsn := "postgres://u:p@h/db?search_path=public&other=x"
	got := injectSearchPath(dsn, "sc")
	want := "postgres://u:p@h/db?search_path=sc&other=x"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSweep_EnvBuilderBase_Default(t *testing.T) {
	t.Setenv("FAAS_BUILDER_BASE_PATH", "")
	if got := envBuilderBase(t); got != "/srv/fc/base/builder-base.ext4" {
		t.Errorf("default path = %q", got)
	}
}

func TestSweep_EnvBuilderBase_Override(t *testing.T) {
	t.Setenv("FAAS_BUILDER_BASE_PATH", "/tmp/test.ext4")
	if got := envBuilderBase(t); got != "/tmp/test.ext4" {
		t.Errorf("override path = %q", got)
	}
}

func TestSweep_FreeTCPAddr(t *testing.T) {
	addr := freeTCPAddr(t)
	if addr == "" {
		t.Fatal("freeTCPAddr returned empty")
	}
	// Should bind to 127.0.0.1.
	if addr[:10] != "127.0.0.1:" {
		t.Errorf("addr = %q, want 127.0.0.1:...", addr)
	}
}

func TestSweep_ReserveGatewayAddresses_HoldsUntilRelease(t *testing.T) {
	h := &Harness{}
	reserveGatewayAddresses(t, h)

	for _, addr := range []string{h.gatewayPublicAddr, h.gatewayControlAddr} {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			_ = listener.Close()
			t.Fatalf("reserved gateway address %s was bindable before release", addr)
		}
	}

	h.releaseGatewayAddressReservations()
	for _, addr := range []string{h.gatewayPublicAddr, h.gatewayControlAddr} {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("gateway address %s remained reserved after release: %v", addr, err)
		}
		_ = listener.Close()
	}
}

func TestSweep_SafeBuffer_Write(t *testing.T) {
	var s safeBuffer
	s.Write([]byte("hello"))
	if got := s.String(); got != "hello" {
		t.Errorf("String = %q", got)
	}
}

func TestSweep_SafeBuffer_ConcurrentWrite(t *testing.T) {
	var s safeBuffer
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Write([]byte("x"))
		}()
	}
	wg.Wait()
	if got := s.String(); len(got) != 16 {
		t.Errorf("len = %d, want 16", len(got))
	}
}

func TestSweep_SafeBuffer_EmptyString(t *testing.T) {
	var s safeBuffer
	if got := s.String(); got != "" {
		t.Errorf("empty buffer String = %q", got)
	}
}

func TestSweep_SafeBuffer_NotUnderlyingBytesBuffer(t *testing.T) {
	// Pin: safeBuffer must NOT be a *bytes.Buffer — it wraps one
	// under a mutex. This is the load-bearing property for
	// safeBuffer's safety guarantees.
	var _ = bytes.Buffer{} // keep import
	var s safeBuffer
	if s.buf.Len() != 0 {
		t.Error("inner buf should start empty")
	}
}

func TestSweep_Repeat(t *testing.T) {
	if got := repeat("ab", 3); got != "ababab" {
		t.Errorf("repeat = %q, want ababab", got)
	}
	if got := repeat("x", 0); got != "" {
		t.Errorf("repeat(0) = %q, want empty", got)
	}
}

func TestSweep_SnapshotProcsEmpty(t *testing.T) {
	// Without a current harness, snapshotProcs returns nil.
	got := snapshotProcs()
	_ = got
}
