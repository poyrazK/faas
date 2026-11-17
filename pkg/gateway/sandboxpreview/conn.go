package sandboxpreview

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// errPortClosed is a guest port nothing listens on.
var errPortClosed = errors.New("sandboxpreview: nothing listens on the guest port")

// errGuest is any other guest refusal of port_connect.
var errGuest = errors.New("sandboxpreview: the guest refused the connection")

// streamConn is one TCP connection to a guest port, carried by a SandboxCall
// port_connect stream: what is written goes as data frames, what the guest
// port sends comes back as data frames, and the call's result is the end of
// the connection.
type streamConn struct {
	stream  vmmdpb.Vmmd_SandboxCallClient
	cancel  context.CancelFunc
	onClose func()

	rbuf []byte
	rerr error

	wmu       sync.Mutex
	wclosed   bool
	closeOnce sync.Once
}

func (c *streamConn) Read(p []byte) (int, error) {
	for len(c.rbuf) == 0 {
		if c.rerr != nil {
			return 0, c.rerr
		}
		ev, err := c.stream.Recv()
		if err != nil {
			c.rerr = io.EOF
			if !errors.Is(err, io.EOF) {
				c.rerr = io.ErrUnexpectedEOF
			}
			continue
		}
		f := ev.GetFrame()
		switch f.GetType() {
		case sandboxproto.FrameData:
			c.rbuf = f.GetBody()
		case sandboxproto.FrameResult:
			c.rerr = io.EOF
		case sandboxproto.FrameError:
			var e sandboxproto.ErrorFrame
			c.rerr = errGuest
			if sandboxproto.DecodeStrict(f.GetBody(), &e) == nil && e.Code == sandboxproto.CodePortClosed {
				c.rerr = errPortClosed
			}
		}
	}
	n := copy(p, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
}

func (c *streamConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.wclosed {
		return 0, net.ErrClosed
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), sandboxproto.ChunkBytes)
		frame := &vmmdpb.SandboxFrame{Type: sandboxproto.FrameData, Body: append([]byte(nil), p[:n]...)}
		if err := c.stream.Send(&vmmdpb.SandboxCallRequest{Message: &vmmdpb.SandboxCallRequest_Frame{Frame: frame}}); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// CloseWrite ends what is sent to the guest port; the guest half-closes its
// side of the TCP connection.
func (c *streamConn) CloseWrite() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.wclosed {
		return nil
	}
	c.wclosed = true
	end := &vmmdpb.SandboxFrame{Type: sandboxproto.FrameDataEnd}
	err := c.stream.Send(&vmmdpb.SandboxCallRequest{Message: &vmmdpb.SandboxCallRequest_Frame{Frame: end}})
	_ = c.stream.CloseSend()
	return err
}

// Close ends the call, which closes the guest's TCP connection.
func (c *streamConn) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		if c.onClose != nil {
			c.onClose()
		}
	})
	return nil
}

func (c *streamConn) LocalAddr() net.Addr  { return previewAddr{} }
func (c *streamConn) RemoteAddr() net.Addr { return previewAddr{} }

// Deadlines are not supported: the request's context and the call's
// cancellation bound the connection instead.
func (c *streamConn) SetDeadline(time.Time) error      { return nil }
func (c *streamConn) SetReadDeadline(time.Time) error  { return nil }
func (c *streamConn) SetWriteDeadline(time.Time) error { return nil }

type previewAddr struct{}

func (previewAddr) Network() string { return "sandbox-port" }
func (previewAddr) String() string  { return "sandbox-port" }
