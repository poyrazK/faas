//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
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
	ctx         context.Context
	log         *slog.Logger
	mgr         *fcvm.Manager
	signer      *workloadidentity.Signer
	dial        outboundRelayDialer
	now         func() time.Time
	deployments workloadIdentityDeployments

	warnMu               sync.Mutex
	lastUnconfiguredWarn time.Time
}

// UseDeployments names each app relay assertion's environment, so outboundd
// can keep stage instances off production's managed integrations.
func (r *OutboundRelayReceiver) UseDeployments(d workloadIdentityDeployments) {
	if r != nil {
		r.deployments = d
	}
}

// StartOutboundRelayReceiver binds the relay port. Without a signer or a
// target the port still accepts and answers an outbound_relay_unavailable 503,
// so apps fail fast with a reason instead of a dropped connection.
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

// outboundRelayUnconfiguredWarnEvery bounds the log line an unconfigured node
// writes for each relay attempt.
const outboundRelayUnconfiguredWarnEvery = time.Minute

func (r *OutboundRelayReceiver) warnUnconfigured(instance string) {
	now := r.now()
	r.warnMu.Lock()
	due := r.lastUnconfiguredWarn.IsZero() || now.Sub(r.lastUnconfiguredWarn) >= outboundRelayUnconfiguredWarnEvery
	if due {
		r.lastUnconfiguredWarn = now
	}
	r.warnMu.Unlock()
	if due {
		r.log.Warn("outbound relay: refused app connection; workload identity or outboundd target is not configured on this node",
			"instance", instance, "signer", r.signer != nil, "target", r.dial != nil)
	}
}

// writeOutboundRelayUnavailable answers the app's first HTTP request with an
// RFC 7807 503. It reads the request head first (bounded) so closing does not
// reset the connection over unread bytes and discard the response.
func writeOutboundRelayUnavailable(conn net.Conn, identityMissing bool) {
	_ = conn.SetDeadline(time.Now().Add(outboundRelayUnavailableDeadline))
	head := make([]byte, 0, 1024)
	buf := make([]byte, 1024)
	for len(head) < outboundRelayUnavailableMaxHead && !bytes.Contains(head, []byte("\r\n\r\n")) {
		n, err := conn.Read(buf)
		head = append(head, buf[:n]...)
		if err != nil {
			break
		}
	}
	detail := "Managed outbound integrations are not available on this node: its workload identity is not configured."
	if !identityMissing {
		detail = "Managed outbound integrations are not available on this node: no outbound gateway is configured."
	}
	body := `{"type":"about:blank","title":"Outbound integrations unavailable","status":503,"code":"outbound_relay_unavailable","detail":"` + detail + `"}`
	_, _ = io.WriteString(conn, "HTTP/1.1 503 Service Unavailable\r\nContent-Type: application/problem+json\r\nConnection: close\r\nContent-Length: "+
		strconv.Itoa(len(body))+"\r\n\r\n"+body)
}

const (
	outboundRelayUnavailableDeadline = 2 * time.Second
	outboundRelayUnavailableMaxHead  = 16 << 10
)

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
		// Not a transport failure: this node has no workload-identity key or
		// outboundd target. Tell the app instead of dropping the connection,
		// so the failure is visible to the customer and in vmmd's log.
		r.warnUnconfigured(instance)
		writeOutboundRelayUnavailable(guest, r.signer == nil)
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
	// ADR-20261027090012: an agent run whose model-wait pause is on reports
	// its relay bytes, and outboundd's bytes resume a paused VM before they
	// are written to it.
	if relay := r.mgr.AgentRelayOpened(instance); relay != nil {
		defer relay.Close()
		spliceOutboundRelayReaders(guest, upstream, relay.Reader(r.ctx, guest, false), relay.Reader(r.ctx, upstream, true))
		return "", nil
	}
	spliceOutboundRelay(guest, upstream)
	return "", nil
}

// relayPreamble identifies the instance as an agent run (ADR-20261027090010 §7), a
// Run with granted integrations (ADR-990) or an app (ADR-989). A Run
// without grants, an agent run without a relay principal, or any other
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
	if accountID, relay, err := r.mgr.AgentRunRelayIdentity(instance); err == nil {
		// ADR-20261027090010 §7: an agent run's relay names the run, its lease and its
		// tenant; outboundd checks the lease and the version's integrations
		// on every request.
		token, mintErr := r.signer.MintAgentRun(r.now(), workloadidentity.AgentRunClaims{AccountID: accountID, RunID: relay.RunID,
			LeaseToken: relay.LeaseToken, TenantID: relay.TenantID, AgentID: relay.AgentID, AgentVersion: relay.AgentVersion},
			workloadidentity.OutboundRelayAudience)
		if mintErr != nil {
			return relaywire.Preamble{}, fmt.Errorf("outbound relay: mint agent run assertion: %w", mintErr)
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
	environment, err := instanceEnvironment(r.ctx, r.mgr, r.deployments, instance)
	if err != nil {
		return relaywire.Preamble{}, fmt.Errorf("outbound relay: %w", err)
	}
	token, err := r.signer.MintEnvironment(r.now(), accountID, appID, instance, environment, workloadidentity.OutboundRelayAudience)
	if err != nil {
		return relaywire.Preamble{}, fmt.Errorf("outbound relay: mint assertion: %w", err)
	}
	return relaywire.Preamble{Assertion: token.AccessToken}, nil
}

// spliceOutboundRelay copies both directions and forwards half-closes. When
// either direction fails rather than reaching EOF, both connections close so
// the other copy cannot block forever.
func spliceOutboundRelay(guest, upstream net.Conn) {
	spliceOutboundRelayReaders(guest, upstream, guest, upstream)
}

// spliceOutboundRelayReaders is spliceOutboundRelay reading the guest's bytes
// from fromGuest and outboundd's from fromUpstream, readers that wrap the two
// connections.
func spliceOutboundRelayReaders(guest, upstream net.Conn, fromGuest, fromUpstream io.Reader) {
	done := make(chan struct{}, 2)
	pipe := func(dst net.Conn, src io.Reader) {
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
	go pipe(upstream, fromGuest)
	go pipe(guest, fromUpstream)
	<-done
	<-done
}
