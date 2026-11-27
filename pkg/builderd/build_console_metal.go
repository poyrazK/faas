//go:build metal && linux

package builderd

import (
	"context"
	"errors"
	"fmt"
	"io"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ BuildConsoleVM = (*VMMDriver)(nil)

// FollowBuildConsole streams the builder instance's console through vmmd's
// per-instance log ring. since_seq 1 replays what the ring kept from boot,
// so the first steps printed before the stream opened are not lost; vmmd
// lets builderd's certificate follow only "build-" instances.
func (d *VMMDriver) FollowBuildConsole(ctx context.Context, h BuildHandle, line func(string)) error {
	if h.Instance == "" {
		return errors.New("builderd: follow console: empty instance")
	}
	follow := true
	stream, err := d.cli.Logs(ctx, &vmmdpb.LogsRequest{Instance: h.Instance, SinceSeq: 1, Follow: &follow})
	if err != nil {
		return fmt.Errorf("builderd: follow console %s: %w", h.Instance, err)
	}
	for {
		resp, err := stream.Recv()
		switch {
		case err == nil:
		case errors.Is(err, io.EOF), ctx.Err() != nil, status.Code(err) == codes.Canceled:
			return nil
		default:
			return fmt.Errorf("builderd: follow console %s: %w", h.Instance, err)
		}
		if resp.GetIsGap() {
			continue
		}
		line(resp.GetLine())
	}
}
