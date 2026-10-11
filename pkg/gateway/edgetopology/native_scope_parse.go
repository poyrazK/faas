package edgetopology

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func matchNativeUnit(raw []byte, service NativeServiceReview) error {
	if len(raw) == 0 || len(raw) > api.RuntimeUpgradeNativeUnitMaxBytes {
		return ErrNativeUnverified
	}
	expected := map[string]string{"Id": service.Unit, "LoadState": "loaded", "ActiveState": "active", "SubState": "running", "MainPID": strconv.Itoa(service.PID), "ControlGroup": service.Cgroup, "InvocationID": service.InvocationID, "NeedDaemonReload": "no"}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || seen[key] {
			return ErrNativeUnverified
		}
		want, ok := expected[key]
		if !ok || value != want {
			return ErrNativeUnverified
		}
		seen[key] = true
	}
	if len(seen) != len(expected) {
		return ErrNativeUnverified
	}
	return nil
}

func matchNativeStat(raw []byte, pid int, start uint64) error {
	text := strings.TrimSuffix(string(raw), "\n")
	prefix := strconv.Itoa(pid) + " ("
	end := strings.LastIndex(text, ") ")
	if !strings.HasPrefix(text, prefix) || end < len(prefix) || len(raw) > api.RuntimeUpgradeNativeMetadataMaxBytes {
		return ErrNativeUnverified
	}
	fields := strings.Fields(text[end+2:]) // comm can contain spaces and closing parentheses
	if len(fields) < 20 || !strings.Contains("RSDI", fields[0]) || len(fields[0]) != 1 || fields[19] != strconv.FormatUint(start, 10) {
		return ErrNativeUnverified
	}
	return nil
}

func matchNativeUID(raw []byte, uid uint32) error {
	if len(raw) > api.RuntimeUpgradeNativeMetadataMaxBytes {
		return ErrNativeUnverified
	}
	seen := false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
		if seen || len(fields) != 4 {
			return ErrNativeUnverified
		}
		for _, field := range fields {
			if field != strconv.FormatUint(uint64(uid), 10) {
				return ErrNativeUnverified
			}
		}
		seen = true
	}
	if !seen {
		return ErrNativeUnverified
	}
	return nil
}

type nativeTCPRow struct {
	Address   string
	Inode     uint64
	Listening bool
}

func parseNativeTCP(raw []byte, ipv6 bool) ([]nativeTCPRow, error) {
	if len(raw) == 0 || len(raw) > api.RuntimeUpgradeNativeProcMaxBytes || raw[len(raw)-1] != '\n' {
		return nil, ErrNativeUnverified
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines)-1 > api.RuntimeUpgradeNativeTCPRowLimit {
		return nil, ErrNativeUnverified
	}
	header := strings.Fields(lines[0])
	if len(header) < 12 || header[0] != "sl" || header[1] != "local_address" || (header[2] != "rem_address" && header[2] != "remote_address") || header[3] != "st" || header[11] != "inode" {
		return nil, ErrNativeUnverified
	}
	rows := make([]nativeTCPRow, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 10 || !strings.HasSuffix(fields[0], ":") {
			return nil, ErrNativeUnverified
		}
		local, err := nativeTCPHexAddress(fields[1], ipv6)
		if err != nil {
			return nil, err
		}
		remote, err := nativeTCPHexAddress(fields[2], ipv6)
		if err != nil {
			return nil, err
		}
		state, err := strconv.ParseUint(fields[3], 16, 8)
		if err != nil || len(fields[3]) != 2 || state == 0 || state > 12 {
			return nil, ErrNativeUnverified
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, ErrNativeUnverified
		}
		listening := state == 0x0a
		if listening && (inode == 0 || local.Port() == 0 || !remote.Addr().IsUnspecified() || remote.Port() != 0 || local.Addr().IsMulticast() || local.Addr().Is4In6()) {
			return nil, ErrNativeUnverified
		}
		rows = append(rows, nativeTCPRow{Address: local.String(), Inode: inode, Listening: listening})
	}
	return rows, nil
}

func nativeTCPHexAddress(value string, ipv6 bool) (netip.AddrPort, error) {
	host, port, ok := strings.Cut(value, ":")
	want := 8
	if ipv6 {
		want = 32
	}
	if !ok || len(host) != want || len(port) != 4 {
		return netip.AddrPort{}, ErrNativeUnverified
	}
	var address [16]byte
	for i := 0; i < want/8; i++ {
		word, err := strconv.ParseUint(host[i*8:(i+1)*8], 16, 32)
		if err != nil {
			return netip.AddrPort{}, ErrNativeUnverified
		}
		binary.NativeEndian.PutUint32(address[i*4:(i+1)*4], uint32(word))
	}
	p, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return netip.AddrPort{}, ErrNativeUnverified
	}
	if !ipv6 {
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte(address[:4])), uint16(p)), nil
	}
	return netip.AddrPortFrom(netip.AddrFrom16(address), uint16(p)), nil
}

func nativeTCPOverlap(a, b string) bool {
	left, err := netip.ParseAddrPort(a)
	if err != nil {
		return true
	}
	right, err := netip.ParseAddrPort(b)
	if err != nil {
		return true
	}
	if left.Port() != right.Port() {
		return false
	}
	// /proc cannot reveal IPV6_V6ONLY. Conservatively reject an IPv6 wildcard
	// competing with IPv4; never infer IPv4 coverage from an IPv6 wildcard.
	if left.Addr().BitLen() != right.Addr().BitLen() {
		return (left.Addr().Is6() && left.Addr().IsUnspecified()) || (right.Addr().Is6() && right.Addr().IsUnspecified())
	}
	return left.Addr() == right.Addr() || left.Addr().IsUnspecified() || right.Addr().IsUnspecified()
}

func nativeReadError(operation string) error {
	return fmt.Errorf("%w: %s unavailable or changed", ErrNativeUnverified, operation)
}
