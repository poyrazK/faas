package main

import (
	"fmt"
	"os/user"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/wire"
)

// debugPprofSystemUIDMax is the highest system-account uid: daemon users are
// system accounts, while people (and tenant jail uids, 20000–29999) sit
// above it.
const debugPprofSystemUIDMax = 999

// debugPprofLookupUID resolves a daemon's service account. A variable for
// tests.
var debugPprofLookupUID = func(name string) (uint32, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	return uint32(uid), err
}

// debugPprofServerAllowed reports whether the process listening on the
// abstract socket may be the daemon. Abstract sockets have no file
// permissions, so any local user could bind @faas-debug/<daemon> first (or
// after a crash) and serve a fabricated profile to root. The listener must be
// root, a system account (daemons run as faas, faas-<daemon> or root), or the
// daemon's own faas-<daemon> account wherever it sits — never a person or a
// tenant jail uid.
func debugPprofServerAllowed(uid uint32, socket string) bool {
	if uid <= debugPprofSystemUIDMax {
		return true
	}
	daemon := strings.TrimPrefix(socket, wire.DebugProfileSocketPrefix)
	// The outgoing generation of a reload listens on <daemon>.<pid>.
	daemon, _, _ = strings.Cut(daemon, ".")
	want, err := debugPprofLookupUID("faas-" + daemon)
	return err == nil && uid == want
}

func debugPprofRefusedServer(socket string, uid uint32) error {
	return fmt.Errorf("refusing @%s: it is held by uid %d, not root or the daemon's service account", socket, uid)
}
