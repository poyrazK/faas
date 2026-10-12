package pgproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"sync"

	"google.golang.org/grpc"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbcreds"
	"github.com/onebox-faas/faas/pkg/overlay"
)

// ScheddWaker wakes databases through schedd.
type ScheddWaker struct {
	client scheddpb.ScheddClient
}

// NewScheddWaker wraps a schedd client authenticated as pgproxyd.
func NewScheddWaker(client scheddpb.ScheddClient) *ScheddWaker {
	return &ScheddWaker{client: client}
}

func (w *ScheddWaker) Wake(ctx context.Context, databaseID string) (string, error) {
	rt, err := w.client.WakeDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: databaseID})
	if err != nil {
		return "", err
	}
	if rt.GetVmmdTarget() == "" {
		return "", errors.New("pgproxy: schedd returned no vmmd target")
	}
	return rt.GetVmmdTarget(), nil
}

func (w *ScheddWaker) Touch(ctx context.Context, databaseID string) error {
	_, err := w.client.TouchDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: databaseID})
	return err
}

// VmmdDialer opens sessions through vmmd's DatabaseSession stream. It keeps
// one connection per vmmd target.
type VmmdDialer struct {
	tls  *tls.Config
	dial func(ctx context.Context, target string, tlsCfg *tls.Config) (*grpc.ClientConn, error)

	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

// NewVmmdDialer dials vmmd through the overlay with pgproxyd's client mTLS.
func NewVmmdDialer(tlsCfg *tls.Config) *VmmdDialer {
	return NewVmmdDialerWith(tlsCfg, func(ctx context.Context, target string, tlsCfg *tls.Config) (*grpc.ClientConn, error) {
		return overlay.Dial(ctx, overlay.New(target), tlsCfg)
	})
}

// NewVmmdDialerWith is NewVmmdDialer with an injected dialer.
func NewVmmdDialerWith(tlsCfg *tls.Config, dial func(context.Context, string, *tls.Config) (*grpc.ClientConn, error)) *VmmdDialer {
	return &VmmdDialer{tls: tlsCfg, dial: dial, conns: map[string]*grpc.ClientConn{}}
}

var _ SessionDialer = (*VmmdDialer)(nil)

func (d *VmmdDialer) client(ctx context.Context, target string) (vmmdpb.VmmdClient, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	conn, ok := d.conns[target]
	if !ok {
		var err error
		if conn, err = d.dial(ctx, target, d.tls); err != nil {
			return nil, err
		}
		d.conns[target] = conn
	}
	return vmmdpb.NewVmmdClient(conn), nil
}

// Dial opens a session. The stream outlives ctx, which only bounds setup;
// Close ends it.
func (d *VmmdDialer) Dial(ctx context.Context, target, databaseID string) (io.ReadWriteCloser, error) {
	c, err := d.client(ctx, target)
	if err != nil {
		return nil, err
	}
	// The session must outlive the setup context, so it keeps ctx's values
	// but not its cancellation.
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stream, err := c.DatabaseSession(streamCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := stream.Send(&vmmdpb.DatabaseSessionFrame{DatabaseId: databaseID}); err != nil {
		cancel()
		return nil, err
	}
	return &sessionStream{stream: stream, cancel: cancel}, nil
}

// Close releases every vmmd connection.
func (d *VmmdDialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for target, conn := range d.conns {
		_ = conn.Close()
		delete(d.conns, target)
	}
	return nil
}

// sessionStream adapts a DatabaseSession stream to io.ReadWriteCloser.
type sessionStream struct {
	stream  vmmdpb.Vmmd_DatabaseSessionClient
	cancel  context.CancelFunc
	pending []byte
	writeMu sync.Mutex
}

const maxSessionFrame = 64 << 10

func (s *sessionStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		frame, err := s.stream.Recv()
		if err != nil {
			return 0, io.EOF
		}
		s.pending = frame.GetData()
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *sessionStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	written := 0
	for written < len(p) {
		end := min(written+maxSessionFrame, len(p))
		chunk := append([]byte(nil), p[written:end]...)
		if err := s.stream.Send(&vmmdpb.DatabaseSessionFrame{Data: chunk}); err != nil {
			return written, err
		}
		written = end
	}
	return written, nil
}

func (s *sessionStream) Close() error {
	_ = s.stream.CloseSend()
	s.cancel()
	return nil
}

var _ Credentials = (*dbcreds.VerifierStore)(nil)
