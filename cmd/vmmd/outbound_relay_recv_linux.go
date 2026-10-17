//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// VsockOutboundRelayHostPort is the host endpoint guest-init's loopback
// outbound relay connects to (ADR-989).
const VsockOutboundRelayHostPort uint32 = fcvm.VsockOutboundRelayHostPort

// OutboundRelayReceiver splices an app's relay connection to outboundd. The
// per-instance listener identifies the app; vmmd never parses the bytes.
type OutboundRelayReceiver struct {
	ctx    context.Context
	log    *slog.Logger
	mgr    *fcvm.Manager
	signer *workloadidentity.Signer
	dial   outboundRelayDialer
	now    func() time.Time
}

// StartOutboundRelayReceiver binds the relay port. Without a signer or a
// target the port still accepts and immediately closes, so apps fail fast.
func StartOutboundRelayReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, signer *workloadidentity.Signer, dial outboundRelayDialer, jailer *fcvm.JailerVMM) (*OutboundRelayReceiver, error) {
	if ctx == nil || jailer == nil {
		return nil, errors.New("outbound relay vsock: context and jailer are required")
	}
	if log == nil {
		log = slog.Default()
	}
	r := &OutboundRelayReceiver{ctx: ctx, log: log, mgr: mgr, signer: signer, dial: dial, now: time.Now}
	if err := jailer.RegisterGuestVsockStreamHandler(VsockOutboundRelayHostPort, r.handleGuestStream); err != nil {
		return nil, fmt.Errorf("outbound relay receiver register port %d: %w", VsockOutboundRelayHostPort, err)
	}
	log.Info("outbound relay receiver registered", "vsock_host_port", VsockOutboundRelayHostPort,
		"enabled", signer != nil && dial != nil)
	return r, nil
}

func (r *OutboundRelayReceiver) Close() {}

func (r *OutboundRelayReceiver) handleGuestStream(instance string, guest net.Conn) (string, error) {
	if r.mgr == nil {
		return "", nil
	}
	app, account, run, err := r.mgr.InstanceRehearsalRelayIdentity(r.ctx, instance)
	if err != nil {
		if session, sessionErr := r.mgr.InstanceRehearsalRecorder(instance); sessionErr == nil {
			session.Fail()
		}
		return "protocol", err
	}
	if run != "" {
		session, err := r.mgr.InstanceRehearsalRecorder(instance)
		if err != nil {
			return "protocol", err
		}
		return "", r.serveRehearsalRelay(session, instance, app, account, run, guest)
	}
	if r.signer == nil || r.dial == nil {
		return "", nil
	}
	preamble, err := r.relayPreamble(instance)
	if err != nil {
		return "protocol", err
	}
	dialCtx, cancel := context.WithTimeout(r.ctx, outboundRelayDialTimeout)
	upstream, err := r.dial(dialCtx)
	cancel()
	if err != nil {
		return "write", fmt.Errorf("outbound relay: dial outboundd: %w", err)
	}
	defer func() { _ = upstream.Close() }()
	if err := relaywire.Write(upstream, preamble); err != nil {
		return "write", err
	}
	spliceOutboundRelay(guest, upstream)
	return "", nil
}

// relayPreamble identifies the instance as a Run with granted integrations
// (ADR-990) or as an app (ADR-989). A Run without grants, or any other
// instance, gets no relay.
func (r *OutboundRelayReceiver) relayPreamble(instance string) (relaywire.Preamble, error) {
	app, account, run, simErr := r.mgr.InstanceRehearsalRelayIdentity(r.ctx, instance)
	if simErr != nil {
		return relaywire.Preamble{}, simErr
	}
	if run != "" {
		token, err := r.signer.MintRehearsalRelay(r.now(), account, app, instance, run)
		if err != nil {
			return relaywire.Preamble{}, err
		}
		return relaywire.Preamble{Assertion: token.BearerValue()}, nil
	}
	appID, accountID, err := r.mgr.InstanceExternalActionIdentity(r.ctx, instance)
	if err != nil {
		return relaywire.Preamble{}, err
	}
	if accountID, executionID, leaseToken, integrations, err := r.mgr.ExecutionRelayIdentity(instance); err == nil {
		token, mintErr := r.signer.MintExecution(r.now(), accountID, executionID, leaseToken, workloadidentity.OutboundRelayAudience)
		if mintErr != nil {
			return relaywire.Preamble{}, fmt.Errorf("outbound relay: mint execution assertion: %w", mintErr)
		}
		return relaywire.Preamble{Assertion: token.BearerValue(), ExecutionIntegrations: integrations}, nil
	}
	if appID == "" || accountID == "" {
		return relaywire.Preamble{}, fmt.Errorf("outbound relay: instance identity unavailable")
	}
	token, err := r.signer.Mint(r.now(), accountID, appID, instance, workloadidentity.OutboundRelayAudience)
	if err != nil {
		return relaywire.Preamble{}, fmt.Errorf("outbound relay: mint assertion: %w", err)
	}
	return relaywire.Preamble{Assertion: token.AccessToken}, nil
}

// spliceOutboundRelay copies both directions and forwards half-closes. When
// either direction fails rather than reaching EOF, both connections close so
// the other copy cannot block forever.
func spliceOutboundRelay(guest, upstream net.Conn) {
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		defer func() { done <- struct{}{} }()
		if _, err := io.Copy(dst, src); err != nil {
			_ = guest.Close()
			_ = upstream.Close()
			return
		}
		if half, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		}
	}
	go pipe(upstream, guest)
	go pipe(guest, upstream)
	<-done
	<-done
}
