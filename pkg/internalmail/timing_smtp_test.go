package internalmail

import (
	"errors"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSMTPExpiredMessageNeverDialsRelay(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	state, code := (SMTP{Address: listener.Addr().String(), Hello: "mail.example.test"}).Submit(t.Context(), Envelope{ExpiresAt: timeRef(time.Now().Add(-time.Second))})
	if state != "expired" || code != "message_expired" {
		t.Fatal("expired mail was not rejected", state, code)
	}
	if err := listener.SetDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if conn != nil {
		_ = conn.Close()
		t.Fatal("expired mail connected to SMTP")
	}
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatal("could not prove no SMTP connection", err)
	}
}

func TestSMTPDeadlineBeforeBodyAndAfterDataCustody(t *testing.T) {
	for _, stage := range []string{"greeting", "envelope", "before_body", "after_body", "accepted"} {
		t.Run(stage, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			var body atomic.Bool
			done := make(chan error, 1)
			go func() { done <- deadlineSession(listener, stage, &body) }()
			expires := time.Now().Add(500 * time.Millisecond)
			state, code := (SMTP{Address: listener.Addr().String(), Hello: "mail.example.test"}).Submit(t.Context(), Envelope{From: "bounce@example.test", To: "one@example.net", Data: []byte("From: notify@example.test\r\n\r\nbody\r\n"), ExpiresAt: &expires})
			want := "expired"
			if stage == "after_body" {
				want = "uncertain"
			} else if stage == "accepted" {
				want = "submitted"
			}
			if state != want {
				t.Fatal("incorrect deadline custody", state, code, want)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("deadline SMTP peer did not stop")
			}
			if body.Load() != (stage == "after_body" || stage == "accepted") {
				t.Fatal("deadline allowed unexpected body transfer")
			}
		})
	}
}

func deadlineSession(listener net.Listener, stage string, body *atomic.Bool) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		return err
	}
	p := textproto.NewConn(conn)
	waitClose := func() error {
		_, err := p.ReadLine()
		if errors.Is(err, io.EOF) {
			return nil
		}
		return errors.New("SMTP deadline failed to close a pending session")
	}
	if stage == "greeting" {
		return waitClose()
	}
	if err := p.PrintfLine("220 relay.example.test ESMTP"); err != nil {
		return err
	}
	for {
		line, err := p.ReadLine()
		if err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(line, "EHLO "), strings.HasPrefix(line, "HELO "), strings.HasPrefix(line, "MAIL FROM:"):
		case strings.HasPrefix(line, "RCPT TO:"):
			if stage == "envelope" {
				return waitClose()
			}
		case line == "DATA":
			if stage == "before_body" {
				return waitClose()
			}
			if err := p.PrintfLine("354 send data"); err != nil {
				return err
			}
			if _, err := io.Copy(io.Discard, p.DotReader()); err != nil {
				return err
			}
			body.Store(true)
			if stage == "after_body" {
				return waitClose()
			}
			return p.PrintfLine("250 accepted")
		default:
			return errors.New("unexpected SMTP deadline command")
		}
		if err := p.PrintfLine("250 OK"); err != nil {
			return err
		}
	}
}
