// PR-2 / ADR-046: per-instance kernel byte counter reader.
//
// netnsReadRXBytesForPoll (cmd/vmmd/network_poller.go) and the
// future schedd-side paths read the cumulative byte counter on
// root-side `vethHost` at
// `/sys/class/net/<vethHost>/statistics/rx_bytes`. The file is
// maintained by the kernel for every network device; the
// counter is the same one the per-plan `tc tbf` qdisc uses, so
// the cap and the meter are consistent (ADR-046 §1).
//
// The read is intentionally a thin shim around os.ReadFile —
// there is no /sys API richer than "read the file", and adding
// an ioctl or netlink path would be a non-trivial kernel
// dependency for a counter that's already exposed as a
// per-device text file.
package netns

import (
	"os"
	"strconv"
	"strings"
)

// ReadVethRXBytes reads the kernel byte counter at
// `/sys/class/net/<vethHost>/statistics/rx_bytes` and returns the
// cumulative byte count. Returns 0 + ErrVethMissing if the veth
// is gone (instance torn down mid-poll); returns 0 + ErrReadFail
// for any other I/O or parse error so the caller can decide
// whether to count the failure.
//
// The shape (cumulative, interface bytes) mirrors /proc/net/dev
// and the `ip -s link show` rx_bytes column. Includes Ethernet
// framing (i.e. the byte count is interface bytes, not IP bytes
// or payload bytes). This canonical unit is preserved through optional
// egress billing; this reader reports the kernel counter verbatim.
func ReadVethRXBytes(vethHost string) (uint64, error) {
	if vethHost == "" {
		return 0, ErrVethMissing
	}
	path := "/sys/class/net/" + vethHost + "/statistics/rx_bytes"
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrVethMissing
		}
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, ErrParseFail
	}
	return n, nil
}

// Sentinel errors for the byte-counter reader. Caller checks via
// errors.Is so a wrapped error from os.ReadFile still classifies
// correctly.
var (
	// ErrVethMissing is returned when the veth's sysfs directory
	// has been torn down between the Manager snapshot and the
	// read. Non-fatal — the next tick sees an empty snapshot and
	// the cache's Forget is called from the vmmd teardown path.
	ErrVethMissing = errSentinel("veth missing")
	// ErrParseFail is returned when the kernel counter file is
	// not a valid uint64 (very rare — the kernel always writes a
	// decimal integer). Counted via ops.EgressSourceErrors so a
	// regression on the format is alerted.
	ErrParseFail = errSentinel("parse fail")
)

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// ReadVethRXBytesForPoll is the function-typed seam the
// network poller (cmd/vmmd/network_poller.go) uses when no
// explicit readRXBytes override is provided. It is a thin
// package-level indirection so tests can swap the production
// reader without touching the package's exported function.
var ReadVethRXBytesForPoll = ReadVethRXBytes

// ReadVethTXBytes reads the kernel byte counter at
// `/sys/class/net/<vethHost>/statistics/tx_bytes` and returns
// the cumulative byte count. The ingress direction — bytes
// root → guest (gateway → vethHost → vethPeer → tap0 → guest).
// Same error contract as ReadVethRXBytes (ErrVethMissing on
// gone, ErrParseFail on malformed content). Added by ADR-048
// to extend the metering surface to ingress bytes (audit
// finding: a customer could otherwise shovel arbitrary bytes
// into a guest for free).
func ReadVethTXBytes(vethHost string) (uint64, error) {
	if vethHost == "" {
		return 0, ErrVethMissing
	}
	path := "/sys/class/net/" + vethHost + "/statistics/tx_bytes"
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrVethMissing
		}
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, ErrParseFail
	}
	return n, nil
}

// ReadVethTXBytesForPoll is the function-typed seam the
// ingress network poller (cmd/vmmd/network_poller.go) uses when
// no explicit readTXBytes override is provided. It mirrors
// ReadVethRXBytesForPoll exactly — same package-level
// indirection so tests can swap the production reader without
// touching the exported function.
var ReadVethTXBytesForPoll = ReadVethTXBytes
