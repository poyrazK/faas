// adr: 1027
package dbarchive

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

type acceptedListener struct {
	net.Listener
	accepted chan struct{}
}

func (l acceptedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		close(l.accepted)
	}
	return c, err
}

func TestArchiveServerShutdownClosesActiveConnections(t *testing.T) {
	for _, phase := range []string{"before-header", "idle", "partial-wal"} {
		for _, stopListener := range []bool{false, true} {
			label := "cancel"
			if stopListener {
				label = "listener-close"
			}
			t.Run(phase+"/"+label, func(t *testing.T) {
				ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "archive.sock"))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				accepted := make(chan struct{})
				server := &Server{Store: newStore(t)}
				go func() { defer close(done); _ = server.Serve(ctx, acceptedListener{ln, accepted}) }()
				conn, err := net.Dial("unix", ln.Addr().String())
				if err != nil {
					cancel()
					_ = ln.Close()
					t.Fatal(err)
				}
				t.Cleanup(func() { cancel(); _ = conn.Close(); _ = ln.Close(); <-done })
				<-accepted
				if phase != "before-header" {
					if err := WriteLine(conn, Header{DatabaseID: dbA}); err != nil {
						t.Fatal(err)
					}
				}
				if phase == "partial-wal" {
					if err := WriteLine(conn, Request{Op: OpPutWAL, Name: seg, Size: 1024}); err != nil {
						t.Fatal(err)
					}
				}
				if stopListener {
					_ = ln.Close()
				} else {
					cancel()
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("shutdown waited on an active client")
				}
			})
		}
	}
}
