package instancebridge

// adr: 20261017 connection survival, §2 data protocol (P1b)

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/udpwire"
)

// bridgeSocket serves sessions for guests on 127.0.0.1 and HTTP on the
// rest, as the instance bridge does, and returns its socket path.
func bridgeSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ib")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "data.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var d net.Dialer
	httpLn := Split(ctx, ln, &Sessions{GuestIP: "127.0.0.1", Dial: d.DialContext})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "http still served") }), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(httpLn) }()
	t.Cleanup(func() { cancel(); _ = srv.Close() })
	return path
}

// session opens a session and returns its connection, past the ready line.
func session(t *testing.T, path, kind string, port int) (*net.UnixConn, *bufio.Reader, error) {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := WriteHello(c, kind, uint16(port)); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(c)
	return c.(*net.UnixConn), r, ReadReady(r)
}

func tcpGuest(t *testing.T, serve func(net.Conn)) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); serve(c) }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestTCPSessionCarriesBytesBothWaysAndEndsClean(t *testing.T) {
	path := bridgeSocket(t)
	port := tcpGuest(t, func(c net.Conn) {
		body, _ := io.ReadAll(c) // until the client's half-close
		_, _ = c.Write([]byte("echo:" + string(body)))
	})
	c, r, err := session(t, path, KindTCP, port)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = c.CloseWrite()
	got, err := io.ReadAll(NewFrameReader(r))
	if err != nil || string(got) != "echo:ping" {
		t.Fatalf("session read %q, %v", got, err)
	}
}

func TestRawSessionSendsTheHeadThenTheBody(t *testing.T) {
	path := bridgeSocket(t)
	port := tcpGuest(t, func(c net.Conn) {
		head, _ := bufio.NewReader(c).ReadString('\n')
		_, _ = io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\nframe:"+strings.TrimSpace(head))
	})
	c, r, err := session(t, path, KindRaw, port)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "GET /ws HTTP/1.1\r\n\r\n")
	_ = c.CloseWrite()
	got, err := io.ReadAll(NewFrameReader(r))
	want := "HTTP/1.1 101 Switching Protocols\nUpgrade: websocket\n\nframe:GET /ws HTTP/1.1"
	if err != nil || string(got) != want {
		t.Fatalf("raw session = %q, %v; want %q", got, err, want)
	}
}

func TestUDPSessionCarriesDatagrams(t *testing.T) {
	path := bridgeSocket(t)
	guest, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guest.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := guest.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = guest.WriteToUDP(append([]byte("pong:"), buf[:n]...), from)
		}
	}()
	c, r, err := session(t, path, KindUDP, guest.LocalAddr().(*net.UDPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	if err := udpwire.Write(c, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	got, err := udpwire.Read(NewFrameReader(r))
	if err != nil || string(got) != "pong:ping" {
		t.Fatalf("datagram = %q, %v", got, err)
	}
}

func TestSessionReportsADialFailure(t *testing.T) {
	path := bridgeSocket(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // nothing listens there now
	var dial *DialError
	if _, _, err := session(t, path, KindTCP, port); !errors.As(err, &dial) {
		t.Fatalf("ready = %v, want a dial error", err)
	}
}

// A guest that takes no request body while the client still sends it is a
// failed session, not a clean, truncated one.
func TestRawSessionEndsInFailureWhenTheGuestDropsTheRequest(t *testing.T) {
	path := bridgeSocket(t)
	port := tcpGuest(t, func(c net.Conn) {
		_, _ = io.ReadFull(c, make([]byte, 1)) // the session is up
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\ndone")
		if tcp, ok := c.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0) // reset: the request body is refused
		}
	})
	c, r, err := session(t, path, KindRaw, port)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		chunk := make([]byte, 64*1024)
		for i := 0; i < 64; i++ {
			if _, err := c.Write(chunk); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		_ = c.CloseWrite()
	}()
	_, err = io.ReadAll(NewFrameReader(r))
	var s *SessionError
	if !errors.As(err, &s) || s.Code != CodeIO {
		t.Fatalf("session end = %v, want a failed end", err)
	}
}

func TestHTTPStillServedOnTheSameSocket(t *testing.T) {
	path := bridgeSocket(t)
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
	resp, err := client.Get("http://bridge/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "http still served" {
		t.Fatalf("HTTP body = %q", body)
	}
}

// A connection that never says what it is is handed to HTTP after the
// hello timeout instead of holding the accept loop.
func TestSplitDoesNotWaitOnASilentConnection(t *testing.T) {
	path := bridgeSocket(t)
	silent, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	c, r, err := session(t, path, KindTCP, tcpGuest(t, func(c net.Conn) { _, _ = c.Write([]byte("hi")) }))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.CloseWrite()
	if got, err := io.ReadAll(NewFrameReader(r)); err != nil || string(got) != "hi" {
		t.Fatalf("session beside a silent connection = %q, %v", got, err)
	}
}
