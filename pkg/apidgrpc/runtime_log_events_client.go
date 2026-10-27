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

// RuntimeLogEventsClient forwards vmmd's runtime warn and error lines to
// apid's LogEvents service (ADR-20261018083536 slice B). It dials the same
// target as GuestSpansClient; the service shares the SpansWriter listeners.
type RuntimeLogEventsClient interface {
	RecordRuntimeLogEvents(ctx context.Context, req *apidpb.RecordRuntimeLogEventsRequest) (*apidpb.RecordRuntimeLogEventsResponse, error)
	Close() error
}

type RuntimeLogEventsClientImpl struct {
	conn *grpc.ClientConn
	cli  apidpb.LogEventsClient
}

var _ RuntimeLogEventsClient = (*RuntimeLogEventsClientImpl)(nil)

// DialRuntimeLogEvents connects lazily; a missing socket surfaces per call.
func DialRuntimeLogEvents(ctx context.Context, target string, tlsCfg *tls.Config) (*RuntimeLogEventsClientImpl, error) {
	if target == "" {
		return nil, errors.New("apidgrpc: empty apid target for runtime log events")
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: dial apid %q for runtime log events: %w", target, err)
	}
	return &RuntimeLogEventsClientImpl{conn: conn, cli: apidpb.NewLogEventsClient(conn)}, nil
}

func (c *RuntimeLogEventsClientImpl) RecordRuntimeLogEvents(ctx context.Context, req *apidpb.RecordRuntimeLogEventsRequest) (*apidpb.RecordRuntimeLogEventsResponse, error) {
	return c.cli.RecordRuntimeLogEvents(ctx, req)
}

func (c *RuntimeLogEventsClientImpl) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
