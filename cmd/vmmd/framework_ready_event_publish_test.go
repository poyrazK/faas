//go:build linux

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
)

func TestParseFrameworkReadyEventPublish(t *testing.T) {
	body := append([]byte{VsockFrameworkReadyHostTypeEventPublish}, []byte(`{"id":"evt-1","source":"billing","type":"invoice.paid","data":{"amount":42}}`)...)
	msg, err := parseFrameworkReadyDatagram(body)
	if err != nil {
		t.Fatalf("parse event publish: %v", err)
	}
	if msg.Kind != parseFWReadyKindEventPublish || string(msg.EventPublish) != string(body[1:]) {
		t.Fatalf("parsed message = %#v", msg)
	}
}

func TestFrameworkReadyDispatchesEventPublish(t *testing.T) {
	receiver := &FrameworkReadyReceiver{
		ctx: context.Background(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		eventPublisher: func(_ context.Context, instance string, body []byte) error {
			if instance != "instance-1" || string(body) != `{"id":"evt-1"}` {
				t.Fatalf("publisher args = %q %q", instance, body)
			}
			return nil
		},
	}
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() {
		kind, err := receiver.handleGuestStream("instance-1", server)
		if kind != "" && err == nil {
			err = io.ErrUnexpectedEOF
		}
		done <- err
	}()
	_, _ = client.Write(append([]byte{VsockFrameworkReadyHostTypeEventPublish}, []byte(`{"id":"evt-1"}`)...))
	_ = client.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
