package fcvm

// adr: 1661

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

type lostResourceRecoveryACK struct{ net.Conn }

func (c lostResourceRecoveryACK) Write(data []byte) (int, error) {
	if len(data) == 2 && data[0] == ackOK {
		_ = c.Close()
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(data)
}

// This is the real host framing over a local UDS, not AF_VSOCK/KVM evidence.
func TestFunctionResourceWarmRecoveryLostACKNeverReplaysHook(t *testing.T) {
	base := shortChrootBase(t, "resource-ack")
	v := NewJailerVMM(base, 0)
	lease := Lease{Instance: "resource-ack"}
	if err := os.MkdirAll(v.chrootRoot(lease.Instance), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(v.chrootRoot(lease.Instance), VsockUDSSocketName))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var calls atomic.Int32
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); _ = listener.Close(); <-done })
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			handleFakeVsockHook(t, lostResourceRecoveryACK{conn}, ackOK, func(int64, []byte) error { calls.Add(1); return nil })
			stop()
		}
	}()
	if err := v.RecoverFunctionResources(t.Context(), lease); !errors.Is(err, io.EOF) || calls.Load() != 1 {
		t.Fatalf("lost recovery ACK replayed or succeeded: calls=%d error=%v", calls.Load(), err)
	}
}
