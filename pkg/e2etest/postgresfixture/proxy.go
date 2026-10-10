package postgresfixture

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Proxy forwards PostgreSQL bytes without terminating TLS. It exposes only the
// disposable test cluster, on an explicitly prepared test-host interface.
type Proxy struct {
	Host string
	Port uint16
}

func OpenProxy(t *testing.T, cluster *pgxpool.Pool, listenIP string, port uint16) Proxy {
	t.Helper()
	listener, err := net.Listen("tcp4", net.JoinHostPort(listenIP, strconv.Itoa(int(port))))
	if err != nil {
		t.Fatal("cannot listen for disposable SQL TLS proxy")
	}
	addr := listener.Addr().(*net.TCPAddr)
	host := cluster.Config().ConnConfig.Host
	if strings.HasPrefix(host, "/") {
		host = "127.0.0.1"
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(cluster.Config().ConnConfig.Port)))
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	connections := make(map[net.Conn]struct{})
	closed := false
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				_ = client.Close()
				return
			}
			connections[client] = struct{}{}
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = client.Close(); mu.Lock(); delete(connections, client); mu.Unlock() }()
				upstream, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", target)
				if err != nil {
					return
				}
				defer func() { _ = upstream.Close() }()
				_ = client.SetDeadline(time.Now().Add(30 * time.Second))
				_ = upstream.SetDeadline(time.Now().Add(30 * time.Second))
				done := make(chan struct{})
				go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); close(done) }()
				_, _ = io.Copy(client, upstream)
				_ = client.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		mu.Lock()
		closed = true
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return Proxy{Host: addr.IP.String(), Port: uint16(addr.Port)}
}
