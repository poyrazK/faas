// adr: 1643 — public bridges use the canonical interface, including refills.
package netns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const publicForwardEnv = "FAAS_PUBLIC_FORWARD_TARGET"

// PublicForwardTarget is captured while the original network generation is
// exclusively owned. A helper must never resolve a recycled interface name into
// a new target. HostIP is the public identity; BridgeIP pins the source used by
// the node-only DNAT rules. The guest's network identity remains 10.0.0.2.
type PublicForwardTarget struct {
	HostIP   netip.Addr
	BridgeIP netip.Addr
	Veth     string
	IfIndex  int
	AppPort  int
}

func (t PublicForwardTarget) Validate() error {
	if !t.HostIP.Is4() || t.HostIP.IsUnspecified() || !t.BridgeIP.Is4() || t.BridgeIP.IsUnspecified() || t.Veth == "" || strings.ContainsAny(t.Veth, "/\x00") || t.IfIndex <= 0 || t.IfIndex > api.PublicForwardIfIndexMax || t.AppPort < 1 || t.AppPort > 65535 {
		return errors.New("public forward: incomplete original network identity")
	}
	return nil
}

// PublishedPort inverts the node-only DNAT permutation. :8080 still publishes
// the main workload. Swapping the main port with :8080 preserves access to an
// actual guest :8080 sidecar without allocating another host port.
func (t PublicForwardTarget) PublishedPort(guestPort int) int {
	if guestPort == t.AppPort {
		return AppPort
	}
	if guestPort == AppPort {
		return t.AppPort
	}
	return guestPort
}

// PublicForwardEnv replaces any inherited routing authority. Private helpers
// must call it with nil; a parent environment cannot make them dial publicly.
func PublicForwardEnv(env []string, target *PublicForwardTarget) []string {
	clean := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, publicForwardEnv+"=") {
			clean = append(clean, item)
		}
	}
	if target != nil {
		raw, _ := json.Marshal(target) // this struct contains no fallible values
		clean = append(clean, publicForwardEnv+"="+string(raw))
	}
	return clean
}

// DialBridgeContext is shared by all bridge transports, including H1, H2C,
// upgraded HTTP, TCP and UDP. An invalid/stale public target fails closed; it
// never falls back to the namespace-local guest connection.
func DialBridgeContext(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
	raw, present := os.LookupEnv(publicForwardEnv)
	if !present {
		return d.DialContext(ctx, network, address)
	}
	var target PublicForwardTarget
	if err := json.Unmarshal([]byte(raw), &target); err != nil {
		return nil, fmt.Errorf("public forward target: %w", err)
	}
	return target.DialContext(ctx, d, network, address)
}

func (t PublicForwardTarget) DialContext(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
	return t.dialContext(ctx, d, network, address, net.InterfaceByName)
}

func (t PublicForwardTarget) dialContext(ctx context.Context, d *net.Dialer, network, address string, iface func(string) (*net.Interface, error)) (net.Conn, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	p, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || host != t.HostIP.String() || p < 1 || p > 65535 || (network != "tcp" && network != "udp" && network != "udp4") {
		return nil, errors.New("public forward: unexpected guest destination")
	}
	check := func() error {
		current, err := iface(t.Veth)
		if err != nil || current == nil || current.Index != t.IfIndex {
			return errors.New("public forward: original interface retired")
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, err
	}
	// Copy the caller's timeouts/control hooks without mutating a shared dialer.
	public := *d
	if err := pinPublicOutput(&public, t.IfIndex); err != nil {
		return nil, err
	}
	if network == "tcp" {
		public.LocalAddr = &net.TCPAddr{IP: net.IP(t.BridgeIP.AsSlice())}
		p = t.PublishedPort(p)
	} else {
		public.LocalAddr = &net.UDPAddr{IP: net.IP(t.BridgeIP.AsSlice())}
	}
	conn, err := public.DialContext(ctx, network, net.JoinHostPort(host, strconv.Itoa(p)))
	if err != nil {
		return nil, err
	}
	// Do not hand a connected socket to a caller if retirement/reuse raced
	// connect. A persistent bridge repeats both checks for every new socket.
	if err := check(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
