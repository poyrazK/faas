//go:build linux

package main

// adr: 20261031102000

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func verdictTestReceiver(publish GuestEventPublisher) *FrameworkReadyReceiver {
	return &FrameworkReadyReceiver{
		ctx: context.Background(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		mgr:            fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("instance-1", "deployment-1", "app", "account"),
		eventPublisher: publish,
	}
}

// exchangeVerdict sends one type=0x0B publish without closing its side and
// reads vmmd's length-prefixed verdict.
func exchangeVerdict(t *testing.T, receiver *FrameworkReadyReceiver, body string) api.EventPublishVerdict {
	t.Helper()
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := receiver.handleGuestStream("instance-1", server)
		_ = server.Close()
		done <- err
	}()
	frame := make([]byte, 5+len(body))
	frame[0] = VsockFrameworkReadyHostTypeEventPublishVerdict
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write(frame); err != nil {
		t.Fatal(err)
	}
	var size [2]byte
	if _, err := io.ReadFull(client, size[:]); err != nil {
		t.Fatalf("read verdict length: %v", err)
	}
	reply := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read verdict: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("handleGuestStream: %v", err)
	}
	var verdict api.EventPublishVerdict
	if err := json.Unmarshal(reply, &verdict); err != nil {
		t.Fatal(err)
	}
	return verdict
}

// A publish that waits for a verdict gets vmmd's answer back without either
// side closing first, and a refused event is no longer acknowledged.
func TestFrameworkReadyEventPublishAnswersWithVerdict(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want api.EventPublishVerdict
	}{
		{"accepted", nil, api.EventPublishVerdict{Status: http.StatusAccepted}},
		{"paused", errGuestEventsPaused, api.EventPublishVerdict{Status: http.StatusServiceUnavailable, Code: api.CodeEventsPaused, RetryAfterSeconds: guestEventsPausedRetrySeconds}},
		{"rate limited", guestEventRateLimitedError{perSecond: 2, wait: 1500 * time.Millisecond}, api.EventPublishVerdict{Status: http.StatusTooManyRequests, Code: api.CodeEventPublishRateLimited, RetryAfterSeconds: 2}},
		{"storage down", errors.New("persist in-guest event publish: connection refused"), api.EventPublishVerdict{Status: http.StatusServiceUnavailable, Code: "event_publish_unavailable", RetryAfterSeconds: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []byte
			receiver := verdictTestReceiver(func(_ context.Context, instance string, body []byte) error {
				got = append([]byte(nil), body...)
				return tc.err
			})
			verdict := exchangeVerdict(t, receiver, `{"id":"evt-1"}`)
			if verdict != tc.want {
				t.Fatalf("verdict = %+v, want %+v", verdict, tc.want)
			}
			if string(got) != `{"id":"evt-1"}` {
				t.Fatalf("publisher body = %q", got)
			}
		})
	}
}

// A body the guest cannot have sent is refused as a protocol error before the
// publisher runs.
func TestFrameworkReadyEventPublishVerdictRejectsOversizedBody(t *testing.T) {
	receiver := verdictTestReceiver(func(context.Context, string, []byte) error {
		t.Fatal("publisher called")
		return nil
	})
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	done := make(chan string, 1)
	go func() {
		kind, _ := receiver.handleGuestStream("instance-1", server)
		_ = server.Close()
		done <- kind
	}()
	frame := []byte{VsockFrameworkReadyHostTypeEventPublishVerdict, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(frame[1:], uint32(VsockEventPublishMaxBody)+1)
	_, _ = client.Write(frame)
	if kind := <-done; kind != "protocol" {
		t.Fatalf("failure kind = %q, want protocol", kind)
	}
}

func TestGuestEventPublishVerdictMapsValidation(t *testing.T) {
	var syntax json.SyntaxError
	if got := guestEventPublishVerdict(&syntax); got.Status != http.StatusBadRequest || got.Code != "invalid_event" {
		t.Fatalf("malformed JSON verdict = %+v", got)
	}
}
