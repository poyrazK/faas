package gateway

// redisSession is the managed Redis cache's client session
// (ADR-20261116192126, slice 2). It sits between the caller's connection and
// the cache and lets an idle cache park without dropping the caller:
//
//   - Idleness comes from Redis commands, not bytes. PING, AUTH, HELLO,
//     SELECT, CLIENT and similar housekeeping never keep a cache awake.
//   - After detachAfter with no data command, no reply outstanding and no
//     pinned state, the session closes its upstream stream. vmmd then sees
//     no activity on the instance, and its idle timeout parks it.
//   - The caller's connection stays open. A detached session answers PING,
//     QUIT and RESET itself. Any other command re-attaches: the session
//     dials again (waking the cache when it is parked), replays the state it
//     kept (HELLO, AUTH, SELECT, CLIENT SETNAME), drops those replies, and
//     forwards the command. The caller sees one slow reply.
//   - Pinned states keep the stream attached: a subscription, client-side
//     tracking, an open MULTI or WATCH. A blocking command is covered by its
//     outstanding reply.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// redisDetachAfter is how long a session stays attached with no data
// command. The cache's own idle timeout runs after the detach.
const redisDetachAfter = 30 * time.Second

// redisHousekeeping are commands that never count as cache activity.
var redisHousekeeping = map[string]bool{
	"PING": true, "ECHO": true, "HELLO": true, "AUTH": true, "SELECT": true,
	"CLIENT": true, "QUIT": true, "RESET": true, "INFO": true, "COMMAND": true,
	"READONLY": true, "READWRITE": true,
}

// redisDeniedCommands are refused for customer sessions: they belong to the
// platform (configuration, replication, lifecycle).
var redisDeniedCommands = func() map[string]bool {
	out := map[string]bool{}
	for _, name := range api.ManagedValkeyClientDeniedCommands {
		out[name] = true
	}
	return out
}()

// redisKeptCommands are read whole so the session can answer or replay them.
var redisKeptCommands = map[string]bool{
	"PING": true, "AUTH": true, "HELLO": true, "SELECT": true, "CLIENT": true,
	"QUIT": true, "RESET": true, "SUBSCRIBE": true, "PSUBSCRIBE": true, "SSUBSCRIBE": true,
	"MULTI": true, "EXEC": true, "DISCARD": true, "UNWATCH": true,
}

// redisAttachFunc forwards one upstream leg over conn until it ends. It
// behaves like ServiceTCPProxy.forward: it routes, wakes the cache when
// needed, and returns when either side closes.
type redisAttachFunc func(ctx context.Context, conn net.Conn) (woke bool, err error)

type redisSession struct {
	client      net.Conn
	attachFn    redisAttachFunc
	detachAfter time.Duration
	now         func() time.Time

	clientMu sync.Mutex // serializes writes to client

	mu     sync.Mutex
	up     net.Conn // nil while detached
	upDone chan struct{}
	busy   bool // the client goroutine is writing upstream
	// sending is set while a command's head is being written to the leg;
	// a leg that ends meanwhile sets lostWhileSending and leaves the
	// verdict to the writer, which knows whether the command got through.
	sending          bool
	lostWhileSending bool
	lastData         time.Time
	woke             bool
	attaches         int

	// Replayed state.
	hello  []byte
	auth   []byte
	selDB  []byte
	name   []byte
	pinned bool // subscribed or tracking: stays attached for good
	multi  bool
	watch  bool

	pending atomic.Int64 // replies the cache still owes the client
	swallow atomic.Int64 // replay replies to drop before pending ones
	// drained is signalled when pending reaches zero or a leg ends, so a
	// reply the session makes itself waits behind the cache's replies to
	// earlier commands (see writeOwnReply).
	drained chan struct{}
}

func newRedisSession(client net.Conn, attach redisAttachFunc) *redisSession {
	return &redisSession{client: client, attachFn: attach, detachAfter: redisDetachAfter, now: time.Now, drained: make(chan struct{}, 1)}
}

// run serves the session until the client leaves or a leg fails with
// replies outstanding. It reports whether any attach woke the cache.
func (s *redisSession) run(ctx context.Context) (woke bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.detach()
	go s.idleLoop(ctx)
	r := bufio.NewReaderSize(s.client, respMaxLine)
	for {
		cmd, err := respReadCommandHead(r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				err = nil
			}
			return s.wokeAny(), err
		}
		done, err := s.handle(ctx, r, cmd)
		if err != nil || done {
			return s.wokeAny(), err
		}
	}
}

func (s *redisSession) wokeAny() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.woke
}

// handle serves one command. done reports that the client asked to leave.
func (s *redisSession) handle(ctx context.Context, r *bufio.Reader, cmd respCommand) (done bool, err error) {
	if cmd.name == "" {
		return false, nil
	}
	if redisDeniedCommands[cmd.name] {
		if cmd.rest > 0 {
			if err := cmd.skipRest(r); err != nil {
				return false, err
			}
		}
		return false, s.writeOwnReply(ctx, []byte("-ERR "+cmd.name+" is not allowed on a managed cache\r\n"))
	}
	if redisKeptCommands[cmd.name] {
		if err := cmd.readRest(r); err != nil {
			return false, err
		}
	}
	s.mu.Lock()
	attached := s.up != nil
	s.mu.Unlock()
	if !attached {
		if reply, local, quit := s.localReply(cmd); local {
			if cmd.rest > 0 {
				if err := cmd.skipRest(r); err != nil {
					return false, err
				}
			}
			return quit, s.writeOwnReply(ctx, reply)
		}
		if err := s.attach(ctx); err != nil {
			if cmd.rest > 0 {
				if err := cmd.skipRest(r); err != nil {
					return false, err
				}
			}
			return false, s.writeOwnReply(ctx, []byte(redisUnavailableReply))
		}
	}
	s.remember(cmd)

	s.mu.Lock()
	up := s.up
	s.busy = true
	if !redisHousekeeping[cmd.name] {
		s.lastData = s.now()
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()
	if up == nil {
		return false, errors.New("redis session: detached during command")
	}
	s.pending.Add(1)
	s.mu.Lock()
	s.sending = true
	s.mu.Unlock()
	_, writeErr := up.Write(cmd.raw)
	s.mu.Lock()
	s.sending = false
	lost := s.lostWhileSending
	s.lostWhileSending = false
	stateful := s.pinned || s.multi || s.watch
	s.mu.Unlock()
	if writeErr == nil && lost && (s.pending.Load() != 0 || stateful) {
		// The command reached a leg that then ended before answering.
		_ = s.client.Close()
		return false, errors.New("redis session: cache leg ended before answering")
	}
	if writeErr != nil {
		// The leg died before the command reached the cache: answer it and
		// let the next command attach again.
		s.pending.Add(-1)
		s.dropLeg(up)
		if cmd.rest > 0 {
			if err := cmd.skipRest(r); err != nil {
				return false, err
			}
		}
		return false, s.writeOwnReply(ctx, []byte(redisUnavailableReply))
	}
	if cmd.rest > 0 {
		if err := cmd.copyRest(r, up); err != nil {
			return false, err
		}
	}
	return cmd.name == "QUIT", nil
}

// localReply answers the commands a detached session can answer itself.
func (s *redisSession) localReply(cmd respCommand) (reply []byte, local, quit bool) {
	switch cmd.name {
	case "PING":
		if len(cmd.args) > 1 {
			return []byte("$" + strconv.Itoa(len(cmd.args[1])) + "\r\n" + string(cmd.args[1]) + "\r\n"), true, false
		}
		return []byte("+PONG\r\n"), true, false
	case "QUIT":
		return []byte("+OK\r\n"), true, true
	case "RESET":
		s.mu.Lock()
		s.hello, s.auth, s.selDB, s.name = nil, nil, nil, nil
		s.multi, s.watch = false, false
		s.mu.Unlock()
		return []byte("+RESET\r\n"), true, false
	}
	return nil, false, false
}

// remember records the state a re-attach must replay and the pinned states.
func (s *redisSession) remember(cmd respCommand) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch cmd.name {
	case "HELLO":
		s.hello = append([]byte(nil), cmd.raw...)
	case "AUTH":
		s.auth = append([]byte(nil), cmd.raw...)
	case "SELECT":
		s.selDB = append([]byte(nil), cmd.raw...)
	case "CLIENT":
		switch cmd.arg(1) {
		case "SETNAME":
			s.name = append([]byte(nil), cmd.raw...)
		case "TRACKING":
			if cmd.arg(2) == "ON" {
				s.pinned = true
			}
		}
	case "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE":
		s.pinned = true
	case "MULTI":
		s.multi = true
	case "WATCH":
		s.watch = true
	case "EXEC", "DISCARD":
		s.multi, s.watch = false, false
	case "UNWATCH":
		s.watch = false
	case "RESET":
		s.hello, s.auth, s.selDB, s.name = nil, nil, nil, nil
		s.multi, s.watch = false, false
	}
}

// attach opens an upstream leg and replays the kept state.
func (s *redisSession) attach(ctx context.Context) error {
	local, remote := net.Pipe()
	legDone := make(chan struct{})
	ready := make(chan error, 1)
	go func() {
		defer close(legDone)
		woke, err := s.attachFn(ctx, remote)
		if woke {
			s.mu.Lock()
			s.woke = true
			s.mu.Unlock()
		}
		select {
		case ready <- err:
		default:
		}
		_ = local.Close()
		_ = remote.Close()
	}()

	s.mu.Lock()
	replay := make([][]byte, 0, 4)
	for _, c := range [][]byte{s.hello, s.auth, s.selDB, s.name} {
		if c != nil {
			replay = append(replay, c)
		}
	}
	s.mu.Unlock()

	// HELLO carries AUTH itself; AUTH is replayed too only when the client
	// sent it separately, which is harmless either way.
	s.swallow.Store(int64(len(replay)))
	s.pending.Store(0)
	go s.pump(local, legDone)
	for _, c := range replay {
		if _, err := local.Write(c); err != nil {
			select {
			case legErr := <-ready:
				if legErr != nil {
					return legErr
				}
			default:
			}
			return err
		}
	}
	s.mu.Lock()
	s.up = local
	s.upDone = legDone
	s.attaches++
	s.lastData = s.now()
	s.mu.Unlock()
	return nil
}

// pump copies replies from one upstream leg to the client.
func (s *redisSession) pump(up net.Conn, legDone chan struct{}) {
	r := bufio.NewReaderSize(up, respMaxLine)
	for {
		var dst io.Writer = &lockedWriter{mu: &s.clientMu, w: s.client}
		dropping := s.swallow.Load() > 0
		if dropping {
			dst = io.Discard
		}
		kind, err := respCopyValue(r, dst)
		if err != nil {
			s.legEnded(up)
			return
		}
		if kind == '>' || kind == '|' {
			continue // push and attribute frames answer no command
		}
		if dropping {
			s.swallow.Add(-1)
			continue
		}
		if s.pending.Add(-1) == 0 {
			s.signalDrained()
		}
	}
}

// legEnded handles an upstream leg that closed. With replies outstanding
// the client cannot be answered, so the client connection is closed; with
// none, the session simply becomes detached.
func (s *redisSession) legEnded(up net.Conn) {
	s.mu.Lock()
	current := s.up == up
	stateful := s.pinned || s.multi || s.watch
	if current {
		s.up = nil
		s.upDone = nil
	}
	// A leg that fails while a command is being written to it (a cache that
	// cannot be woken ends the leg at once) owes no reply yet: the writer
	// answers the command itself, or closes the client if it got through.
	// Closing here left the client with EOF instead of the unavailable
	// error (flaky TestRedisSessionUnavailableCacheAnswersAnError).
	deferToWriter := current && s.sending
	if deferToWriter {
		s.lostWhileSending = true
	}
	s.mu.Unlock()
	if current && !deferToWriter && (s.pending.Load() != 0 || stateful) {
		_ = s.client.Close()
	}
	s.signalDrained()
}

func (s *redisSession) signalDrained() {
	select {
	case s.drained <- struct{}{}:
	default:
	}
}

// writeOwnReply writes a reply the session makes itself (a refusal, a local
// PING, an unavailable error). Redis answers a pipeline in order, so it first
// waits until the cache has answered every command already sent upstream, or
// that leg is gone. Writing it at once put a refused CONFIG's error ahead of
// the replies to the SET and GET pipelined before it (native run
// redis-20261117T171321).
func (s *redisSession) writeOwnReply(ctx context.Context, b []byte) error {
	for s.pending.Load() > 0 {
		s.mu.Lock()
		done := s.upDone
		s.mu.Unlock()
		if done == nil {
			break
		}
		select {
		case <-s.drained:
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.writeClient(b)
}

// dropLeg forgets a leg the client goroutine found dead.
func (s *redisSession) dropLeg(up net.Conn) {
	s.mu.Lock()
	if s.up == up {
		s.up, s.upDone = nil, nil
	}
	s.mu.Unlock()
	_ = up.Close()
}

// idleLoop detaches the session once it has been quiet long enough.
func (s *redisSession) idleLoop(ctx context.Context) {
	tick := s.detachAfter / 4
	if tick <= 0 {
		tick = time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.maybeDetach()
		}
	}
}

func (s *redisSession) maybeDetach() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.up == nil || s.busy || s.pinned || s.multi || s.watch {
		return false
	}
	if s.pending.Load() != 0 || s.swallow.Load() != 0 {
		return false
	}
	if s.now().Sub(s.lastData) < s.detachAfter {
		return false
	}
	up := s.up
	s.up = nil
	s.upDone = nil
	_ = up.Close()
	return true
}

// detach closes the current leg, if any.
func (s *redisSession) detach() {
	s.mu.Lock()
	up, done := s.up, s.upDone
	s.up, s.upDone = nil, nil
	s.mu.Unlock()
	if up != nil {
		_ = up.Close()
		<-done
	}
}

func (s *redisSession) writeClient(b []byte) error {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	_, err := s.client.Write(b)
	return err
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// redisUnavailableReply answers a command the session could not deliver.
// It names no internal cause.
const redisUnavailableReply = "-ERR cache unavailable, retry the command\r\n"
