package apidgrpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// GuestSpansClient forwards in-guest trace exports from vmmd to apid's
// SpansWriter service (ADR-957). It is separate from SpansWriterClient so the
// gateway producers' interface and fakes are unaffected.
type GuestSpansClient interface {
	IngestGuestSpans(ctx context.Context, req *apidpb.IngestGuestSpansRequest) (*apidpb.IngestGuestSpansResponse, error)
	Close() error
}

type GuestSpansClientImpl struct {
	conn *grpc.ClientConn
	cli  apidpb.SpansWriterClient
}

var _ GuestSpansClient = (*GuestSpansClientImpl)(nil)

// DialGuestSpans connects lazily; a missing socket surfaces per call.
func DialGuestSpans(ctx context.Context, target string, tlsCfg *tls.Config) (*GuestSpansClientImpl, error) {
	if target == "" {
		return nil, errors.New("apidgrpc: empty apid target for guest spans")
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: dial apid %q for guest spans: %w", target, err)
	}
	return &GuestSpansClientImpl{conn: conn, cli: apidpb.NewSpansWriterClient(conn)}, nil
}

func (c *GuestSpansClientImpl) IngestGuestSpans(ctx context.Context, req *apidpb.IngestGuestSpansRequest) (*apidpb.IngestGuestSpansResponse, error) {
	return c.cli.IngestGuestSpans(ctx, req)
}

func (c *GuestSpansClientImpl) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
