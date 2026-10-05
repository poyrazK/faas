//go:build linux

// adr: 476
package fcvm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func resourceNetworkContext() (*resourceMountIdentity, error) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, err
	}
	info, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		return nil, err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	id := resourceMountIdentity{BootID: strings.TrimSpace(string(boot))}
	if !ok || s.Ino == 0 || !looksLikeInstanceID(id.BootID) {
		return nil, errors.New("invalid network namespace context")
	}
	id.Namespace = s.Ino
	return &id, nil
}

// Query rtnetlink in the actual calling network namespace. sysfs may still
// expose another namespace's links after setns/unshare and is not authority.
func resourceNetworkLinkAt(name string, index int) (*resourceLinkIdentity, error) {
	data, err := syscall.NetlinkRIB(unix.RTM_GETLINK, unix.AF_UNSPEC)
	if err != nil {
		return nil, err
	}
	messages, err := syscall.ParseNetlinkMessage(data)
	if err != nil {
		return nil, err
	}
	var byName, byIndex *resourceLinkIdentity
	for _, msg := range messages {
		if msg.Header.Flags&unix.NLM_F_DUMP_INTR != 0 {
			return nil, errors.New("interrupted link inventory")
		}
		if msg.Header.Type != unix.RTM_NEWLINK {
			continue
		}
		link, err := parseResourceLink(msg.Data)
		if err != nil {
			return nil, err
		}
		if link.Name == name {
			byName = link
		}
		if index > 0 && link.Index == index {
			byIndex = link
		}
	}
	if byName != nil {
		return byName, nil
	}
	return byIndex, nil // a renamed owned index must retain the slot
}

func parseResourceLink(data []byte) (*resourceLinkIdentity, error) {
	if len(data) < unix.SizeofIfInfomsg {
		return nil, errors.New("short rtnetlink interface")
	}
	l := &resourceLinkIdentity{Index: int(int32(binary.NativeEndian.Uint32(data[4:8])))}
	err := resourceLinkAttributes(data[unix.SizeofIfInfomsg:], func(kind uint16, value []byte) error {
		switch kind {
		case unix.IFLA_IFNAME:
			l.Name = strings.TrimRight(string(value), "\x00")
		case unix.IFLA_ADDRESS:
			if len(value) == 6 {
				l.Address = net.HardwareAddr(value).String()
			}
		case unix.IFLA_LINKINFO:
			return resourceLinkAttributes(value, func(kind uint16, value []byte) error {
				if kind == unix.IFLA_INFO_KIND {
					l.Kind = strings.TrimRight(string(value), "\x00")
				}
				return nil
			})
		}
		return nil
	})
	if err != nil || l.Index <= 0 || l.Name == "" {
		return nil, errors.Join(errors.New("invalid rtnetlink interface"), err)
	}
	return l, nil
}

func resourceLinkAttributes(data []byte, visit func(uint16, []byte) error) error {
	for len(data) != 0 {
		if len(data) < unix.SizeofRtAttr {
			return errors.New("short rtnetlink attribute")
		}
		size := int(binary.NativeEndian.Uint16(data[:2]))
		kind := binary.NativeEndian.Uint16(data[2:4]) & 0x3fff
		aligned := (size + 3) &^ 3
		if size < unix.SizeofRtAttr || aligned > len(data) {
			return errors.New("invalid rtnetlink attribute length")
		}
		if err := visit(kind, data[unix.SizeofRtAttr:size]); err != nil {
			return err
		}
		data = data[aligned:]
	}
	return nil
}

// RTM_DELLINK addresses the checked index, avoiding a later name lookup. Linux
// has no compare-and-delete operation for an address; index reuse remains a race.
func resourceNetworkLinkDelete(index int) error {
	if index <= 0 || index > 1<<31-1 {
		return errors.New("invalid veth deletion index")
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 5}); err != nil {
		return err
	}
	request := make([]byte, unix.NLMSG_HDRLEN+unix.SizeofIfInfomsg)
	binary.NativeEndian.PutUint32(request[:4], uint32(len(request)))
	binary.NativeEndian.PutUint16(request[4:6], unix.RTM_DELLINK)
	binary.NativeEndian.PutUint16(request[6:8], unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	binary.NativeEndian.PutUint32(request[8:12], 1)
	binary.NativeEndian.PutUint32(request[unix.NLMSG_HDRLEN+4:unix.NLMSG_HDRLEN+8], uint32(index))
	if err := unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return err
	}
	response := make([]byte, 4096)
	n, from, err := unix.Recvfrom(fd, response, 0)
	if err != nil {
		return err
	}
	sender, ok := from.(*unix.SockaddrNetlink)
	if !ok || sender.Pid != 0 {
		return errors.New("unexpected rtnetlink deletion sender")
	}
	messages, err := syscall.ParseNetlinkMessage(response[:n])
	if err != nil {
		return err
	}
	for _, msg := range messages {
		if msg.Header.Type != unix.NLMSG_ERROR || msg.Header.Seq != 1 || len(msg.Data) < 4 {
			continue
		}
		code := int32(binary.NativeEndian.Uint32(msg.Data[:4]))
		if code == 0 {
			return nil
		}
		return fmt.Errorf("delete veth index %d: %w", index, syscall.Errno(-code))
	}
	return errors.New("veth deletion acknowledgement missing")
}
