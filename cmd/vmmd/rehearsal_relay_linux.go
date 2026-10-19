//go:build linux

// adr: 20261017201007
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

// Rehearsal relays use one recorded obligation and broker connection per HTTP
// request. Keeping a guest connection alive must not hide a second request or
// bypass the durable admission fence.
func (r *OutboundRelayReceiver) serveRehearsalRelay(session *rehearsalrecorder.Session, instance, app, account, run string, guest net.Conn) error {
	if r.signer == nil || r.dial == nil {
		session.Fail()
		return errors.New("rehearsal relay is not configured")
	}
	listener := &rehearsalGuestListener{conn: guest, done: make(chan struct{})}
	var active, handled atomic.Int64
	var failed atomic.Bool
	closed := make(chan struct{})
	var connection *rehearsalrecorder.Connection
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 5 * time.Second,
		MaxHeaderBytes: 16 << 10,
		ErrorLog:       log.New(io.Discard, "", 0),
		BaseContext:    func(net.Listener) context.Context { return r.ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			handled.Add(1)
			r.forwardRehearsalRequest(w, req, session, connection, instance, app, account, run)
		}),
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateActive {
				active.Add(1)
			}
			if state == http.StateClosed {
				if active.Load() != handled.Load() {
					failed.Store(true)
				}
				close(closed)
				_ = listener.Close()
			}
		},
	}
	var err error
	connection, err = session.Attach(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			failed.Store(true)
			_ = server.Close()
		}
	})
	if err != nil {
		return err
	}
	defer func() { connection.Detach(failed.Load()) }()
	err = server.Serve(listener)
	// Shutdown may return Serve before its active handler has left. Join the
	// connection's final state before letting the recorder acknowledge closing.
	if !listener.accepted.Load() {
		_ = guest.Close()
		return err
	}
	<-closed
	if errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (r *OutboundRelayReceiver) forwardRehearsalRequest(w http.ResponseWriter, req *http.Request, session *rehearsalrecorder.Session, connection *rehearsalrecorder.Connection, instance, app, account, run string) {
	ctx, cancel := context.WithTimeout(req.Context(), 35*time.Second)
	defer cancel()
	requestID, err := session.Begin(ctx)
	if err != nil {
		http.Error(w, "rehearsal recorder unavailable or closing", http.StatusServiceUnavailable)
		return
	}
	if req.URL.IsAbs() || req.Method == http.MethodConnect || req.Header.Get("Upgrade") != "" {
		http.Error(w, "unsupported rehearsal relay request", http.StatusBadRequest)
		return // Its unacknowledged obligation prevents a complete result.
	}
	token, err := r.signer.MintRehearsalRelay(r.now(), account, app, instance, run)
	if err != nil {
		http.Error(w, "rehearsal identity unavailable", http.StatusServiceUnavailable)
		return
	}
	transport := &http.Transport{
		DisableKeepAlives: true, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := r.dial(ctx)
			if err != nil {
				return nil, err
			}
			if err = conn.SetWriteDeadline(time.Now().Add(outboundRelayDialTimeout)); err == nil {
				err = relaywire.Write(conn, relaywire.Preamble{Assertion: token.BearerValue(), RecorderRequestID: requestID})
			}
			if err == nil {
				err = conn.SetWriteDeadline(time.Time{})
			}
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		},
	}
	defer transport.CloseIdleConnections()
	out := req.Clone(ctx)
	out.RequestURI, out.URL.Scheme, out.URL.Host = "", "http", "rehearsal.invalid"
	out.Body = http.MaxBytesReader(w, req.Body, api.MaxScenarioSimulationRequestBytes)
	response, err := transport.RoundTrip(out)
	if err != nil {
		// An injected timeout/disconnect may already have completed durably.
		// A broker/storage failure instead leaves the obligation unacknowledged.
		panic(http.ErrAbortHandler)
	}
	defer func() { _ = response.Body.Close() }()
	if fault := response.Header.Get("X-Gregale-Rehearsal-Worker-Fault"); fault != "" {
		// The broker has completed this obligation. Hold its bounded response
		// locally so the same vmmd owner can prove the guest was at the checkpoint.
		if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 8193)); err != nil {
			session.Fail()
			panic(http.ErrAbortHandler)
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(35 * time.Second))
		_ = session.HoldWorkerCheckpoint(ctx, fault)
		panic(http.ErrAbortHandler)
	}
	if receipt := response.Header.Get("X-Gregale-Rehearsal-Suspend"); receipt != "" {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 8193))
		if readErr != nil || len(body) > 8192 {
			session.Fail()
			panic(http.ErrAbortHandler)
		}
		// Only the cooperative hold has the extended host deadline. The broker
		// round trip and application's dispatch deadline retain their own bounds.
		holdCtx, done := context.WithTimeout(req.Context(), 170*time.Second)
		defer done()
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(170 * time.Second))
		if err := session.HoldSuspendCheckpoint(holdCtx, receipt, connection); err != nil {
			panic(http.ErrAbortHandler)
		}
		response.Header.Del("X-Gregale-Rehearsal-Suspend")
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
	}
	for name, values := range response.Header {
		if name == "Connection" || name == "Keep-Alive" || name == "Transfer-Encoding" {
			continue
		}
		w.Header()[name] = values
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

type rehearsalGuestListener struct {
	conn     net.Conn
	accepted atomic.Bool
	done     chan struct{}
	once     sync.Once
}

func (l *rehearsalGuestListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	default:
	}
	if l.accepted.CompareAndSwap(false, true) {
		return l.conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}
func (l *rehearsalGuestListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *rehearsalGuestListener) Addr() net.Addr { return l.conn.LocalAddr() }
