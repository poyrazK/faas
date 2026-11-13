package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type subscriptionDeadlineWriter struct {
	header        http.Header
	connection    net.Conn
	deadlines     []time.Time
	flushDeadline time.Time
	flushError    error
}

func (writer *subscriptionDeadlineWriter) Header() http.Header { return writer.header }
func (writer *subscriptionDeadlineWriter) WriteHeader(int)     {}
func (writer *subscriptionDeadlineWriter) Write(body []byte) (int, error) {
	if writer.connection != nil {
		return writer.connection.Write(body)
	}
	return len(body), nil
}
func (writer *subscriptionDeadlineWriter) Flush() {}
func (writer *subscriptionDeadlineWriter) FlushError() error {
	writer.flushDeadline = writer.deadlines[len(writer.deadlines)-1]
	return writer.flushError
}
func (writer *subscriptionDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	writer.deadlines = append(writer.deadlines, deadline)
	if writer.connection != nil {
		return writer.connection.SetWriteDeadline(deadline)
	}
	return nil
}

func TestSubscriptionWritesBoundFlushAndPermitIdleStreams(t *testing.T) {
	for _, test := range []struct {
		name    string
		expires float64
	}{
		{name: "open"},
		{name: "short token", expires: float64(time.Now().Add(time.Second).UnixNano()) / float64(time.Second)},
		{name: "far future token", expires: 1e100},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &subscriptionDeadlineWriter{header: make(http.Header)}
			started := time.Now()
			err := flushSubscription(writer, &authContext{expiresAt: test.expires}, func() error {
				_, err := writer.Write([]byte(": keepalive\n\n"))
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if writer.flushDeadline.Before(started) || writer.flushDeadline.After(time.Now().Add(subscriptionWriteTimeout)) {
				t.Fatalf("flush had an unbounded deadline: %s", writer.flushDeadline)
			}
			if test.name == "short token" && writer.flushDeadline.Sub(started) > time.Second {
				t.Fatal("write may outlive the token")
			}
			if !writer.deadlines[len(writer.deadlines)-1].IsZero() {
				t.Fatal("idle stream retained the last write deadline")
			}
		})
	}
}

func TestSubscriptionFlushFailureIsReturned(t *testing.T) {
	want := errors.New("flush failed")
	writer := &subscriptionDeadlineWriter{header: make(http.Header), flushError: want}
	observed, observation := observeProtocolRequest(writer, httptest.NewRequest(http.MethodPost, "/mcp", nil), rpcRequest{Method: "subscriptions/listen"})
	if err := flushSubscription(observed, nil, func() error { return nil }); !errors.Is(err, want) {
		t.Fatalf("flush error = %v", err)
	}
	if !observation.writeFailed {
		t.Fatal("flush failure was missing from protocol telemetry")
	}
	if writer.deadlines[len(writer.deadlines)-1].IsZero() {
		t.Fatal("failed output lost its deadline before HTTP cleanup")
	}
}

func TestBlockedSubscriptionExpiresAndReleasesCapacity(t *testing.T) {
	auth, err := newAuthenticator(mcpConfig{Endpoint: "/mcp", Auth: json.RawMessage(`{"mode":"open"}`)})
	if err != nil {
		t.Fatal(err)
	}
	connection, unreadPeer := net.Pipe()
	defer connection.Close()
	defer unreadPeer.Close()
	writer := &subscriptionDeadlineWriter{header: make(http.Header), connection: connection}
	identity := &authContext{expiresAt: float64(time.Now().Add(100*time.Millisecond).UnixNano()) / float64(time.Second)}
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("MCP-Protocol-Version", currentProtocol)
	request.Header.Set("Mcp-Method", "subscriptions/listen")
	request.Header.Set("Accept", "text/event-stream")
	observed, _ := observeProtocolRequest(writer, request, rpcRequest{Method: "subscriptions/listen"})
	before := len(subscriptionStreamSlots)
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveTaskSubscription(observed, request, json.RawMessage(`1`), json.RawMessage(`{"notifications":{"resourceSubscriptions":["greeting://welcome"]},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`), currentProtocol, auth, identity, nil)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = connection.Close()
		<-done
		t.Fatal("blocked output outlived the verified token")
	}
	if len(subscriptionStreamSlots) != before {
		t.Fatal("expired stream retained subscription capacity")
	}
}
