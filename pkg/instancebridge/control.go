package instancebridge

// Control socket (connection-survival ADR §2 "Gate and counters", phase P1d).
// Every byte for an instance passes through its bridge, and the bridge
// outlives vmmd, so the bridge counts the instance's activity. vmmd keeps one
// long-lived connection to each bridge's control socket and reads the
// counters.
//
// A control connection is line based:
//
//	client → bridge  "FAAS-IBRIDGE-CTL/1\n"
//	bridge → client  "HELLO <instance>\n"
//	client → bridge  "STATS\n"
//	bridge → client  "STATS <inflight> <sessions> <last-end-unix-nanos>\n"
//
// The client checks that the hello names the instance it expects before it
// trusts any counter. Unknown requests are answered "ERR <message>" and the
// connection closes. Later P1d slices add the gate requests.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ControlHello is the protocol name and major version of a control
// connection.
const ControlHello = "FAAS-IBRIDGE-CTL/1"

const (
	maxControlLine     = 256
	controlIdleTimeout = 2 * time.Minute
)

// Counters is an instance's activity as its bridge sees it: HTTP requests in
// flight, open raw, TCP and UDP sessions, and when the last of either ended.
// The zero value is ready to use.
type Counters struct {
	inflight atomic.Int64
	sessions atomic.Int64
	lastEnd  atomic.Int64 // unix nanos; 0 until something ends
	now      func() time.Time
}

// Stats is a snapshot of Counters.
type Stats struct {
	Inflight int64
	Sessions int64
	// LastEnd is when the last request or session ended; zero if none has.
	LastEnd time.Time
}

// Busy reports whether anything is in flight.
func (s Stats) Busy() bool { return s.Inflight > 0 || s.Sessions > 0 }

// BeginRequest counts an HTTP request until the returned func runs. The
// func is safe to call more than once; only the first call counts. A nil
// Counters counts nothing.
func (c *Counters) BeginRequest() func() {
	if c == nil {
		return func() {}
	}
	return c.begin(&c.inflight)
}

// BeginSession counts a raw, TCP or UDP session until the returned func
// runs. The func is safe to call more than once. A nil Counters counts
// nothing.
func (c *Counters) BeginSession() func() {
	if c == nil {
		return func() {}
	}
	return c.begin(&c.sessions)
}

func (c *Counters) begin(n *atomic.Int64) func() {
	n.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			c.lastEnd.Store(c.clock().UnixNano())
			n.Add(-1)
		})
	}
}

func (c *Counters) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Snapshot reads the counters.
func (c *Counters) Snapshot() Stats {
	s := Stats{Inflight: c.inflight.Load(), Sessions: c.sessions.Load()}
	if end := c.lastEnd.Load(); end != 0 {
		s.LastEnd = time.Unix(0, end)
	}
	return s
}

// ServeControl answers control connections on ln until ctx ends, then
// returns ctx's error, or until accepting fails. instance is the name the
// hello reports.
func ServeControl(ctx context.Context, ln net.Listener, instance string, c *Counters) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("accept control connection: %w", err)
		}
		go serveControlConn(ctx, conn, instance, c)
	}
}

func serveControlConn(ctx context.Context, conn net.Conn, instance string, c *Counters) {
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	r := bufio.NewReaderSize(conn, maxControlLine)
	_ = conn.SetReadDeadline(time.Now().Add(controlIdleTimeout))
	hello, err := readControlLine(r)
	if err != nil {
		return
	}
	if hello != ControlHello {
		_, _ = fmt.Fprintf(conn, "ERR unsupported control protocol\n")
		return
	}
	if _, err := fmt.Fprintf(conn, "HELLO %s\n", instance); err != nil {
		return
	}
	for {
		// vmmd polls; a silent connection is a dead client.
		_ = conn.SetReadDeadline(time.Now().Add(controlIdleTimeout))
		req, err := readControlLine(r)
		if err != nil {
			return
		}
		switch req {
		case "STATS":
			s := c.Snapshot()
			var end int64
			if !s.LastEnd.IsZero() {
				end = s.LastEnd.UnixNano()
			}
			if _, err := fmt.Fprintf(conn, "STATS %d %d %d\n", s.Inflight, s.Sessions, end); err != nil {
				return
			}
		default:
			_, _ = fmt.Fprintf(conn, "ERR unknown request\n")
			return
		}
	}
}

// readControlLine reads one line of at most maxControlLine bytes, without
// its newline.
func readControlLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", errors.New("control line too long")
		}
		return "", err
	}
	return strings.TrimSuffix(string(line), "\n"), nil
}

// ControlClient is vmmd's connection to one bridge's control socket. It is
// safe for concurrent use; requests are serialized.
type ControlClient struct {
	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
}

// DialControl connects to a bridge's control socket at path and checks that
// it serves instance.
func DialControl(ctx context.Context, path, instance string) (*ControlClient, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("dial bridge control socket: %w", err)
	}
	c, err := NewControlClient(ctx, conn, instance)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

// NewControlClient runs the hello on conn, a connection the caller dialed
// and whose peer it checked, and returns the client. It does not close conn
// on failure.
func NewControlClient(ctx context.Context, conn net.Conn, instance string) (*ControlClient, error) {
	c := &ControlClient{conn: conn, r: bufio.NewReaderSize(conn, maxControlLine)}
	if err := c.hello(ctx, instance); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *ControlClient) hello(ctx context.Context, instance string) error {
	line, err := c.roundTrip(ctx, ControlHello)
	if err != nil {
		return fmt.Errorf("bridge control hello: %w", err)
	}
	got, ok := strings.CutPrefix(line, "HELLO ")
	if !ok {
		return fmt.Errorf("bridge control hello: unexpected answer %q", line)
	}
	if got != instance {
		return fmt.Errorf("bridge control hello: bridge serves %q, want %q", got, instance)
	}
	return nil
}

// Stats reads the bridge's counters.
func (c *ControlClient) Stats(ctx context.Context) (Stats, error) {
	line, err := c.roundTrip(ctx, "STATS")
	if err != nil {
		return Stats{}, fmt.Errorf("bridge control stats: %w", err)
	}
	return parseStats(line)
}

func parseStats(line string) (Stats, error) {
	fields := strings.Fields(line)
	if len(fields) != 4 || fields[0] != "STATS" {
		return Stats{}, fmt.Errorf("bridge control stats: unexpected answer %q", line)
	}
	var n [3]int64
	for i, f := range fields[1:] {
		v, err := strconv.ParseInt(f, 10, 64)
		if err != nil || v < 0 {
			return Stats{}, fmt.Errorf("bridge control stats: bad counter %q", f)
		}
		n[i] = v
	}
	s := Stats{Inflight: n[0], Sessions: n[1]}
	if n[2] != 0 {
		s.LastEnd = time.Unix(0, n[2])
	}
	return s, nil
}

func (c *ControlClient) roundTrip(ctx context.Context, req string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	_ = c.conn.SetDeadline(deadline)
	if _, err := io.WriteString(c.conn, req+"\n"); err != nil {
		return "", err
	}
	line, err := readControlLine(c.r)
	if err != nil {
		return "", err
	}
	if msg, ok := strings.CutPrefix(line, "ERR "); ok {
		return "", fmt.Errorf("bridge refused %s: %s", req, msg)
	}
	return line, nil
}

// Close closes the connection.
func (c *ControlClient) Close() error { return c.conn.Close() }
