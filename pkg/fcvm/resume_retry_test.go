package fcvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestResumeTransportRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   error
		succeed bool
		want    int
	}{
		{"eof then ack", io.EOF, true, 2},
		{"reset then ack", syscall.ECONNRESET, true, 2},
		{"persistent reset fails closed", io.EOF, false, 3},
		{"nack stays terminal", errors.New("resume hook failed (ack=2)"), true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retryResumeTransport(t.Context(), func(context.Context) error {
				calls++
				if calls > 1 && tc.succeed {
					return nil
				}
				return fmt.Errorf("read resume ack: %w", tc.first)
			})
			if calls != tc.want {
				t.Fatalf("calls=%d want=%d", calls, tc.want)
			}
			wantSuccess := tc.succeed && tc.want > 1
			if (err == nil) != wantSuccess {
				t.Fatalf("err=%v wantSuccess=%v", err, wantSuccess)
			}
		})
	}
}

func TestTriggerResumeHookReconnectsAfterLostAck(t *testing.T) {
	base := shortChrootBase(t, "reset")
	v := &JailerVMM{chrootBase: base, fcName: "f"}
	lease := Lease{Instance: "resume-reset"}
	socket := v.VsockUDSSocketPath(lease.Instance)
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	entropies := make(chan []byte, 2)
	go func() {
		for n := 0; n < 2; n++ {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			handleFakeVsockHook(t, c, ackOK, func(_ int64, entropy []byte) error {
				entropies <- append([]byte(nil), entropy...)
				if n == 0 {
					_ = c.Close()
				}
				return nil
			})
		}
	}()
	if err := v.TriggerResumeHook(t.Context(), lease, 123); err != nil {
		t.Fatal(err)
	}
	first, second := <-entropies, <-entropies
	if len(first) != resumeHookEntropyBytes || len(second) != resumeHookEntropyBytes || bytes.Equal(first, second) {
		t.Fatal("reconnect must supply fresh complete entropy")
	}
}
