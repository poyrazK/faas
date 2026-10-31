//go:build unix

package wire_test

// adr: 1736 — a hand-off successor must claim every socket its predecessor
// passed before it reports ready. Readiness releases unclaimed sockets; a
// later bind of the same address fails while the predecessor still holds it
// (gatewayd-internal on fsn-3, 2026-10-25: bind 0.0.0.0:9097 in use).

import (
	"errors"
	"net"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestHandoffSocketMustBeClaimedBeforeReady(t *testing.T) {
	cases := []struct {
		name          string
		claimBefore   bool
		wantInherited bool
	}{
		{name: "claimed before readiness survives the release", claimBefore: true, wantInherited: true},
		{name: "claimed after readiness finds it released", claimBefore: false, wantInherited: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			predecessor, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = predecessor.Close() }()
			addr := predecessor.Addr().String()
			f, err := predecessor.(*net.TCPListener).File()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			defer wire.UseHandoffListenersForTest([]int{dupFD(t, f)})()

			var claimed net.Listener
			if tc.claimBefore {
				l, ok, err := wire.InheritedListener("tcp", addr)
				if err != nil || !ok {
					t.Fatalf("claim before readiness: ok=%v err=%v", ok, err)
				}
				claimed = l
				defer func() { _ = claimed.Close() }()
			}
			released := wire.ReleaseUnclaimedHandoff()
			if tc.claimBefore && len(released) != 0 {
				t.Fatalf("released a claimed socket: %v", released)
			}
			if tc.claimBefore {
				return
			}
			if _, ok, _ := wire.InheritedListener("tcp", addr); ok != tc.wantInherited {
				t.Fatalf("inherited after release = %v, want %v", ok, tc.wantInherited)
			}
			// With the passed socket released and the predecessor still
			// listening, the fallback bind is exactly the failure seen on prod.
			_, err = wire.ListenInherited("tcp", addr)
			if !errors.Is(err, syscall.EADDRINUSE) {
				t.Fatalf("late claim error = %v, want EADDRINUSE", err)
			}
		})
	}
}
