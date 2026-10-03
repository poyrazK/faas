//go:build linux

// adr: 402
package fcvm

import (
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

func TestResourceLinksRtnetlinkParser(t *testing.T) {
	attribute := func(kind uint16, value []byte) []byte {
		size := 4 + len(value)
		out := make([]byte, (size+3)&^3)
		binary.NativeEndian.PutUint16(out[:2], uint16(size))
		binary.NativeEndian.PutUint16(out[2:4], kind)
		copy(out[4:], value)
		return out
	}
	data := make([]byte, unix.SizeofIfInfomsg)
	binary.NativeEndian.PutUint32(data[4:8], 42)
	data = append(data, attribute(unix.IFLA_IFNAME, []byte("vh0\x00"))...)
	data = append(data, attribute(unix.IFLA_ADDRESS, []byte{2, 17, 34, 51, 68, 85})...)
	data = append(data, attribute(unix.IFLA_LINKINFO|unix.NLA_F_NESTED, attribute(unix.IFLA_INFO_KIND, []byte("veth\x00")))...)
	got, err := parseResourceLink(data)
	if err != nil || *got != (resourceLinkIdentity{Index: 42, Name: "vh0", Address: "02:11:22:33:44:55", Kind: "veth"}) {
		t.Fatalf("nested link attributes: %+v %v", got, err)
	}
	for _, bad := range [][]byte{data[:15], data[:17], append(append([]byte(nil), data...), 0), append(append([]byte(nil), data...), 8, 0, 1, 0)} {
		if _, err := parseResourceLink(bad); err == nil {
			t.Fatal("malformed link inventory accepted")
		}
	}
}
