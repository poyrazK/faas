// adr: 1027 — a pooled server's reader runs while the server is idle, so a
// server whose database stopped is marked ended and never lent.
package pgproxy

import (
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

// pipeConn is a server connection whose far side the test controls.
type pipeConn struct {
	*io.PipeReader
	io.Writer
}

func TestServerReaderMarksAnIdleServerEnded(t *testing.T) {
	encode := func(t *testing.T, msg pgproto3.BackendMessage) []byte {
		t.Helper()
		buf, err := msg.Encode(nil)
		if err != nil {
			t.Fatal(err)
		}
		return buf
	}
	cases := []struct {
		name string
		end  func(t *testing.T, w *io.PipeWriter)
	}{
		{"connection closed", func(t *testing.T, w *io.PipeWriter) { _ = w.Close() }},
		{"FATAL from the backend", func(t *testing.T, w *io.PipeWriter) {
			_, _ = w.Write(encode(t, &pgproto3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, w := io.Pipe()
			srv := newServerConn(pipeConn{PipeReader: r, Writer: io.Discard}, backendKey{})
			srv.events = make(chan serverEvent, 64)
			go readServer(srv)

			// An asynchronous notice while idle waits for the next borrower
			// and leaves the server usable.
			_, _ = w.Write(encode(t, &pgproto3.NoticeResponse{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "idle notice"}))
			if ev := <-srv.events; ev.err != nil || ev.msg[0] != 'N' {
				t.Fatalf("idle notice = %q, %v", ev.msg, ev.err)
			}
			if srv.ended.Load() {
				t.Fatal("a notice ended the server")
			}

			tc.end(t, w)
			for deadline := time.Now().Add(5 * time.Second); !srv.ended.Load(); {
				if time.Now().After(deadline) {
					t.Fatal("the reader did not mark the server ended")
				}
				time.Sleep(5 * time.Millisecond)
			}
			srv.close()
			done := make(chan struct{})
			go func() {
				for range srv.events {
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the reader outlived its server")
			}
		})
	}
}

func TestEndsSession(t *testing.T) {
	for _, tc := range []struct {
		severity, unlocalized string
		want                  bool
	}{
		{"FATAL", "FATAL", true},
		{"PANIC", "", true},
		{"FATAL", "", true},
		{"ERROR", "ERROR", false},
		{"FEHLER", "ERROR", false},
	} {
		if got := endsSession(&pgproto3.ErrorResponse{Severity: tc.severity, SeverityUnlocalized: tc.unlocalized}); got != tc.want {
			t.Errorf("endsSession(%q, %q) = %v, want %v", tc.severity, tc.unlocalized, got, tc.want)
		}
	}
}
