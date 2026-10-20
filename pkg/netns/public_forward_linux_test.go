//go:build linux

package netns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

func TestPublicForwardRoutingRefusesStrictOrInvalidReversePathFilter(t *testing.T) {
	for all := 0; all <= 2; all++ {
		for bridge := 0; bridge <= 2; bridge++ {
			err := publicForwardRPFilter([]byte(fmt.Sprint(all)), []byte(fmt.Sprint(bridge)))
			if (err != nil) != (max(all, bridge) == 1) {
				t.Fatalf("rp_filter all=%d bridge=%d: %v", all, bridge, err)
			}
		}
	}
	for _, raw := range []string{"", "3", "-1", "not available"} {
		if err := publicForwardRPFilter([]byte(raw), []byte("2")); err == nil {
			t.Fatalf("invalid routing evidence accepted: %q", raw)
		}
	}
}

func TestPublicForwardOutputPinIsOnTCPAndUDPSockets(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("SO_MARK requires native bridge privileges")
	}
	for _, network := range []string{"tcp", "udp4"} {
		t.Run(network, func(t *testing.T) {
			var address string
			if network == "tcp" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = listener.Close() }()
				address = listener.Addr().String()
			} else {
				listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = listener.Close() }()
				address = listener.LocalAddr().String()
			}
			target := loopbackPublicTarget(t)
			controlled := false
			dialer := &net.Dialer{Timeout: time.Second, ControlContext: func(_ context.Context, _, _ string, _ syscall.RawConn) error { controlled = true; return nil }}
			conn, err := target.DialContext(t.Context(), dialer, network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if !controlled {
				t.Fatal("existing connection control hook was discarded")
			}
			raw, err := conn.(syscall.Conn).SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var index int
			var optionErr error
			if err := raw.Control(func(fd uintptr) { index, optionErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK) }); err != nil {
				t.Fatal(err)
			}
			want := api.PublicForwardMarkFlag | uint32(target.IfIndex)
			if optionErr != nil || uint32(index) != want {
				t.Fatalf("socket can choose a replacement interface: index=%d want=%d err=%v", index, want, optionErr)
			}
		})
	}
}

// Run only inside the dedicated namespace fixture that installs the real host
// guard. Ordinary portable CI must not change its host firewall. The fixture
// has no external network and makes no KVM/funded-packet qualification claim.
func TestPublicForwardGuardRejectsMismatchedNewAndExistingSockets(t *testing.T) {
	if os.Getenv("GREGALE_PUBLIC_FORWARD_GUARD_TEST") != "1" {
		t.Skip("isolated root firewall fixture only")
	}
	target := loopbackPublicTarget(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	wrong := &net.Dialer{Timeout: 150 * time.Millisecond}
	if err := pinPublicOutput(wrong, target.IfIndex+1); err != nil {
		t.Fatal(err)
	}
	if conn, err := wrong.DialContext(t.Context(), "tcp", listener.Addr().String()); err == nil {
		_ = conn.Close()
		t.Fatal("new TCP socket escaped through another output interface")
	}
	client, err := target.DialContext(t.Context(), &net.Dialer{Timeout: time.Second}, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	wrongMark := int(api.PublicForwardMarkFlag | uint32(target.IfIndex+1))
	setMark := func(conn net.Conn) {
		t.Helper()
		raw, err := conn.(syscall.Conn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var markErr error
		if err := raw.Control(func(fd uintptr) { markErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, wrongMark) }); err != nil || markErr != nil {
			t.Fatalf("set retired output identity: %v/%v", err, markErr)
		}
	}
	setMark(client)
	_, _ = client.Write([]byte("retired TCP generation"))
	_ = server.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buffer := make([]byte, 128)
	if n, err := server.Read(buffer); n != 0 || err == nil {
		t.Fatalf("existing TCP socket crossed another interface: n=%d err=%v", n, err)
	}
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = udp.Close() }()
	connected, err := target.DialContext(t.Context(), &net.Dialer{Timeout: time.Second}, "udp4", udp.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connected.Close() }()
	_, _ = connected.Write([]byte("original"))
	_ = udp.SetReadDeadline(time.Now().Add(time.Second))
	if n, _, err := udp.ReadFrom(buffer); err != nil || string(buffer[:n]) != "original" {
		t.Fatalf("original UDP generation blocked: %v", err)
	}
	setMark(connected)
	_, _ = connected.Write([]byte("retired UDP generation"))
	_ = udp.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, _, err := udp.ReadFrom(buffer); n != 0 || err == nil {
		t.Fatalf("connected UDP escaped: n=%d err=%v", n, err)
	} else {
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("UDP did not remain isolated: %v", err)
		}
	}
}
