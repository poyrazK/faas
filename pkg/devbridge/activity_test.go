package devbridge

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 379
func TestActivityReplacementFencesOldDisconnectAndRevocation(t *testing.T) {
	session := Session{ID: "session", ExpiresAt: time.Now().Add(time.Hour)}
	observer := NewObserver(2, 2, 1024)
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 101, Body: &activityTestSocket{}}, nil
	})
	first, err := observer.ConnectionTransport(session, base).RoundTrip(httptest.NewRequest("GET", "/connect", nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := observer.ConnectionTransport(session, base).RoundTrip(httptest.NewRequest("GET", "/connect", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Body.Close()
	if got := observer.Snapshot(session); got.ConnectionState != "connected" || got.ConnectedAt == nil {
		t.Fatalf("old cleanup changed replacement: %+v", got)
	}
	_ = second.Body.Close()
	if got := observer.Snapshot(session); got.ConnectionState != "disconnected" || got.DisconnectedAt == nil {
		t.Fatalf("disconnect not observed: %+v", got)
	}
	now := time.Now()
	session.RevokedAt = &now
	if observer.Snapshot(session).ConnectionState != "revoked" {
		t.Fatal("revocation not reflected")
	}
}

// adr: 379
func TestActivityWindowIsBoundedAndExcludesQuerySecrets(t *testing.T) {
	observer := NewObserver(1, 2, 1024)
	session := Session{ID: "first", ExpiresAt: time.Now().Add(time.Hour)}
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString("ok"))}, nil
	})
	for range 3 {
		response, err := observer.RequestTransport(session, base).RoundTrip(httptest.NewRequest("GET", "/charge?token=secret", nil))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
	}
	activity := observer.Snapshot(session)
	if len(activity.Requests) != 2 {
		t.Fatalf("unbounded window: %d", len(activity.Requests))
	}
	for _, record := range activity.Requests {
		if record.Path != "/charge" || !record.Complete || record.ResponseBytes != 2 {
			t.Fatalf("invalid record: %+v", record)
		}
	}
	observer.RequestTransport(Session{ID: "second", ExpiresAt: time.Now().Add(time.Hour)}, base)
	if len(observer.Snapshot(session).Requests) != 0 {
		t.Fatal("observer cache was not bounded")
	}
}

type activityTestSocket struct{ bytes.Buffer }

func (*activityTestSocket) Close() error { return nil }
