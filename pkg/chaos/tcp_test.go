package chaos

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.(*net.TCPListener).AcceptTCP()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	return client, server
}

func installTCPRule(t *testing.T, conn *TCPConn, rule Rule, expires time.Time) {
	t.Helper()
	rule.To, rule.Percent, rule.Port = "cache", 100, 6379
	if err := conn.Update(Lease{Rules: []Rule{rule}, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
}

func TestTCPFaultsDelayAndThrottleBothDirections(t *testing.T) {
	for _, direction := range []string{DirectionUpstream, DirectionDownstream} {
		for _, kind := range []string{KindTCPLatency, KindTCPBandwidth} {
			t.Run(kind+"/"+direction, func(t *testing.T) {
				client, server := tcpPair(t)
				conn := NewTCPConn(context.Background(), server, 6379, 1, nil)
				defer conn.Close()
				rule := Rule{Kind: kind, Direction: direction}
				want := 80 * time.Millisecond
				if kind == KindTCPLatency {
					rule.LatencyMS = 80
				} else {
					rule.RateKiBPerSecond = 1
					want = 250 * time.Millisecond
				}
				installTCPRule(t, conn, rule, time.Now().Add(3*time.Second))
				payload := make([]byte, 256)
				for i := range payload {
					payload[i] = byte(i)
				}
				received := make([]byte, 256)
				done := make(chan error, 1)
				start := time.Now()
				if direction == DirectionDownstream {
					go func() { _, err := conn.Write(payload); done <- err }()
					_, err := io.ReadFull(client, received)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					go func() { _, err := client.Write(payload); done <- err }()
					_, err := io.ReadFull(conn, received)
					if err != nil {
						t.Fatal(err)
					}
				}
				if !bytes.Equal(payload, received) {
					t.Fatal("fault changed binary payload")
				}
				if elapsed := time.Since(start); elapsed < want*3/4 || elapsed > 2*time.Second {
					t.Fatalf("elapsed %v, want at least %v", elapsed, want*3/4)
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestTCPTimeoutReleasesOnReplacementAndExpiry(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "expiry", true: "replacement"}[replace], func(t *testing.T) {
			client, server := tcpPair(t)
			injected := make(chan struct{}, 1)
			conn := NewTCPConn(context.Background(), server, 6379, 1, func(Rule, string) { injected <- struct{}{} })
			defer conn.Close()
			installTCPRule(t, conn, Rule{Kind: KindTCPTimeout, Direction: DirectionDownstream}, time.Now().Add(180*time.Millisecond))
			done := make(chan error, 1)
			go func() { _, err := conn.Write([]byte("ok")); done <- err }()
			select {
			case <-injected:
			case <-time.After(time.Second):
				t.Fatal("timeout did not activate")
			}
			if replace {
				if err := conn.Update(Lease{}); err != nil {
					t.Fatal(err)
				}
			}
			data := make([]byte, 2)
			if _, err := io.ReadFull(client, data); err != nil {
				t.Fatal(err)
			}
			if string(data) != "ok" {
				t.Fatalf("payload changed: %q", data)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTCPResetIsARealClientReset(t *testing.T) {
	client, server := tcpPair(t)
	conn := NewTCPConn(context.Background(), server, 6379, 1, nil)
	defer conn.Close()
	installTCPRule(t, conn, Rule{Kind: KindTCPReset, ResetAfterMS: 50}, time.Now().Add(time.Second))
	_, err := client.Read(make([]byte, 1))
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("read = %v, want TCP reset", err)
	}
	if _, err := conn.Write([]byte("x")); !errors.Is(err, ErrTCPReset) {
		t.Fatalf("write = %v, want intentional reset", err)
	}
}

func TestTCPResetDoesNotOutliveItsLease(t *testing.T) {
	client, server := tcpPair(t)
	conn := NewTCPConn(context.Background(), server, 6379, 1, nil)
	defer conn.Close()
	installTCPRule(t, conn, Rule{Kind: KindTCPReset, ResetAfterMS: 200}, time.Now().Add(40*time.Millisecond))
	done := make(chan error, 1)
	go func() { time.Sleep(100 * time.Millisecond); _, err := conn.Write([]byte("x")); done <- err }()
	if _, err := io.ReadFull(client, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTCPBlockedTrafficCancelsWithoutLeaking(t *testing.T) {
	_, server := tcpPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	conn := NewTCPConn(ctx, server, 6379, 1, nil)
	defer conn.Close()
	installTCPRule(t, conn, Rule{Kind: KindTCPTimeout}, time.Now().Add(time.Minute))
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("x")); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("write leaked after cancellation")
	}
}

func TestTCPRuleValidationRejectsAmbiguousParameters(t *testing.T) {
	for _, rule := range []Rule{
		{Kind: KindTCPBandwidth}, {Kind: KindTCPBandwidth, RateKiBPerSecond: -1},
		{Kind: KindTCPReset, Direction: DirectionUpstream}, {Kind: KindTCPTimeout, LatencyMS: 1},
		{Kind: KindTCPLatency, LatencyMS: 30_001}, {Kind: KindHTTPStatus, StatusCode: 503},
	} {
		rule.To, rule.Port, rule.Percent = "cache", 6379, 100
		if err := ValidateRule(rule); err == nil {
			t.Fatalf("invalid rule accepted: %+v", rule)
		}
	}
}

func TestTCPReplacingResetCancelsOldTimer(t *testing.T) {
	client, server := tcpPair(t)
	conn := NewTCPConn(context.Background(), server, 6379, 1, nil)
	defer conn.Close()
	installTCPRule(t, conn, Rule{Kind: KindTCPReset, ResetAfterMS: 80}, time.Now().Add(time.Second))
	if err := conn.Update(Lease{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("x")); done <- err }()
	if _, err := io.ReadFull(client, make([]byte, 1)); err != nil {
		t.Fatalf("removed reset still fired: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
