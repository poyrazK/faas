package wire

// The portable half of pre-traffic checks for the two-generation hand-off
// (docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md): the check
// definitions, the rejection a deploy reads, and the successor's side, which
// serves the predecessor's check connections from its ready gates
// (handoff_gate.go) before it accepts traffic. handoff_check.go sends them.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// handoffCheckFDsEnv describes the check connections passed to a successor:
// a JSON list of {"fd", "network", "address"}.
const handoffCheckFDsEnv = "FAAS_HANDOFF_CHECK_FDS"

// HandoffChecksSkipFile, when it exists, makes a hand-off skip its
// pre-traffic checks: the break-glass for a check that wrongly rejects every
// successor. The checks are the running generation's, so only a hand-off
// without them, or a restart, replaces it. /run is tmpfs: a reboot clears it.
const HandoffChecksSkipFile = "/run/faas/handoff-checks.skip"

// handoffChecksSkipFile is HandoffChecksSkipFile; tests point it elsewhere.
var handoffChecksSkipFile = HandoffChecksSkipFile

// handoffCheckDefaultTimeout bounds one check: dial-free socket pair, one
// request, one response.
const handoffCheckDefaultTimeout = 10 * time.Second

// handoffCheckHeader marks a check request in the successor's logs.
const handoffCheckHeader = "X-Gregale-Handoff-Check"

// ErrHandoffRejected wraps a successor that failed a pre-traffic check.
var ErrHandoffRejected = errors.New("rejected by pre-traffic check")

// HandoffCheck is one request the old generation sends its successor before
// the successor accepts traffic.
type HandoffCheck struct {
	// Name labels the check in reports ("readyz", "api").
	Name string
	// Network ("tcp" or "unix") and Address name the shared listener the
	// request goes to, as the daemon binds it; matched like an inherited
	// socket (an unspecified host matches any).
	Network, Address string
	// Method (default GET), Path and Host (default "localhost") make the
	// request.
	Method, Path, Host string
	// Want lists the status codes that pass.
	Want []int
	// Timeout bounds the request (default 10 s).
	Timeout time.Duration
}

// HandoffRejected reports whether a unit's STATUS= text says its last
// hand-off was rejected by a pre-traffic check (cmd/deployctl). The text
// keeps HandoffStatusFailed's prefix, so a deployctl that predates checks
// treats it as a failed hand-off.
func HandoffRejected(status string) bool {
	return strings.HasPrefix(status, HandoffStatusFailed) && strings.Contains(status, ErrHandoffRejected.Error())
}

// checkFD is one entry of handoffCheckFDsEnv.
type checkFD struct {
	FD      int    `json:"fd"`
	Network string `json:"network"`
	Address string `json:"address"`
}

// --- the successor's side ---------------------------------------------------

// handoffProbe is a check connection the successor serves before ready.
type handoffProbe struct {
	spec  checkFD
	taken bool
}

// loadHandoffProbes reads handoffCheckFDsEnv.
func loadHandoffProbes(lookup func(string) (string, bool)) ([]*handoffProbe, error) {
	raw, ok := lookup(handoffCheckFDsEnv)
	if !ok || raw == "" {
		return nil, nil
	}
	var specs []checkFD
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		return nil, fmt.Errorf("wire: invalid %s: %w", handoffCheckFDsEnv, err)
	}
	probes := make([]*handoffProbe, 0, len(specs))
	for _, s := range specs {
		if s.FD < activationFirstFD {
			return nil, fmt.Errorf("wire: invalid %s fd %d", handoffCheckFDsEnv, s.FD)
		}
		probes = append(probes, &handoffProbe{spec: s})
	}
	return probes, nil
}

// probeConn reports addresses for a check connection: a socket pair has
// none, and middleware that reads the client address must see a local one.
type probeConn struct {
	net.Conn
	local net.Addr
}

func (c probeConn) LocalAddr() net.Addr { return c.local }

func (c probeConn) RemoteAddr() net.Addr {
	if _, ok := c.local.(*net.UnixAddr); ok {
		return &net.UnixAddr{Name: "@handoff-check", Net: "unix"}
	}
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

// probesFor opens the check connections meant for sock's listener.
func (s *activationSet) probesFor(sock *inheritedSocket, local net.Addr) []net.Conn {
	var conns []net.Conn
	for _, p := range s.probes {
		if p.taken || !inheritedMatches(sock, p.spec.Network, p.spec.Address) {
			continue
		}
		p.taken = true
		f := os.NewFile(uintptr(p.spec.FD), "handoff-check")
		if f == nil {
			continue
		}
		conn, err := net.FileConn(f)
		_ = f.Close()
		if err != nil {
			continue
		}
		conns = append(conns, probeConn{Conn: conn, local: local})
	}
	return conns
}
