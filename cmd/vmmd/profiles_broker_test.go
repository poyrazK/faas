package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type brokerHarness struct {
	b          *profileBroker
	mu         sync.Mutex
	resolves   int
	generation string
	sent       int
}

func newBrokerHarness() *brokerHarness {
	h := &brokerHarness{generation: "g1"}
	h.b = newProfileBroker(prometheus.NewRegistry())
	h.b.identity = func(string) (string, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.generation, nil
	}
	h.b.resolve = func(context.Context, string) (profiling.Principal, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.resolves++
		return profiling.Principal{AppID: "app"}, nil
	}
	h.b.send = func(context.Context, profiling.Envelope) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.sent++
		return nil
	}
	return h
}

func profileFrame(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(profiling.Upload{Profile: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	return append(frame, body...)
}

// upload runs one complete guest upload and returns the broker's ack.
func (h *brokerHarness) upload(t *testing.T, instance string) (byte, string) {
	t.Helper()
	guest, host := net.Pipe()
	defer func() { _ = guest.Close() }()
	go func() { _, _ = guest.Write(profileFrame(t)) }()
	ack, result, _ := h.b.handle(context.Background(), instance, host)
	_ = host.Close()
	return ack, result
}

// stall opens a connection from instance that never sends its frame.
func (h *brokerHarness) stall(t *testing.T, instance string) (done chan string, stop func()) {
	t.Helper()
	guest, host := net.Pipe()
	done = make(chan string, 1)
	go func() {
		_, result, _ := h.b.handle(context.Background(), instance, host)
		done <- result
	}()
	return done, func() { _ = guest.Close(); _ = host.Close() }
}

// A guest that opens connections and never finishes its frames holds only
// its own instance's connection, never the node's upload slots.
func TestProfileBrokerSlowGuestsCannotStarveTheNode(t *testing.T) {
	h := newBrokerHarness()
	var stops []func()
	var dones []chan string
	for i := range api.ProfileMaxConcurrentUploads + 2 {
		done, stop := h.stall(t, "slow-"+string(rune('a'+i)))
		dones, stops = append(dones, done), append(stops, stop)
	}
	time.Sleep(50 * time.Millisecond)
	if ack, result := h.upload(t, "neighbour"); ack != 0 || result != "accepted" {
		t.Fatalf("a neighbour's upload = %d %q while slow guests read", ack, result)
	}
	done, stop := h.stall(t, "slow-a")
	defer stop()
	if got := <-done; got != "limited" {
		t.Fatalf("a second connection from a stalled instance = %q, want limited", got)
	}
	for i, s := range stops {
		s()
		if got := <-dones[i]; got != "read" {
			t.Fatalf("stalled connection ended %q, want read", got)
		}
	}
	if got := testutil.ToFloat64(h.b.uploads.WithLabelValues("limited")); got != 1 {
		t.Fatalf("limited uploads metric = %v", got)
	}
}

func TestProfileBrokerLimitsUploadsPerInstanceMinute(t *testing.T) {
	h := newBrokerHarness()
	now := time.Unix(1_800_000_000, 0)
	h.b.clock = func() time.Time { return now }
	for range api.ProfileBrokerUploadsPerInstanceMinute {
		if ack, result := h.upload(t, "busy"); ack != 0 {
			t.Fatalf("upload within the minute = %q", result)
		}
	}
	if ack, result := h.upload(t, "busy"); ack == 0 || result != "limited" {
		t.Fatalf("upload over the minute = %d %q", ack, result)
	}
	if ack, _ := h.upload(t, "other"); ack != 0 {
		t.Fatal("another instance was limited by a neighbour's minute")
	}
	now = now.Add(time.Minute)
	if ack, _ := h.upload(t, "busy"); ack != 0 {
		t.Fatal("instance still limited in the next minute")
	}
}

// Database identity is reused within a generation and TTL, refusals too.
func TestProfileBrokerReusesIdentityWithinAGeneration(t *testing.T) {
	h := newBrokerHarness()
	now := time.Unix(1_800_000_000, 0)
	h.b.clock = func() time.Time { return now }
	for range 5 {
		h.upload(t, "vm")
	}
	if h.resolves != 1 || h.sent != 5 {
		t.Fatalf("resolves = %d, sent = %d; want 1 and 5", h.resolves, h.sent)
	}
	h.mu.Lock()
	h.generation = "g2"
	h.mu.Unlock()
	h.upload(t, "vm")
	now = now.Add(api.ProfileBrokerPrincipalTTL)
	h.upload(t, "vm")
	if h.resolves != 3 {
		t.Fatalf("resolves = %d after a restore and the TTL, want 3", h.resolves)
	}
	refusals := 0
	h.b.resolve = func(context.Context, string) (profiling.Principal, error) {
		refusals++
		return profiling.Principal{}, errors.New("profiling disabled for instance")
	}
	for range 3 {
		if ack, result := h.upload(t, "disabled"); ack == 0 || result != "identity" {
			t.Fatalf("disabled instance upload = %d %q", ack, result)
		}
	}
	if refusals != 1 {
		t.Fatalf("refusals resolved %d times, want 1", refusals)
	}
}
