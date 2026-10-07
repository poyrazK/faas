//go:build linux

// adr:644
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/healthcheckproto"
)

func TestImageHealthcheckMonitorDaemonRelayAndCancellation(t *testing.T) {
	for _, cancelInflight := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover declared failure", true: "park cancels inflight check"}[cancelInflight], func(t *testing.T) {
			_, sink, mgr := newTestLoop(t, "image", 3)
			root, err := os.MkdirTemp("", "image-monitor-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			socket := filepath.Join(root, "vsock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			entered := make(chan struct{})
			var attempts atomic.Int32
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					func() {
						defer func() { _ = conn.Close() }()
						line, err := bufio.NewReader(conn).ReadString('\n')
						if err != nil || line != "CONNECT 1028\n" {
							return
						}
						if _, err := conn.Write([]byte("OK 1\n")); err != nil {
							return
						}
						var header [8]byte
						if _, err := io.ReadFull(conn, header[:]); err != nil {
							return
						}
						var request healthcheckproto.Request
						if err := healthcheckproto.ReadBody(conn, binary.BigEndian.Uint32(header[4:]), &request); err != nil {
							return
						}
						if binary.BigEndian.Uint32(header[:4]) == healthcheckproto.ConfigProbe {
							_ = healthcheckproto.Write(conn, healthcheckproto.ConfigAck, healthcheckproto.Config{Nonce: request.Nonce, RuntimeID: "main", IntervalNS: int64(time.Millisecond), TimeoutNS: int64(time.Second), Retries: 3})
							return
						}
						attempt := attempts.Add(1)
						if cancelInflight {
							if attempt == 1 {
								close(entered)
							}
							_, _ = io.Copy(io.Discard, conn)
							return
						}
						_ = healthcheckproto.Write(conn, healthcheckproto.CheckAck, healthcheckproto.Response{Nonce: request.Nonce, RuntimeID: request.RuntimeID, Error: "unhealthy", NextIntervalNS: int64(time.Millisecond)})
					}()
				}
			}()
			ctx, stop := context.WithTimeout(t.Context(), 2*time.Second)
			defer stop()
			cancel := startLivenessLoopHelper(ctx, mgr, slog.Default(), "image", 1, "deployment", fcvm.LivenessProbeConfig{ImageHealthcheckRequired: true}, socket, nil)
			defer cancel()
			if cancelInflight {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("monitor never probed")
				}
				cancel()
				// The canceled connection must unwind without a terminal report.
				select {
				case <-time.After(30 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal("monitor failed to cancel")
				}
				if sink.count() != 0 {
					t.Fatal("park cancellation emitted recovery")
				}
			} else {
				deadline := time.NewTimer(time.Second)
				defer deadline.Stop()
				for sink.count() == 0 {
					select {
					case <-deadline.C:
						t.Fatal("command failure did not reach scheduler relay")
					case <-time.After(time.Millisecond):
					}
				}
				sink.mu.Lock()
				defer sink.mu.Unlock()
				if len(sink.calls) != 1 || sink.calls[0].reason != fcvm.LivenessReasonImageHealthcheck || attempts.Load() != 3 {
					t.Fatalf("relay=%+v attempts=%d", sink.calls, attempts.Load())
				}
			}
		})
	}
}
