package internal

import (
	"net/http"
	"net/netip"
)

// guest-init calls hooks over the local socket. Public gateway requests arrive
// from the guest's non-loopback interface even when a client forges the marker.
// Keep this narrower than IsLoopback: only the actual guest-init destinations
// are accepted, including IPv4-mapped IPv6, and ambiguous markers are rejected.
func lifecycleHook(r *http.Request) string {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || r.Method != http.MethodPost {
		return ""
	}
	addr := peer.Addr().Unmap()
	if addr != netip.MustParseAddr("127.0.0.1") && addr != netip.IPv6Loopback() {
		return ""
	}
	before, after := r.Header.Values("X-Faas-Before-Checkpoint"), r.Header.Values("X-Faas-After-Restore")
	if len(before) == 1 && before[0] == "1" && len(after) == 0 {
		return "before_checkpoint"
	}
	if len(after) == 1 && after[0] == "1" && len(before) == 0 {
		return "after_restore"
	}
	return ""
}
