package fcvm

// adr: 20261109130000 — vmmd resets an app guest's TCP connections before it
// destroys the guest, so its databases free the sessions at once.

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/connreleaseproto"
)

func TestReleaseGuestConnectionsWire(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply []byte
		want  int
		err   string
	}{
		{name: "current guest", reply: []byte{0, 0, 0, 0, 12}, want: 12},
		{name: "nothing to reset", reply: []byte{0, 0, 0, 0, 0}},
		{name: "older guest", reply: []byte{7}, err: "unsupported by guest"},
		{name: "truncated count", reply: []byte{0, 0}, err: "count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := shortChrootBase(t, "rel")
			instance := "i1"
			root := filepath.Join(base, "f", instance, "root")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", filepath.Join(root, VsockUDSSocketName))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			got := make(chan [8]byte, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				for {
					var one [1]byte
					if _, err := io.ReadFull(conn, one[:]); err != nil || one[0] == '\n' {
						break
					}
				}
				if _, err := conn.Write([]byte("OK 1024\n")); err != nil {
					return
				}
				var hdr [8]byte
				if _, err := io.ReadFull(conn, hdr[:]); err != nil {
					return
				}
				got <- hdr
				_, _ = conn.Write(tc.reply)
			}()
			v := &JailerVMM{chrootBase: base, fcName: "f"}
			n, err := v.ReleaseGuestConnections(context.Background(), Lease{Instance: instance})
			if tc.err == "" {
				if err != nil || n != tc.want {
					t.Fatalf("ReleaseGuestConnections() = %d, %v; want %d", n, err, tc.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("ReleaseGuestConnections() error = %v, want %q", err, tc.err)
			}
			hdr := <-got
			if typ, length := binary.BigEndian.Uint32(hdr[:4]), binary.BigEndian.Uint32(hdr[4:]); typ != connreleaseproto.MessageType || length != 0 {
				t.Fatalf("request header = type %d length %d, want type %d length 0", typ, length, connreleaseproto.MessageType)
			}
		})
	}
}

func TestReleaseGuestConnectionsNoGuest(t *testing.T) {
	v := &JailerVMM{chrootBase: shortChrootBase(t, "rel"), fcName: "f"}
	if _, err := v.ReleaseGuestConnections(context.Background(), Lease{Instance: "gone"}); err == nil {
		t.Fatal("release against a missing guest socket succeeded")
	}
}

func TestReleasesConnectionsEligibility(t *testing.T) {
	app := func(edit func(*Instance)) *Instance {
		inst := &Instance{Lease: Lease{Instance: "i1"}}
		if edit != nil {
			edit(inst)
		}
		return inst
	}
	m := &Manager{releaseConnectionsOnDestroy: true}
	for name, tc := range map[string]struct {
		inst      *Instance
		exportDir string
		want      bool
	}{
		"serving app":      {inst: app(nil), want: true},
		"app task":         {inst: app(func(i *Instance) { i.AppTaskOnly = true }), want: true},
		"builder":          {inst: app(func(i *Instance) { i.Lease.IsBuilder = true })},
		"export":           {inst: app(nil), exportDir: "/out"},
		"execution":        {inst: app(func(i *Instance) { i.ExecutionOnly = true })},
		"data repair":      {inst: app(func(i *Instance) { i.DataRepairOnly = true })},
		"qualification":    {inst: app(func(i *Instance) { i.QualificationOnly = true })},
		"warm-pool spare":  {inst: app(func(i *Instance) { i.Paused = true })},
		"unknown instance": {},
	} {
		if got := m.releasesConnections(tc.inst, tc.exportDir); got != tc.want {
			t.Errorf("%s: releasesConnections = %v, want %v", name, got, tc.want)
		}
	}
	off := &Manager{}
	if off.releasesConnections(app(nil), "") {
		t.Fatal("disabled manager still releases connections")
	}
}
