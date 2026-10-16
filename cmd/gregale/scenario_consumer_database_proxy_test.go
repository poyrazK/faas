//go:build !no_pg && !windows

package main

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// This proxy only controls the API fixture's SQL connections. The parent SQL
// oracle and PostgreSQL cluster remain reachable and unchanged.
type consumerAcceptanceDatabaseProxy struct {
	listener        net.Listener
	network, target string
	mu              sync.Mutex
	online          bool
	connections     map[net.Conn]bool
	workers         sync.WaitGroup
	done            chan struct{}
}

func startConsumerAcceptanceDatabaseProxy(t *testing.T, cfg *pgxpool.Config) *consumerAcceptanceDatabaseProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &consumerAcceptanceDatabaseProxy{listener: listener, network: "tcp", target: net.JoinHostPort(cfg.ConnConfig.Host, strconv.Itoa(int(cfg.ConnConfig.Port))), online: true, connections: map[net.Conn]bool{}, done: make(chan struct{})}
	if strings.HasPrefix(cfg.ConnConfig.Host, "/") {
		p.network = "unix"
		p.target = filepath.Join(cfg.ConnConfig.Host, ".s.PGSQL."+strconv.Itoa(int(cfg.ConnConfig.Port)))
	}
	go func() {
		defer close(p.done)
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			p.workers.Add(1)
			go p.forward(c)
		}
	}()
	t.Cleanup(func() { listener.Close(); <-p.done; p.setOnline(false); p.workers.Wait() })
	return p
}
func (p *consumerAcceptanceDatabaseProxy) setOnline(online bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.online = online
	if !online {
		for c := range p.connections {
			c.Close()
		}
	}
}
func (p *consumerAcceptanceDatabaseProxy) forward(client net.Conn) {
	defer p.workers.Done()
	defer client.Close()
	p.mu.Lock()
	online := p.online
	if online {
		p.connections[client] = true
	}
	p.mu.Unlock()
	if !online {
		return
	}
	defer func() { p.mu.Lock(); delete(p.connections, client); p.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, err := (&net.Dialer{}).DialContext(ctx, p.network, p.target)
	if err != nil {
		return
	}
	defer server.Close()
	p.mu.Lock()
	online = p.online
	if online {
		p.connections[server] = true
	}
	p.mu.Unlock()
	if !online {
		return
	}
	defer func() { p.mu.Lock(); delete(p.connections, server); p.mu.Unlock() }()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(server, client); server.Close(); close(done) }()
	_, _ = io.Copy(client, server)
	client.Close()
	server.Close()
	<-done
}
