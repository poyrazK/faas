package api

import (
	"encoding/binary"
	"net/netip"
)

// ServiceAddressForIndex returns the service address an app holds for its
// account-scoped index (ADR-482). Indices outside
// [ServiceAddressIndexMin, ServiceAddressIndexMax] have no address.
func ServiceAddressForIndex(index int) (netip.Addr, bool) {
	if index < ServiceAddressIndexMin || index > ServiceAddressIndexMax {
		return netip.Addr{}, false
	}
	base := ServiceAddressCIDR().Addr().As4()
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], binary.BigEndian.Uint32(base[:])+uint32(index))
	return netip.AddrFrom4(out), true
}

// ServiceAddressIndexOf is the inverse of ServiceAddressForIndex. It accepts
// only IPv4 addresses (including IPv4-mapped IPv6) inside ServiceAddressCIDR
// whose index is allocatable.
func ServiceAddressIndexOf(addr netip.Addr) (int, bool) {
	addr = addr.Unmap()
	block := ServiceAddressCIDR()
	if !addr.Is4() || !block.Contains(addr) {
		return 0, false
	}
	base := block.Addr().As4()
	raw := addr.As4()
	index := int(binary.BigEndian.Uint32(raw[:]) - binary.BigEndian.Uint32(base[:]))
	if index < ServiceAddressIndexMin || index > ServiceAddressIndexMax {
		return 0, false
	}
	return index, true
}
