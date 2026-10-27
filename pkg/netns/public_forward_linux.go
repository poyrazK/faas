//go:build linux

package netns

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// SO_MARK applies to every TCP/UDP packet, including retransmissions and pool
// sockets. The host output guard compares its encoded ifindex to actual oif.
// Binding receive to the slave would reject bridge-master-delivered replies;
// IP_UNICAST_IF alone does not constrain TCP on older supported kernels.
func pinPublicOutput(d *net.Dialer, index int) error {
	originalContext, original := d.ControlContext, d.Control
	d.ControlContext = func(ctx context.Context, network, address string, conn syscall.RawConn) error {
		if originalContext != nil {
			if err := originalContext(ctx, network, address, conn); err != nil {
				return err
			}
		} else if original != nil {
			if err := original(network, address, conn); err != nil {
				return err
			}
		}
		var optionErr error
		if err := conn.Control(func(fd uintptr) {
			optionErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(api.PublicForwardMarkFlag|uint32(index)))
		}); err != nil {
			return err
		}
		if optionErr != nil {
			return fmt.Errorf("public forward output pin: %w", optionErr)
		}
		return nil
	}
	return nil
}

// Replies enter through a bridge slave and are delivered on its master. The
// pinned /32 output route therefore requires loose/disabled reverse-path
// filtering on the root bridge. Refuse publication instead of silently timing
// out or changing the operator's global sysctls.
func CheckPublicForwardRouting() error {
	all, err := os.ReadFile("/proc/sys/net/ipv4/conf/all/rp_filter")
	if err != nil {
		return fmt.Errorf("public forward routing: %w", err)
	}
	bridge, err := os.ReadFile("/proc/sys/net/ipv4/conf/" + TenantBridge + "/rp_filter")
	if err != nil {
		return fmt.Errorf("public forward routing: %w", err)
	}
	return publicForwardRPFilter(all, bridge)
}

func publicForwardRPFilter(all, bridge []byte) error {
	effective := 0
	for _, raw := range [][]byte{all, bridge} {
		value, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || value < 0 || value > 2 {
			return fmt.Errorf("public forward routing: invalid rp_filter")
		}
		if value > effective {
			effective = value
		}
	}
	if effective == 1 {
		return fmt.Errorf("public forward routing requires loose or disabled reverse-path filtering on %s", TenantBridge)
	}
	return nil
}
