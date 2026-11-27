package gateway

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRedis is a tiny in-memory Redis behind redisAttachFunc. Each attach is
// one leg; legs records the commands each leg received.
type fakeRedis struct {
	mu      sync.Mutex
	data    map[string]string
	legs    [][]string
	open    int
	block   chan struct{} // BLPOP waits for this
	push    bool          // send a RESP3 push frame before each GET reply
	failing bool
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{data: map[string]string{}, block: make(chan struct{})}
}

func (f *fakeRedis) attach(ctx context.Context, conn net.Conn) (bool, error) {
	f.mu.Lock()
	if f.failing {
		f.mu.Unlock()
		return false, errors.New("wake_failed")
	}
	f.legs = append(f.legs, nil)
	leg := len(f.legs) - 1
	f.open++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.open--
		f.mu.Unlock()
	}()
	r := bufio.NewReader(conn)
	for {
		cmd, err := respReadCommandHead(r)
		if err != nil {
			return false, nil
		}
		if err := cmd.readRest(r); err != nil {
			return false, err
		}
		args := make([]string, len(cmd.args))
		for i, a := range cmd.args {
			args[i] = string(a)
		}
		f.mu.Lock()
		f.legs[leg] = append(f.legs[leg], strings.Join(args, " "))
		f.mu.Unlock()
		var reply string
		switch cmd.name {
		case "PING":
			reply = "+PONG\r\n"
		case "AUTH", "SELECT", "MULTI":
			reply = "+OK\r\n"
		case "SET":
			f.mu.Lock()
			f.data[args[1]] = args[2]
			f.mu.Unlock()
			reply = "+OK\r\n"
		case "GET":
			f.mu.Lock()
			v, ok := f.data[args[1]]
			push := f.push
			f.mu.Unlock()
			if push {
				reply = ">2\r\n$10\r\ninvalidate\r\n*1\r\n$1\r\nk\r\n"
			}
			if !ok {
				reply += "$-1\r\n"
			} else {
				reply += "$" + strconv.Itoa(len(v)) + "\r\n" + v + "\r\n"
			}
		case "SUBSCRIBE":
			reply = "*3\r\n$9\r\nsubscribe\r\n$" + strconv.Itoa(len(args[1])) + "\r\n" + args[1] + "\r\n:1\r\n"
		case "BLPOP":
			select {
			case <-f.block:
			case <-ctx.Done():
			}
			reply = "*-1\r\n"
		default:
			reply = "-ERR unknown\r\n"
		}
		if _, err := io.WriteString(conn, reply); err != nil {
			return false, nil
		}
	}
}

func (f *fakeRedis) snapshot() ([][]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.legs))
	for i, leg := range f.legs {
		out[i] = append([]string(nil), leg...)
	}
	return out, f.open
}

type redisTestClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func startRedisSession(t *testing.T, f *fakeRedis, detachAfter time.Duration) (*redisTestClient, *redisSession, chan error) {
	t.Helper()
	client, server := net.Pipe()
	s := newRedisSession(server, f.attach)
	s.detachAfter = detachAfter
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, err := s.run(ctx)
		done <- err
	}()
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("session did not end")
		}
	})
	return &redisTestClient{t: t, conn: client, r: bufio.NewReader(client)}, s, done
}

func (c *redisTestClient) do(args ...string) string {
	c.t.Helper()
	raw := make([][]byte, len(args))
	for i, a := range args {
		raw[i] = []byte(a)
	}
	_ = c.conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.conn.Write(respEncodeCommand(raw...)); err != nil {
		c.t.Fatalf("write %v: %v", args, err)
	}
	return c.read()
}

func (c *redisTestClient) read() string {
	c.t.Helper()
	var b bytes.Buffer
	_ = c.conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := respCopyValue(c.r, &b); err != nil {
		c.t.Fatalf("read reply: %v", err)
	}
	return b.String()
}

func redisWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRedisSessionForwardsCommands(t *testing.T) {
	f := newFakeRedis()
	c, _, _ := startRedisSession(t, f, time.Hour)
	if got := c.do("AUTH", "default", "pw"); got != "+OK\r\n" {
		t.Fatalf("AUTH = %q", got)
	}
	if got := c.do("SET", "k", "v"); got != "+OK\r\n" {
		t.Fatalf("SET = %q", got)
	}
	if got := c.do("GET", "k"); got != "$1\r\nv\r\n" {
		t.Fatalf("GET = %q", got)
	}
	legs, _ := f.snapshot()
	if len(legs) != 1 || len(legs[0]) != 3 {
		t.Fatalf("legs = %v", legs)
	}
}

func TestRedisSessionDetachesWhenQuietAndReplaysStateOnReattach(t *testing.T) {
	f := newFakeRedis()
	c, _, _ := startRedisSession(t, f, 40*time.Millisecond)
	c.do("AUTH", "default", "pw")
	c.do("SELECT", "3")
	c.do("SET", "k", "v")

	// Quiet: the leg closes, so the cache sees no activity.
	redisWaitFor(t, "detach", func() bool { _, open := f.snapshot(); return open == 0 })

	// A detached session answers PING itself, with no new leg.
	if got := c.do("PING"); got != "+PONG\r\n" {
		t.Fatalf("PING = %q", got)
	}
	if got := c.do("PING", "hi"); got != "$2\r\nhi\r\n" {
		t.Fatalf("PING hi = %q", got)
	}
	if legs, _ := f.snapshot(); len(legs) != 1 {
		t.Fatalf("PING opened a leg: %v", legs)
	}

	// A data command re-attaches, replays AUTH and SELECT, and only the GET
	// reply reaches the client.
	if got := c.do("GET", "k"); got != "$1\r\nv\r\n" {
		t.Fatalf("GET = %q", got)
	}
	legs, _ := f.snapshot()
	if len(legs) != 2 {
		t.Fatalf("legs = %v", legs)
	}
	want := []string{"AUTH default pw", "SELECT 3", "GET k"}
	if strings.Join(legs[1], "|") != strings.Join(want, "|") {
		t.Fatalf("second leg = %v, want %v", legs[1], want)
	}
}

func TestRedisSessionHousekeepingDoesNotKeepCacheAwake(t *testing.T) {
	f := newFakeRedis()
	c, _, _ := startRedisSession(t, f, 60*time.Millisecond)
	c.do("SET", "k", "v")
	// Pings while attached go to the cache but do not count as activity.
	stop := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(stop) {
		c.do("PING")
		time.Sleep(10 * time.Millisecond)
	}
	redisWaitFor(t, "detach despite pings", func() bool { _, open := f.snapshot(); return open == 0 })
}

func TestRedisSessionSubscriptionStaysAttached(t *testing.T) {
	f := newFakeRedis()
	c, s, _ := startRedisSession(t, f, 20*time.Millisecond)
	c.do("SUBSCRIBE", "news")
	time.Sleep(100 * time.Millisecond)
	if _, open := f.snapshot(); open != 1 {
		t.Fatal("subscribed session detached")
	}
	if s.maybeDetach() {
		t.Fatal("maybeDetach detached a subscribed session")
	}
}

func TestRedisSessionOutstandingReplyBlocksDetach(t *testing.T) {
	f := newFakeRedis()
	c, _, _ := startRedisSession(t, f, 20*time.Millisecond)
	raw := respEncodeCommand([]byte("BLPOP"), []byte("q"), []byte("0"))
	if _, err := c.conn.Write(raw); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, open := f.snapshot(); open != 1 {
		t.Fatal("detached with a blocking command outstanding")
	}
	close(f.block)
	if got := c.read(); got != "*-1\r\n" {
		t.Fatalf("BLPOP = %q", got)
	}
	redisWaitFor(t, "detach after the reply", func() bool { _, open := f.snapshot(); return open == 0 })
}

func TestRedisSessionMultiStaysAttached(t *testing.T) {
	f := newFakeRedis()
	c, s, _ := startRedisSession(t, f, 20*time.Millisecond)
	c.do("MULTI")
	time.Sleep(80 * time.Millisecond)
	if s.maybeDetach() {
		t.Fatal("detached inside MULTI")
	}
	if _, open := f.snapshot(); open != 1 {
		t.Fatal("leg closed inside MULTI")
	}
}

func TestRedisSessionUnavailableCacheAnswersAnError(t *testing.T) {
	f := newFakeRedis()
	f.failing = true
	c, _, _ := startRedisSession(t, f, time.Hour)
	if got := c.do("AUTH", "default", "pw"); got != redisUnavailableReply {
		t.Fatalf("reply = %q", got)
	}
	f.mu.Lock()
	f.failing = false
	f.mu.Unlock()
	if got := c.do("SET", "k", "v"); got != "+OK\r\n" {
		t.Fatalf("after recovery: %q", got)
	}
}

func TestRedisSessionPushFramesAreNotReplies(t *testing.T) {
	f := newFakeRedis()
	f.push = true
	c, _, _ := startRedisSession(t, f, 30*time.Millisecond)
	c.do("SET", "k", "v")
	if got := c.read2("GET", "k"); !strings.HasSuffix(got, "$1\r\nv\r\n") {
		t.Fatalf("GET with push = %q", got)
	}
	redisWaitFor(t, "detach after push", func() bool { _, open := f.snapshot(); return open == 0 })
}

// read2 sends a command and reads two values (a push frame, then the reply).
func (c *redisTestClient) read2(args ...string) string {
	c.t.Helper()
	first := c.do(args...)
	return first + c.read()
}

func TestRedisSessionRefusesPlatformCommands(t *testing.T) {
	f := newFakeRedis()
	c, _, _ := startRedisSession(t, f, time.Hour)
	for _, args := range [][]string{
		{"CONFIG", "SET", "maxmemory", "0"},
		{"REPLICAOF", "evil.example", "6379"},
		{"PSYNC", "?", "-1"},
		{"replconf", "listening-port", "1"},
	} {
		if got := c.do(args...); !strings.HasPrefix(got, "-ERR ") || !strings.Contains(got, "not allowed") {
			t.Fatalf("%v = %q", args, got)
		}
	}
	if legs, _ := f.snapshot(); len(legs) != 0 {
		t.Fatalf("a refused command reached the cache: %v", legs)
	}
	if got := c.do("SET", "k", "v"); got != "+OK\r\n" {
		t.Fatalf("session unusable after refusals: %q", got)
	}
}

// A refused command pipelined behind a command the cache has not answered yet
// is answered after it: Redis replies to a pipeline in order.
func TestRedisSessionRefusalKeepsPipelineOrder(t *testing.T) {
	f := newFakeRedis()
	c, _, _ := startRedisSession(t, f, time.Hour)
	if got := c.do("SET", "k", "v"); got != "+OK\r\n" {
		t.Fatalf("SET = %q", got)
	}
	pipeline := append(respEncodeCommand([]byte("BLPOP"), []byte("q"), []byte("0")),
		respEncodeCommand([]byte("CONFIG"), []byte("GET"), []byte("maxmemory"))...)
	pipeline = append(pipeline, respEncodeCommand([]byte("PING"))...)
	go func() { _, _ = c.conn.Write(pipeline) }()
	time.Sleep(50 * time.Millisecond) // CONFIG is read while BLPOP is outstanding
	close(f.block)
	for i, want := range []string{"*-1\r\n", "-ERR CONFIG", "+PONG\r\n"} {
		if got := c.read(); !strings.HasPrefix(got, want) {
			t.Fatalf("reply %d = %q, want %q first", i, got, want)
		}
	}
}

func TestRedisSessionQuitWhileDetached(t *testing.T) {
	f := newFakeRedis()
	c, _, done := startRedisSession(t, f, time.Hour)
	if got := c.do("QUIT"); got != "+OK\r\n" {
		t.Fatalf("QUIT = %q", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		done <- nil // for the cleanup
	case <-time.After(2 * time.Second):
		t.Fatal("QUIT did not end the session")
	}
	if legs, _ := f.snapshot(); len(legs) != 0 {
		t.Fatalf("QUIT opened a leg: %v", legs)
	}
}

func TestRESPCopyValueNestedAndStreamed(t *testing.T) {
	big := strings.Repeat("x", 200<<10)
	in := "%2\r\n+a\r\n*2\r\n:1\r\n$" + strconv.Itoa(len(big)) + "\r\n" + big + "\r\n$1\r\nb\r\n_\r\n"
	var out bytes.Buffer
	kind, err := respCopyValue(bufio.NewReaderSize(strings.NewReader(in), respMaxLine), &out)
	if err != nil || kind != '%' || out.String() != in {
		t.Fatalf("kind=%q err=%v same=%v", kind, err, out.String() == in)
	}
}

func TestRESPReadCommandInlineAndArray(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("PING\r\n*2\r\n$3\r\nget\r\n$1\r\nk\r\n"))
	cmd, err := respReadCommandHead(r)
	if err != nil || cmd.name != "PING" || string(cmd.raw) != "PING\r\n" {
		t.Fatalf("inline: %+v %v", cmd, err)
	}
	cmd, err = respReadCommandHead(r)
	if err != nil || cmd.name != "GET" || cmd.rest != 1 {
		t.Fatalf("array head: %+v %v", cmd, err)
	}
	var rest bytes.Buffer
	if err := cmd.copyRest(r, &rest); err != nil || rest.String() != "$1\r\nk\r\n" {
		t.Fatalf("rest = %q %v", rest.String(), err)
	}
	if _, err := respReadCommandHead(bufio.NewReader(strings.NewReader("*2\r\n$999999\r\n"))); !errors.Is(err, errRESPProtocol) {
		t.Fatalf("oversized name: %v", err)
	}
}
