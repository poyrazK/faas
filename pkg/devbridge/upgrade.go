package devbridge

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// UpgradeHeader marks a WebSocket handshake carried over one HTTP/2 tunnel
// stream (ADR-742). HTTP/2 forbids Connection/Upgrade, so the relay sends the
// handshake as an ordinary stream and the laptop answers 200 plus this marker
// when its loopback process switched protocols. The X-Gregale-Dev-Bridge-
// prefix means ClearCredentials removes it before every application hop.
const UpgradeHeader = "X-Gregale-Dev-Bridge-Upgrade"

const upgradeWebSocket = "websocket"

var (
	ErrUpgradeLimit    = errors.New("dev bridge upgraded connection limit reached")
	ErrUpgradeDisabled = errors.New("dev bridge upgrades are not enabled")
)

// UpgradeLimits bound long-lived upgraded connections per session. A zero
// MaxConnections disables upgrades; zero IdleTimeout/MaxBytes disable that
// bound (used only on the laptop, where the relay is authoritative).
type UpgradeLimits struct {
	MaxConnections int
	IdleTimeout    time.Duration
	MaxBytes       int64
}

// IsUpgradeRequest reports an HTTP/1.1 protocol switch of any kind.
func IsUpgradeRequest(r *http.Request) bool {
	return headerHasToken(r.Header, "Connection", "upgrade") && strings.TrimSpace(r.Header.Get("Upgrade")) != ""
}

// IsWebSocketUpgrade reports the only protocol switch the bridge forwards.
func IsWebSocketUpgrade(r *http.Request) bool {
	return r.Method == http.MethodGet && IsUpgradeRequest(r) && strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), upgradeWebSocket)
}

func headerHasToken(h http.Header, key, token string) bool {
	for _, value := range h.Values(key) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(textproto.TrimString(part), token) {
				return true
			}
		}
	}
	return false
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func removeHopHeaders(h http.Header) {
	for _, value := range h.Values("Connection") {
		for _, part := range strings.Split(value, ",") {
			if part = textproto.TrimString(part); part != "" {
				h.Del(part)
			}
		}
	}
	for _, key := range hopHeaders {
		h.Del(key)
	}
}

// tunnel copies both directions until either side ends, the idle timeout
// passes, a direction exceeds its byte cap, or stop fires. closeAll must
// unblock every pending Read and Write. It returns bytes moved toward the
// local process (sent) and toward the remote client (received).
func tunnel(stop <-chan struct{}, limits UpgradeLimits, closeAll func(), toLocal io.Writer, fromClient io.Reader, toClient io.Writer, fromLocal io.Reader, flushClient func()) (sent, received int64) {
	var once sync.Once
	shutdown := func() { once.Do(closeAll) }
	var idle *time.Timer
	if limits.IdleTimeout > 0 {
		idle = time.AfterFunc(limits.IdleTimeout, shutdown)
		defer idle.Stop()
	}
	var up, down atomic.Int64
	copyDirection := func(dst io.Writer, src io.Reader, counter *atomic.Int64, flush func()) {
		defer shutdown()
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if idle != nil {
					idle.Reset(limits.IdleTimeout)
				}
				if total := counter.Add(int64(n)); limits.MaxBytes > 0 && total > limits.MaxBytes {
					counter.Add(-int64(n))
					return
				}
				if _, err := dst.Write(buf[:n]); err != nil {
					return
				}
				if flush != nil {
					flush()
				}
			}
			if err != nil {
				return
			}
		}
	}
	done := make(chan struct{}, 2)
	go func() { copyDirection(toLocal, fromClient, &up, nil); done <- struct{}{} }()
	go func() { copyDirection(toClient, fromLocal, &down, flushClient); done <- struct{}{} }()
	pending := 2
	select {
	case <-done:
		pending--
	case <-stop:
	}
	shutdown()
	for ; pending > 0; pending-- {
		<-done
	}
	return up.Load(), down.Load()
}

// serveLocalUpgrade replays one relayed WebSocket handshake against the fixed
// loopback origin and, once it switches protocols, streams the connection over
// the HTTP/2 tunnel stream. The destination is never chosen by the request.
func serveLocalUpgrade(w http.ResponseWriter, r *http.Request, host string, inspector *Inspector) {
	var record uint64
	if inspector != nil {
		record = inspector.beginUpgrade(r)
	}
	finish := func(status int, sent, received int64, failed bool) {
		if inspector != nil {
			inspector.finishUpgrade(record, status, sent, received, failed)
		}
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(r.Context(), "tcp", host)
	if err != nil {
		finish(0, 0, 0, true)
		http.Error(w, "local process unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = conn.Close() }()
	target := *r.URL
	target.Scheme, target.Host, target.User = "", "", nil
	out := &http.Request{Method: http.MethodGet, URL: &target, Host: host, Header: r.Header.Clone(), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}
	removeHopHeaders(out.Header)
	ClearCredentials(out.Header)
	out.Header.Del("X-Faas-Caller-App")
	out.Header.Set("Connection", "Upgrade")
	out.Header.Set("Upgrade", upgradeWebSocket)
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := out.Write(conn); err != nil {
		finish(0, 0, 0, true)
		http.Error(w, "local process unavailable", http.StatusBadGateway)
		return
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, out)
	if err != nil {
		finish(0, 0, 0, true)
		http.Error(w, "local process unavailable", http.StatusBadGateway)
		return
	}
	_ = conn.SetDeadline(time.Time{})
	header := response.Header.Clone()
	removeHopHeaders(header)
	ClearCredentials(header)
	for key, values := range header {
		w.Header()[key] = values
	}
	if response.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(response.Header.Get("Upgrade"), upgradeWebSocket) {
		// The local process declined the handshake; return its answer as a
		// normal response and keep the stream non-upgraded.
		w.WriteHeader(response.StatusCode)
		received, _ := io.Copy(w, response.Body)
		_ = response.Body.Close()
		finish(response.StatusCode, 0, received, false)
		return
	}
	w.Header().Set(UpgradeHeader, upgradeWebSocket)
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	_ = controller.Flush()
	sent, received := tunnel(r.Context().Done(), UpgradeLimits{}, func() { _ = conn.Close(); _ = r.Body.Close() },
		conn, r.Body, w, reader, func() { _ = controller.Flush() })
	finish(http.StatusSwitchingProtocols, sent, received, false)
}

// limitedConn bounds a hijacked client connection used by the dependency
// direction, where net/http/httputil owns the protocol switch.
type limitedConn struct {
	net.Conn
	limits      UpgradeLimits
	idle        *time.Timer
	read, wrote atomic.Int64
	once        sync.Once
	onClose     func()
}

func newLimitedConn(conn net.Conn, limits UpgradeLimits, onClose func()) *limitedConn {
	c := &limitedConn{Conn: conn, limits: limits, onClose: onClose}
	if limits.IdleTimeout > 0 {
		c.idle = time.AfterFunc(limits.IdleTimeout, func() { _ = c.Close() })
	}
	return c
}

func (c *limitedConn) touch() {
	if c.idle != nil {
		c.idle.Reset(c.limits.IdleTimeout)
	}
}

func (c *limitedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.touch()
		if total := c.read.Add(int64(n)); c.limits.MaxBytes > 0 && total > c.limits.MaxBytes {
			_ = c.Close()
			return 0, ErrUpgradeLimit
		}
	}
	return n, err
}

func (c *limitedConn) Write(p []byte) (int, error) {
	if c.limits.MaxBytes > 0 && c.wrote.Load()+int64(len(p)) > c.limits.MaxBytes {
		_ = c.Close()
		return 0, ErrUpgradeLimit
	}
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.touch()
		c.wrote.Add(int64(n))
	}
	return n, err
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		if c.idle != nil {
			c.idle.Stop()
		}
		if c.onClose != nil {
			c.onClose()
		}
	})
	return err
}

// limitedHijacker wraps the dependency handler's ResponseWriter so the
// protocol switch performed by httputil.ReverseProxy hijacks a bounded conn.
type limitedHijacker struct {
	http.ResponseWriter
	limits  UpgradeLimits
	onClose func()
	mu      sync.Mutex
	conn    *limitedConn
}

func (h *limitedHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(h.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	limited := newLimitedConn(conn, h.limits, h.onClose)
	h.mu.Lock()
	h.conn = limited
	h.mu.Unlock()
	return limited, rw, nil
}

func (h *limitedHijacker) Unwrap() http.ResponseWriter { return h.ResponseWriter }

func (h *limitedHijacker) close() {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}
