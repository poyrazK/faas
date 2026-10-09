package edgetopology

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrNativeUnverified = errors.New("selected native origin scope unverified")

// NativeHostAddress pins one directly assigned address, not a NAT, floating-IP
// route, anycast site or external reachability assertion (ADR-706).
type NativeHostAddress struct {
	IP        string `json:"ip"`
	Interface string `json:"interface"`
	Index     int    `json:"index"`
	Prefix    int    `json:"prefix"`
}

type NativeHostReview struct {
	MachineID    string              `json:"machine_id"`
	BootID       string              `json:"boot_id"`
	NetNamespace string              `json:"net_namespace"`
	PIDNamespace string              `json:"pid_namespace"`
	Addresses    []NativeHostAddress `json:"addresses"`
}

// NativeServiceReview pins a specific systemd main process and its complete
// held TCP LISTEN address set. It never asserts exclusive socket ownership,
// complete unit membership, UDP/QUIC coverage or unit-file configuration.
type NativeServiceReview struct {
	Unit         string   `json:"unit"`
	InvocationID string   `json:"invocation_id"`
	PID          int      `json:"pid"`
	StartTicks   uint64   `json:"start_ticks"`
	UID          uint32   `json:"uid"`
	ExeSHA256    string   `json:"exe_sha256"`
	Cgroup       string   `json:"cgroup"`
	CgroupInode  uint64   `json:"cgroup_inode"`
	TCPListeners []string `json:"tcp_listeners"`
}

type NativeScopeReview struct {
	Host     NativeHostReview      `json:"host"`
	Services []NativeServiceReview `json:"services"`
}

type NativeFileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	SHA256 string `json:"sha256,omitempty"`
}

type NativeTCPListener struct {
	Address string `json:"address"`
	Inode   uint64 `json:"inode"`
	FDs     []int  `json:"fds"`
}

type NativeServiceObservation struct {
	Review     NativeServiceReview `json:"review"`
	Executable NativeFileIdentity  `json:"executable"`
	Cgroup     NativeFileIdentity  `json:"cgroup_identity"`
	Listeners  []NativeTCPListener `json:"listeners"`
}

// NativeScopeObservation is a read-only selected host/process snapshot. Socket
// activation may leave other holders, including PID 1. No future lease, drain,
// dead-process receipt or traffic/retirement authority follows from this type.
type NativeScopeObservation struct {
	Host      NativeHostReview           `json:"host"`
	Services  []NativeServiceObservation `json:"services"`
	CheckedAt time.Time                  `json:"checked_at"`
}

type nativeScopeSession interface {
	Capture(context.Context, NativeScopeReview) (NativeScopeObservation, error)
	Close() error
}

func freezeNativeScope(review NativeScopeReview) (NativeScopeReview, error) {
	boot, err := uuid.Parse(review.Host.BootID)
	if !nativeID128(review.Host.MachineID) || err != nil || boot == uuid.Nil || boot.String() != review.Host.BootID || !nativeNamespace(review.Host.NetNamespace, "net") || !nativeNamespace(review.Host.PIDNamespace, "pid") || len(review.Services) < 1 || len(review.Services) > api.RuntimeUpgradeNativeServiceLimit || len(review.Host.Addresses) < 1 || len(review.Host.Addresses) > api.RuntimeUpgradeNativeOriginLimit {
		return NativeScopeReview{}, fmt.Errorf("%w: canonical bounded host and service review required", ErrNativeUnverified)
	}
	frozen := NativeScopeReview{Host: review.Host}
	frozen.Host.Addresses = slices.Clone(review.Host.Addresses)
	addresses := make(map[string]bool)
	for _, a := range frozen.Host.Addresses {
		ip, err := netip.ParseAddr(a.IP)
		if err != nil || ip.String() != a.IP || ip.Zone() != "" || ip.Is4In6() || ip.IsUnspecified() || ip.IsMulticast() || a.Index < 1 || a.Prefix < 0 || a.Prefix > ip.BitLen() || !nativeInterfaceName(a.Interface) || addresses[a.IP] {
			return NativeScopeReview{}, fmt.Errorf("%w: distinct directly assigned canonical addresses required", ErrNativeUnverified)
		}
		addresses[a.IP] = true
	}
	slices.SortFunc(frozen.Host.Addresses, func(a, b NativeHostAddress) int { return strings.Compare(a.IP, b.IP) })
	units, pids, listeners := make(map[string]bool), make(map[int]bool), make(map[string]bool)
	for _, service := range review.Services {
		if !nativeUnit(service.Unit) || units[service.Unit] || service.PID < 2 || pids[service.PID] || service.StartTicks == 0 || !nativeID128(service.InvocationID) || !canonicalDigest(service.ExeSHA256) || !nativeCgroup(service.Cgroup, service.Unit) || service.CgroupInode == 0 || len(service.TCPListeners) < 1 || len(service.TCPListeners) > api.RuntimeUpgradeNativeListenerLimit {
			return NativeScopeReview{}, fmt.Errorf("%w: distinct pinned main-process service identities required", ErrNativeUnverified)
		}
		units[service.Unit], pids[service.PID] = true, true
		service.TCPListeners = slices.Clone(service.TCPListeners)
		for _, address := range service.TCPListeners {
			if !nativeTCPAddress(address) || listeners[address] {
				return NativeScopeReview{}, fmt.Errorf("%w: distinct canonical literal TCP listener addresses required", ErrNativeUnverified)
			}
			listeners[address] = true
		}
		slices.Sort(service.TCPListeners)
		frozen.Services = append(frozen.Services, service)
	}
	if len(listeners) > api.RuntimeUpgradeNativeListenerLimit {
		return NativeScopeReview{}, fmt.Errorf("%w: native listener bound exceeded", ErrNativeUnverified)
	}
	slices.SortFunc(frozen.Services, func(a, b NativeServiceReview) int { return strings.Compare(a.Unit, b.Unit) })
	return frozen, nil
}

func nativeID128(value string) bool {
	return len(value) == 32 && value != strings.Repeat("0", 32) && strings.IndexFunc(value, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) == -1
}

func nativeNamespace(value, kind string) bool {
	if !strings.HasPrefix(value, kind+":[") || !strings.HasSuffix(value, "]") {
		return false
	}
	_, ok := nativeDecimal(value[len(kind)+2 : len(value)-1])
	return ok
}

func nativeDecimal(value string) (uint64, bool) {
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatUint(n, 10) == value
}

func nativeUnit(value string) bool {
	return nativeUnitSuffix(value, ".service")
}

func nativeUnitSuffix(value, suffix string) bool {
	return strings.HasSuffix(value, suffix) && len(value) <= api.RuntimeUpgradePublicEdgeCaddyNameMaxBytes && len(value) > len(suffix) && value[0] != '-' && strings.IndexFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' && r != '@'
	}) == -1
}

func nativeCgroup(value, unit string) bool {
	return strings.HasPrefix(value, "/") && path.Clean(value) == value && path.Base(value) == unit && len(value) <= api.RuntimeUpgradePublicEdgeConfigPathMaxBytes && strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e || r == '\\' }) == -1
}

func nativeInterfaceName(value string) bool {
	return len(value) > 0 && len(value) <= api.RuntimeUpgradeNativeInterfaceNameMaxBytes && strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e || r == '/' || r == ':' }) == -1
}

func nativeTCPAddress(value string) bool {
	a, err := netip.ParseAddrPort(value)
	return err == nil && a.String() == value && a.Port() != 0 && a.Addr().Zone() == "" && !a.Addr().Is4In6() && !a.Addr().IsMulticast()
}
