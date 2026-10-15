package main

import (
	"net"
	"slices"
	"strconv"
)

// ADR-1170: the host reaches a guest on the deployment's runtime port
// (inferred per framework, e.g. 3000 for Node) at the guest's tap address.
// Development servers often listen elsewhere: Vite on 5173, Astro on 4321,
// and many only on localhost. In a watch-mode image guest-init bridges the
// runtime port to whatever the development server actually listens on, so a
// dev script, a composite script or an unknown framework works without
// host or port flags. Production images keep the plain contract (listen on
// $PORT on every interface, app_loopback_bound otherwise).

// devWatchBridgeTarget decides whether the runtime port needs a bridge. It
// returns the address to forward guestIP:want to, or ok=false when the host
// can already reach a listener on want: one on every interface or on the
// guest address. A listener on want is preferred as the target; otherwise
// the first one wins, which is the server's main socket for every
// development server watch mode starts.
func devWatchBridgeTarget(want int, guestIP net.IP, listeners []listenSocket) (target string, ok bool) {
	if len(listeners) == 0 {
		return "", false
	}
	for _, l := range listeners {
		if l.Port == want && (l.IP.IsUnspecified() || l.IP.Equal(guestIP)) {
			return "", false
		}
	}
	pick := listeners[0]
	if i := slices.IndexFunc(listeners, func(l listenSocket) bool { return l.Port == want }); i >= 0 {
		pick = listeners[i]
	}
	ip := pick.IP
	switch {
	case ip == nil || ip.To4() != nil && ip.IsUnspecified():
		ip = net.IPv4(127, 0, 0, 1)
	case ip.IsUnspecified():
		ip = net.IPv6loopback
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(pick.Port)), true
}

// sameListeners reports whether two scans saw the same sockets; the bridge
// waits for the development server's listeners to settle before choosing.
func sameListeners(a, b []listenSocket) bool {
	return slices.EqualFunc(a, b, func(x, y listenSocket) bool { return x.Port == y.Port && x.IP.Equal(y.IP) })
}
