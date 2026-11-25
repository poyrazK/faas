// adr: 20261027090013 §11 — a port nothing listens on answers as a closed
// port even when the request write loses the race to the guest's error frame.
package sandboxpreview

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// endedCall is a port_connect call the guest already ended: Send finds it
// over, and Recv returns the guest's error frame.
type endedCall struct {
	grpc.ClientStream
	frames chan *vmmdpb.SandboxCallEvent
}

func (c *endedCall) Send(*vmmdpb.SandboxCallRequest) error { return io.EOF }

func (c *endedCall) Recv() (*vmmdpb.SandboxCallEvent, error) {
	if ev, ok := <-c.frames; ok {
		return ev, nil
	}
	return nil, io.EOF
}

func TestWriteAfterTheGuestEndedTheCallReportsItsResult(t *testing.T) {
	body, err := json.Marshal(sandboxproto.ErrorFrame{Code: sandboxproto.CodePortClosed})
	if err != nil {
		t.Fatal(err)
	}
	call := &endedCall{frames: make(chan *vmmdpb.SandboxCallEvent, 1)}
	call.frames <- &vmmdpb.SandboxCallEvent{Frame: &vmmdpb.SandboxFrame{Type: sandboxproto.FrameError, Body: body}}
	close(call.frames)
	c := &streamConn{stream: call, cancel: func() {}, ended: make(chan struct{})}
	read := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 1))
		read <- err
	}()
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\n\r\n")); !errors.Is(err, errPortClosed) {
		t.Fatalf("write = %v, want the closed port", err)
	}
	if err := <-read; !errors.Is(err, errPortClosed) {
		t.Fatalf("read = %v", err)
	}
}
