package udpd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnabledListenerSource keeps the public edge's durable-state access read-only.
type EnabledListenerSource interface {
	ListEnabledUDPListeners(context.Context) ([]state.UDPListener, error)
}

// Supervisor reconciles immutable socket identities. Replacement cancels and
// joins all old peers before binding the new owner. Pool and rate budgets are
// shared across every public port, including across reconciliation churn.
type Supervisor struct {
	BindHost        string
	Source          EnabledListenerSource
	AllowedSources  []netip.Prefix
	ResolveTarget   func(context.Context, Route) (gateway.Target, error)
	Forwarder       Forwarder
	RefreshInterval time.Duration
	Pool            *PeerPool
	Rates           *RateLimits
	Metrics         *Metrics
	Listen          func(string, *net.UDPAddr) (*net.UDPConn, error)
	OnReady         func()
	// OnError may run concurrently with peer sessions.
	OnError func(error)
}

type supervisedSocket struct {
	identity state.UDPListener
	socket   *net.UDPConn
	cancel   context.CancelFunc
	done     chan error
}

func (entry *supervisedSocket) stop() {
	entry.cancel()
	_ = entry.socket.Close()
	<-entry.done
}

func (s *Supervisor) Serve(ctx context.Context) error {
	if s == nil || ctx == nil || s.Source == nil || s.ResolveTarget == nil || s.Forwarder == nil {
		return errors.New("UDP supervisor requires context, source, resolver and forwarder")
	}
	for _, prefix := range s.AllowedSources {
		if !prefix.IsValid() {
			return errors.New("invalid UDP source prefix")
		}
	}
	interval := s.RefreshInterval
	if interval <= 0 {
		interval = api.UDPListenerRefreshInterval
	}
	listen := s.Listen
	if listen == nil {
		listen = net.ListenUDP
	}
	host := s.BindHost
	if host == "" {
		host = "0.0.0.0"
	}
	pool := s.Pool
	if pool == nil {
		pool = NewPeerPool(0, 0)
	}
	rates := s.Rates
	if rates == nil {
		rates = NewRateLimits()
	}
	sockets := make(map[int]*supervisedSocket)
	defer func() {
		for _, entry := range sockets {
			entry.stop()
		}
	}()
	report := func(err error) {
		if s.OnError != nil {
			s.OnError(err)
		}
	}
	refresh := func() error {
		rows, err := s.Source.ListEnabledUDPListeners(ctx)
		if err != nil {
			return fmt.Errorf("list enabled UDP listeners: %w", err)
		}
		desired := make(map[int]state.UDPListener, len(rows))
		for _, row := range rows {
			if !row.Enabled || row.Protocol != "udp" || row.ID == "" || row.AppID == "" || row.AccountID == "" || row.ListenerName == "" || row.GuestPort < 1 || row.GuestPort > 65535 || row.PublicPort < api.UDPListenerPublicPortMin || row.PublicPort > api.UDPListenerPublicPortMax {
				return errors.New("invalid enabled UDP listener")
			}
			if _, exists := desired[row.PublicPort]; exists {
				return fmt.Errorf("duplicate UDP public port %d", row.PublicPort)
			}
			desired[row.PublicPort] = row
		}
		for port, entry := range sockets {
			row, keep := desired[port]
			// Timestamps do not affect routing. All identity fields do.
			same := keep && row.ID == entry.identity.ID && row.AppID == entry.identity.AppID && row.AccountID == entry.identity.AccountID && row.ListenerName == entry.identity.ListenerName && row.GuestPort == entry.identity.GuestPort
			if !same {
				entry.stop()
				delete(sockets, port)
				continue
			}
			select {
			case serveErr := <-entry.done:
				entry.cancel()
				_ = entry.socket.Close()
				delete(sockets, port)
				if serveErr != nil {
					report(fmt.Errorf("serve UDP port %d: %w", port, serveErr))
				}
			default:
			}
		}
		for port, row := range desired {
			if _, open := sockets[port]; open {
				continue
			}
			address, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(host, strconv.Itoa(port)))
			if err != nil {
				return err
			}
			socket, err := listen("udp4", address)
			if err != nil {
				return fmt.Errorf("bind UDP port %d: %w", port, err)
			}
			child, cancel := context.WithCancel(ctx)
			entry := &supervisedSocket{identity: row, socket: socket, cancel: cancel, done: make(chan error, 1)}
			sockets[port] = entry
			server := &Server{Socket: socket, Route: Route{ListenerID: row.ID, PublicPort: row.PublicPort, AppID: row.AppID, AccountID: row.AccountID, ListenerName: row.ListenerName, GuestPort: row.GuestPort}, AllowedSources: s.AllowedSources, Pool: pool, Rates: rates, ResolveTarget: s.ResolveTarget, Forwarder: s.Forwarder, OnError: s.OnError, Metrics: s.Metrics}
			go func() { entry.done <- server.Serve(child) }()
		}
		return nil
	}
	if err := refresh(); err != nil {
		s.Metrics.reconcileError()
		return err
	}
	if s.OnReady != nil {
		s.OnReady()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := refresh(); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				s.Metrics.reconcileError()
				report(err)
			}
		}
	}
}
