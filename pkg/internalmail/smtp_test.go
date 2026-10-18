package internalmail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestSMTPSubmissionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rcpt, final int
		drop        bool
		state       string
	}{
		{"accepted", 250, 250, false, "submitted"},
		{"recipient-permanent", 550, 250, false, "failed"},
		{"recipient-temporary", 450, 250, false, "deferred"},
		{"data-permanent", 250, 550, false, "failed"},
		{"data-temporary", 250, 451, false, "deferred"},
		{"lost-data-reply", 250, 0, true, "uncertain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			done := make(chan error, 1)
			go func() { done <- smtpSession(listener, tc.rcpt, tc.final, tc.drop) }()
			state, code := (SMTP{Address: listener.Addr().String(), Hello: "mail.example.test"}).Submit(t.Context(), Envelope{From: "bounce@example.test", To: "one@example.net", Data: []byte("From: notify@example.test\r\n\r\nhello\r\n")})
			if state != tc.state {
				t.Fatalf("state=%s code=%s, want %s", state, code, tc.state)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("SMTP session did not end")
			}
		})
	}
}

func smtpSession(listener net.Listener, rcpt, final int, drop bool) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		return err
	}
	p := textproto.NewConn(conn)
	if err := p.PrintfLine("220 relay.example.test ESMTP"); err != nil {
		return err
	}
	for {
		line, err := p.ReadLine()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		reply := 250
		switch {
		case strings.HasPrefix(line, "EHLO "), strings.HasPrefix(line, "HELO "), strings.HasPrefix(line, "MAIL FROM:"):
		case strings.HasPrefix(line, "RCPT TO:"):
			reply = rcpt
		case line == "DATA":
			if err := p.PrintfLine("354 send data"); err != nil {
				return err
			}
			if _, err := io.Copy(io.Discard, p.DotReader()); err != nil {
				return err
			}
			if drop {
				return nil
			}
			if err := p.PrintfLine("%d result", final); err != nil {
				return err
			}
			continue
		case line == "QUIT":
			return p.PrintfLine("221 bye")
		default:
			return errors.New("unexpected SMTP command")
		}
		if err := p.PrintfLine("%d result", reply); err != nil {
			return err
		}
	}
}

func TestSMTPRefusesExternalRelay(t *testing.T) {
	state, _ := (SMTP{Address: "smtp.external.test:25", Hello: "mail.example.test"}).Submit(context.Background(), Envelope{})
	if state != "failed" {
		t.Fatal(state)
	}
}

func TestSMTPRequestsCorrelatedDSNsAndRequiresRelaySupport(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			done := make(chan error, 1)
			go func() { done <- dsnSession(listener, supported) }()
			state, code := (SMTP{Address: listener.Addr().String(), Hello: "mail.example.test", RequestDSN: true}).Submit(t.Context(), Envelope{
				From: "bounce+" + dsnRecipient + "@example.test", To: "one@example.net", RecipientID: dsnRecipient,
				Data: []byte("From: notify@example.test\r\n\r\nHello\r\n"),
			})
			if supported && state != "submitted" || !supported && (state != "deferred" || code != "relay_dsn_unavailable") {
				t.Fatal(state, code)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func dsnSession(listener net.Listener, supported bool) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		return err
	}
	p := textproto.NewConn(conn)
	if err := p.PrintfLine("220 relay.example.test ESMTP"); err != nil {
		return err
	}
	if line, err := p.ReadLine(); err != nil || !strings.HasPrefix(line, "EHLO ") {
		return errors.New("expected EHLO")
	}
	if supported {
		if err := p.PrintfLine("250-relay.example.test\r\n250 DSN"); err != nil {
			return err
		}
	} else {
		if err := p.PrintfLine("250 relay.example.test"); err != nil {
			return err
		}
		if _, err := p.ReadLine(); !errors.Is(err, io.EOF) {
			return errors.New("submitted without DSN support")
		}
		return nil
	}
	for _, command := range []string{
		"MAIL FROM:<bounce+" + dsnRecipient + "@example.test> RET=HDRS ENVID=gregale-" + dsnRecipient,
		"RCPT TO:<one@example.net> NOTIFY=SUCCESS,FAILURE,DELAY",
	} {
		line, err := p.ReadLine()
		if err != nil || line != command {
			return fmt.Errorf("DSN command %q, want %q", line, command)
		}
		if err := p.PrintfLine("250 accepted"); err != nil {
			return err
		}
	}
	if line, err := p.ReadLine(); err != nil || line != "DATA" {
		return errors.New("expected DATA")
	}
	if err := p.PrintfLine("354 send message"); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, p.DotReader()); err != nil {
		return err
	}
	return p.PrintfLine("250 queued")
}
