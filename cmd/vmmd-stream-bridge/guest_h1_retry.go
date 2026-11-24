package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"syscall"
)

// roundTripGuestH1 sends req once and retries it on a fresh connection when
// a bodyless request failed on a pooled connection the guest had already
// closed. net/http replays such a request only for idempotent methods, so a
// bodyless POST or DELETE that raced the guest's keep-alive close reached the
// client as 502. A request with a body is never replayed: the inbound body
// stream is already consumed.
func roundTripGuestH1(transport http.RoundTripper, req *http.Request) (*http.Response, error) {
	reused := false
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
	first := req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := transport.RoundTrip(first)
	if err == nil || !reused || !guestH1Replayable(req) || !staleGuestConnError(err) || req.Context().Err() != nil {
		return resp, err
	}
	if t, ok := transport.(*http.Transport); ok {
		// The other idle connections are as old as the one that failed.
		t.CloseIdleConnections()
	}
	return transport.RoundTrip(req)
}

func guestH1Replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody
}

// staleGuestConnError matches the errors a keep-alive connection closed by
// the guest produces on the next write or read.
func staleGuestConnError(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}
