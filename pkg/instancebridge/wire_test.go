package instancebridge

// adr: 20261017 connection survival, §2 data protocol (P1b)

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestHelloRoundTripsAndRejectsWhatIsNotOne(t *testing.T) {
	var b bytes.Buffer
	if err := WriteHello(&b, KindTCP, 5432); err != nil {
		t.Fatal(err)
	}
	if !IsHello(b.Bytes()) {
		t.Fatal("a hello is not recognised")
	}
	kind, port, err := ReadHello(bufio.NewReader(&b))
	if err != nil || kind != KindTCP || port != 5432 {
		t.Fatalf("hello = %q %d %v", kind, port, err)
	}
	for _, line := range []string{
		"FAAS-IBRIDGE/2 tcp 5432\n",     // another major version
		"FAAS-IBRIDGE/1 sctp 5432\n",    // unknown kind
		"FAAS-IBRIDGE/1 tcp 0\n",        // no port
		"FAAS-IBRIDGE/1 tcp 65536\n",    // out of range
		"FAAS-IBRIDGE/1 tcp 080\n",      // not canonical
		"FAAS-IBRIDGE/1 tcp 80",         // no newline
		"FAAS-IBRIDGE/1 tcp 80 x\n",     // extra field
		strings.Repeat("A", 100) + "\n", // too long
	} {
		if _, _, err := ReadHello(bufio.NewReader(strings.NewReader(line))); err == nil {
			t.Errorf("accepted hello %q", line)
		}
	}
	for _, peek := range []string{"PRI * HTTP/2.0\r\n\r\nSM", "GET / HTTP/1.1\r\n", "FAAS-IBRIDGE"} {
		if IsHello([]byte(peek)) {
			t.Errorf("%q taken for a session", peek)
		}
	}
	if WriteHello(io.Discard, "sctp", 1) == nil || WriteHello(io.Discard, KindUDP, 0) == nil {
		t.Fatal("wrote an invalid hello")
	}
}

func TestReadyCarriesTheDialError(t *testing.T) {
	var b bytes.Buffer
	_ = WriteReady(&b, nil)
	_ = WriteReady(&b, errors.New("connection refused\nsecond line"))
	r := bufio.NewReader(&b)
	if err := ReadReady(r); err != nil {
		t.Fatalf("OK = %v", err)
	}
	var dial *DialError
	if err := ReadReady(r); !errors.As(err, &dial) || dial.Message != "connection refused second line" {
		t.Fatalf("ERR = %v", err)
	}
	if err := ReadReady(bufio.NewReader(strings.NewReader("HTTP/1.1 400 Bad Request\r\n"))); err == nil || errors.As(err, &dial) {
		t.Fatalf("an HTTP answer = %v, want a protocol error", err)
	}
}

func TestFramesCarryBytesAndTheEnd(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 3*maxFrameBytes+17)
	for _, tc := range []struct {
		name    string
		code    byte
		message string
		wantErr func(error) bool
	}{
		{"clean end", CodeOK, "", func(err error) bool { return errors.Is(err, io.EOF) }},
		{"failed end", CodeIO, "write body: reset", func(err error) bool {
			var s *SessionError
			return errors.As(err, &s) && s.Code == CodeIO && s.Message == "write body: reset"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			w := NewFrameWriter(&b)
			if n, err := w.Write(big); err != nil || n != len(big) {
				t.Fatalf("write = %d, %v", n, err)
			}
			if err := w.End(tc.code, tc.message); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("late")); err == nil {
				t.Fatal("wrote after the end")
			}
			raw := append([]byte(nil), b.Bytes()...)
			got, _ := io.ReadAll(NewFrameReader(bufio.NewReader(bytes.NewReader(raw))))
			if !bytes.Equal(got, big) {
				t.Fatalf("read %d bytes, want %d", len(got), len(big))
			}
			// io.ReadAll swallows io.EOF; read the end frame on its own.
			r := NewFrameReader(bufio.NewReader(bytes.NewReader(raw[len(raw)-4-len(tc.message):])))
			_, err := r.Read(make([]byte, 1))
			if !tc.wantErr(err) {
				t.Fatalf("end = %v", err)
			}
		})
	}
}

func TestFramesWithoutAnEndAreUnexpected(t *testing.T) {
	var b bytes.Buffer
	_, _ = NewFrameWriter(&b).Write([]byte("partial"))
	for _, cut := range []int{0, 3, b.Len() - 2, b.Len()} {
		r := NewFrameReader(bufio.NewReader(bytes.NewReader(b.Bytes()[:cut])))
		if _, err := io.ReadAll(r); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("cut at %d: %v, want unexpected EOF", cut, err)
		}
	}
	if _, err := io.ReadAll(NewFrameReader(bufio.NewReader(strings.NewReader("Zjunk")))); err == nil {
		t.Fatal("read an unknown frame")
	}
}
