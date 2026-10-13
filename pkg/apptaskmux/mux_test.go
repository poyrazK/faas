package apptaskmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// startEcho listens on loopback and echoes each connection upper-cased.
func startEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				data, _ := io.ReadAll(conn)
				_, _ = conn.Write(bytes.ToUpper(data))
			}()
		}
	}()
	return ln.Addr().String()
}

// startRelay wires Forward and Serve back to back through two pipes, the way
// the CLI and guest-init are wired through one session's stdin/stdout.
func startRelay(t *testing.T, dial func(context.Context) (net.Conn, error)) (string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	toGuestR, toGuestW := io.Pipe()
	fromGuestR, fromGuestW := io.Pipe()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = Serve(ctx, toGuestR, fromGuestW, dial)
		_ = fromGuestW.Close()
	}()
	go func() {
		defer wg.Done()
		_ = Forward(ctx, ln, toGuestW, fromGuestR)
		_ = toGuestW.Close()
	}()
	t.Cleanup(func() {
		cancel()
		_ = toGuestW.Close()
		_ = fromGuestW.Close()
		wg.Wait()
	})
	return ln.Addr().String(), cancel
}

func roundTrip(addr, payload string) (string, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", err
	}
	_ = conn.(*net.TCPConn).CloseWrite()
	got, err := io.ReadAll(conn)
	return string(got), err
}

func TestRelayCarriesConcurrentConnections(t *testing.T) {
	target := startEcho(t)
	addr, _ := startRelay(t, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", target)
	})
	big := strings.Repeat("abcdefgh", 3*MaxPayload/8+5) // spans several frames
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := fmt.Sprintf("conn-%d:", i) + big
			got, err := roundTrip(addr, payload)
			if err != nil {
				errs <- err
				return
			}
			if got != strings.ToUpper(payload) {
				errs <- fmt.Errorf("conn %d: got %d bytes, want %d", i, len(got), len(payload))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestRelayDialFailureClosesLocalConnection(t *testing.T) {
	addr, _ := startRelay(t, func(context.Context) (net.Conn, error) {
		return nil, errors.New("connection refused")
	})
	got, err := roundTrip(addr, "hello")
	if err != nil && !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "reset") {
		t.Fatalf("roundTrip error = %v", err)
	}
	if got != "" {
		t.Fatalf("got %q from a failed dial", got)
	}
}

func TestReaderRejectsMalformedFrames(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.Data(7, []byte("ok"))
	frame, err := NewReader(&buf).Next()
	if err != nil || frame.Type != FrameData || frame.Stream != 7 || string(frame.Payload) != "ok" {
		t.Fatalf("frame = %+v, %v", frame, err)
	}
	for name, raw := range map[string][]byte{
		"unknown type":  {9, 0, 0, 0, 1, 0, 0, 0, 0},
		"oversized":     {FrameData, 0, 0, 0, 1, 0xff, 0xff, 0xff, 0xff},
		"open w/ data":  {FrameOpen, 0, 0, 0, 1, 0, 0, 0, 1, 'x'},
		"short header":  {FrameData, 0, 0},
		"short payload": {FrameData, 0, 0, 0, 1, 0, 0, 0, 4, 'x'},
	} {
		if _, err := NewReader(bytes.NewReader(raw)).Next(); !errors.Is(err, ErrMalformed) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if _, err := NewReader(bytes.NewReader(nil)).Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty stream err = %v", err)
	}
}

func TestServeRejectsTooManyStreams(t *testing.T) {
	var frames bytes.Buffer
	w := NewWriter(&frames)
	for i := uint32(1); i <= MaxStreams+1; i++ {
		_ = w.Open(i)
	}
	// Dials stay pending long enough for the last Open to find the table full.
	block := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(block)
	}()
	var out bytes.Buffer
	var outMu sync.Mutex
	err := Serve(context.Background(), &frames, writerFunc(func(p []byte) (int, error) {
		outMu.Lock()
		defer outMu.Unlock()
		return out.Write(p)
	}), func(context.Context) (net.Conn, error) {
		<-block
		return nil, errors.New("closed")
	})
	_ = err
	outMu.Lock()
	defer outMu.Unlock()
	frame, err := NewReader(bytes.NewReader(out.Bytes())).Next()
	if err != nil || frame.Type != FrameClose || frame.Stream != MaxStreams+1 || string(frame.Payload) != "stream rejected" {
		t.Fatalf("first reply = %+v, %v", frame, err)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
