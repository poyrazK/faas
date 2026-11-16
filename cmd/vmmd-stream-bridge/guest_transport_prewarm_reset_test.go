package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// TestGuestH2CRecoversWhenGuestResetsPrewarmedConnection pins the prod outage
// of 2026-11-16: after a restore, guest-init resets every off-guest TCP
// connection, including the bridge's prewarmed H2C connection. That
// connection had carried only a Ping, and x/net/http2 keeps reporting
// CanTakeNewRequest for a never-used closed connection, so a bridge that
// lives as long as its instance (bridge units) answered every request with
// "client conn could not be established" (a 502) for hours.
func TestGuestH2CRecoversWhenGuestResetsPrewarmedConnection(t *testing.T) {
	for _, mode := range []string{"rst", "fin"} {
		t.Run(mode, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			var mu sync.Mutex
			var conns []net.Conn
			srv := &http2.Server{}
			go func() {
				for {
					c, acceptErr := ln.Accept()
					if acceptErr != nil {
						return
					}
					mu.Lock()
					conns = append(conns, c)
					mu.Unlock()
					go srv.ServeConn(c, &http2.ServeConnOpts{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						_, _ = io.WriteString(w, "ok")
					})})
				}
			}()
			_, portText, err := net.SplitHostPort(ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.ParseUint(portText, 10, 16)
			if err != nil {
				t.Fatal(err)
			}
			pool := newGuestTransportPool("127.0.0.1")
			defer pool.closeIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := pool.prewarmH2C(ctx, uint16(port)); err != nil {
				t.Fatalf("prewarmH2C: %v", err)
			}

			mu.Lock()
			prewarmed := conns[0]
			mu.Unlock()
			if mode == "rst" {
				_ = prewarmed.(*net.TCPConn).SetLinger(0)
			}
			_ = prewarmed.Close()
			time.Sleep(50 * time.Millisecond) // let the client read the reset

			for i := range 3 {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+portText+"/svc/Method", nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := pool.h2c(uint16(port)).RoundTrip(req)
				if err != nil {
					t.Fatalf("request %d after the guest reset the prewarmed connection: %v", i, err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("request %d status = %d, want 200", i, resp.StatusCode)
				}
			}
		})
	}
}
