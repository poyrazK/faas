//go:build metal

package builderd

import (
	"context"
	"errors"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runenv"
	"io"
	"strings"
)

// StreamEnvironmentBuildLogs reads only this driver's validated build owner.
func (d *VMMDriver) StreamEnvironmentBuildLogs(ctx context.Context, h BuildHandle, emit func(string, bool) error) error {
	if !runenv.ValidID(h.BuildID) || h.Instance != "build-"+h.BuildID {
		return errors.New("builderd: invalid diagnostic owner")
	}
	stream, err := d.cli.Logs(ctx, &vmmdpb.LogsRequest{Instance: h.Instance})
	if err != nil {
		return err
	}
	for {
		r, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.GetIsGap() {
			if err = emit("", true); err != nil {
				return err
			}
			continue
		}
		for _, line := range strings.Split(r.GetLine(), "\n") {
			if safe := runenv.BuildDiagnosticLine(line); safe != "" {
				if err = emit(safe, false); err != nil {
					return err
				}
			}
		}
	}
}
