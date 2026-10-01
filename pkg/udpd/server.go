package udpd

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Route struct {
	AppID, AccountID, ListenerName string
	ListenerID                     string
	PublicPort                     int
	GuestPort                      int
}
type Forwarder interface {
	ServePeer(context.Context, gateway.DatagramPeer, gateway.Target) error
}

// Server owns one immutable app-listener socket. A supervisor must cancel it
// on intent deletion or reassignment. Empty source allowlists expose no traffic.
type Server struct {
	Socket         *net.UDPConn
	Route          Route
	AllowedSources []netip.Prefix
	Pool           *PeerPool
	Rates          *RateLimits
	Metrics        *Metrics
	ResolveTarget  func(context.Context, Route) (gateway.Target, error)
	Forwarder      Forwarder
	// OnError may be invoked concurrently by independent peer sessions.
	OnError func(error)
}

func (s *Server) Serve(parent context.Context) error {
	if s == nil || parent == nil || s.Socket == nil || s.Socket.RemoteAddr() != nil || s.ResolveTarget == nil || s.Forwarder == nil {
		return errors.New("UDP server requires context, listening socket, resolver and forwarder")
	}
	if s.Route.AppID == "" || s.Route.AccountID == "" || s.Route.ListenerName == "" || s.Route.ListenerID == "" || s.Route.PublicPort < api.UDPListenerPublicPortMin || s.Route.PublicPort > api.UDPListenerPublicPortMax || s.Route.GuestPort < 1 || s.Route.GuestPort > 65535 {
		return errors.New("UDP server requires an app-owned listener route")
	}
	for _, prefix := range s.AllowedSources {
		if !prefix.IsValid() {
			return errors.New("invalid UDP source prefix")
		}
	}
	pool := s.Pool
	if pool == nil {
		pool = NewPeerPool(0, 0)
	}
	rates := s.Rates
	if rates == nil {
		rates = NewRateLimits()
	}
	s.Metrics.listener(1)
	defer s.Metrics.listener(-1)
	ctx, cancel := context.WithCancel(parent)
	replies := make(chan Reply, api.UDPReplyQueueDepth)
	var wg sync.WaitGroup
	var mu sync.Mutex
	peers := make(map[netip.AddrPort]*Peer)
	wg.Add(1)
	go func() { defer wg.Done(); <-ctx.Done(); _ = s.Socket.Close() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case reply := <-replies:
				if reply.Context.Err() != nil {
					s.Metrics.drop("stale_reply")
					continue
				}
				if !rates.Allow(s.Route.AccountID, len(reply.Payload), true) {
					s.Metrics.drop("outbound_rate")
					continue
				}
				if err := s.Socket.SetWriteDeadline(time.Now().Add(api.UDPWriteTimeout)); err != nil {
					s.Metrics.drop("write_error")
					s.report(err)
					continue
				}
				_, _, err := s.Socket.WriteMsgUDPAddrPort(reply.Payload, nil, reply.Address)
				if err != nil {
					s.Metrics.drop("write_error")
					if ctx.Err() == nil {
						s.report(err)
					}
				} else {
					s.Metrics.packet(len(reply.Payload), true)
				}
			}
		}
	}()
	defer func() { cancel(); _ = s.Socket.Close(); wg.Wait() }()
	buffer := make([]byte, api.UDPDatagramMaxBytes)
	for {
		n, _, flags, address, err := s.Socket.ReadMsgUDPAddrPort(buffer, nil)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if flags&syscall.MSG_TRUNC != 0 {
			s.Metrics.drop("truncated")
			continue
		}
		if !s.allowed(address.Addr()) {
			s.Metrics.drop("source_denied")
			continue
		}
		if !rates.Allow(s.Route.AccountID, n, false) {
			s.Metrics.drop("inbound_rate")
			continue
		}
		mu.Lock()
		peer := peers[address]
		if peer == nil {
			release, ok := pool.Acquire(s.Route.AccountID)
			if !ok {
				s.Metrics.drop("peer_limit")
				mu.Unlock()
				continue
			}
			peer, err = NewPeer(ctx, address, replies)
			if err != nil {
				release()
				mu.Unlock()
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			peers[address] = peer
			wg.Add(1)
			go func(peerCtx context.Context, peer *Peer, address netip.AddrPort) { //nolint:contextcheck // NewPeer derives peerCtx from the listener context and adds per-peer cancellation.
				start := s.Metrics.beginPeer()
				outcome := "admission_error"
				defer wg.Done()
				defer release()
				defer func() {
					mu.Lock()
					if peers[address] == peer {
						delete(peers, address)
					}
					mu.Unlock()
				}()
				defer peer.Close()
				defer func() {
					if peerCtx.Err() != nil {
						outcome = "canceled"
					}
					s.Metrics.endPeer(start, outcome)
				}()
				admitCtx, admitCancel := context.WithTimeout(peerCtx, api.UDPAdmissionTimeout)
				target, err := s.ResolveTarget(admitCtx, s.Route)
				if err == nil {
					err = admitCtx.Err()
				}
				admitCancel()
				if err == nil && (target.AppID != s.Route.AppID || target.InstanceID == "" || target.NodeID == "") {
					err = errors.New("UDP admission requires the listener app and a live instance/node identity")
				}
				if err == nil {
					target.AppID = s.Route.AppID
					target.Port = s.Route.GuestPort
					err = s.Forwarder.ServePeer(peerCtx, peer, target)
					outcome = "success"
					if err != nil {
						outcome = "forward_error"
					}
					if status.Code(err) == codes.ResourceExhausted {
						outcome = "resource_exhausted"
					}
					if status.Code(err) == codes.DeadlineExceeded {
						outcome = "idle_timeout"
					}
				}
				if err != nil && peerCtx.Err() == nil {
					s.report(err)
				}
			}(peer.ctx, peer, address)
		}
		if peer.Enqueue(buffer[:n]) {
			s.Metrics.packet(n, false)
		} else {
			s.Metrics.drop("queue_full")
		}
		mu.Unlock()
	}
}
func (s *Server) allowed(address netip.Addr) bool {
	for _, prefix := range s.AllowedSources {
		if prefix.Contains(address.Unmap()) {
			return true
		}
	}
	return false
}
func (s *Server) report(err error) {
	if s.OnError != nil {
		s.OnError(err)
	}
}
