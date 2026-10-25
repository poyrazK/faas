package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

const streamBridgeSocketDirectory = "/var/run/faas/stream"

// FenceStaleStreamBridges runs before a new vmmd serves forwarding RPCs. A
// responsive old bridge refuses startup: its permits belonged to another
// process. Only confirmed dead socket entries are removed. Parent-death kill
// and systemd's control-group shutdown provide the production process fence.
func FenceStaleStreamBridges(ctx context.Context) error {
	return fenceStaleStreamBridges(ctx, streamBridgeSocketDirectory)
}

func fenceStaleStreamBridges(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read bridge sockets: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("fence bridge sockets: %w", err)
		}
		if !strings.HasSuffix(entry.Name(), ".sock") || entry.Type()&os.ModeSocket == 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		conn, err := (&net.Dialer{Timeout: api.TrafficBridgeFenceDialTimeout}).DialContext(ctx, "unix", path)
		if err == nil {
			_ = conn.Close()
			return fmt.Errorf("bridge %q still accepts connections from an earlier vmmd; stop its owner before restart", entry.Name())
		}
		if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("bridge %q cannot be fenced: %w", entry.Name(), err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove dead bridge %q: %w", entry.Name(), err)
		}
	}
	return nil
}
