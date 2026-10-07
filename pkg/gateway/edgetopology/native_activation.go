package edgetopology

import (
	"context"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// NativeActivationReview pins a previously reviewed service identity and its
// selected activation units. The caller owns historical startup attribution;
// this audit neither reconstructs it nor proves every activation path (ADR-621).
type NativeActivationReview struct {
	Host    NativeHostReview    `json:"host"`
	Service NativeServiceReview `json:"service"`
	Sockets []string            `json:"sockets"`
}

type NativeMaskedUnit struct {
	Unit string             `json:"unit"`
	Mask NativeFileIdentity `json:"persistent_mask"`
}

type NativeEmptyCgroup struct {
	Present  bool               `json:"present"`
	Identity NativeFileIdentity `json:"identity"`
}

type NativeActivationSnapshot struct {
	Units     []NativeMaskedUnit `json:"units"`
	Cgroup    NativeEmptyCgroup  `json:"cgroup"`
	CheckedAt time.Time          `json:"checked_at"`
}

// NativeActivationObservation contains two equal read-only snapshots. It is
// deliberately distinct from a withdrawal seal or external fence receipt: root
// can remove masks, observations have no lease, and escaped processes, other
// namespaces, UDP and network bypasses remain outside this selected scope.
type NativeActivationObservation struct {
	Review    NativeActivationReview     `json:"review"`
	Snapshots []NativeActivationSnapshot `json:"snapshots"`
	CheckedAt time.Time                  `json:"checked_at"`
}

type nativeActivationSession interface {
	Capture(context.Context, NativeActivationReview) (NativeActivationSnapshot, error)
	Close() error
}

type NativeActivationProbe struct {
	open func(context.Context, NativeActivationReview) (nativeActivationSession, error)
}

func NewNativeActivationProbe() *NativeActivationProbe {
	return &NativeActivationProbe{open: newNativeActivationSession}
}

func (p *NativeActivationProbe) Observe(ctx context.Context, review NativeActivationReview) (NativeActivationObservation, error) {
	frozen, err := freezeNativeActivation(review)
	if err != nil || p == nil || p.open == nil || ctx.Err() != nil {
		return NativeActivationObservation{}, nativeReadError("activation review or collector")
	}
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeNativeActivationTimeout)
	defer cancel()
	session, err := p.open(ctx, frozen)
	if err != nil {
		return NativeActivationObservation{}, nativeReadError("local activation scope")
	}
	defer func() { _ = session.Close() }()
	a, err := session.Capture(ctx, frozen)
	if err != nil {
		return NativeActivationObservation{}, nativeReadError("first activation snapshot")
	}
	b, err := session.Capture(ctx, frozen)
	if err != nil || !sameNativeActivation(a, b) || ctx.Err() != nil {
		return NativeActivationObservation{}, nativeReadError("activation changed during observation")
	}
	return NativeActivationObservation{Review: frozen, Snapshots: []NativeActivationSnapshot{a, b}, CheckedAt: time.Now().UTC()}, nil
}

func freezeNativeActivation(review NativeActivationReview) (NativeActivationReview, error) {
	scope, err := freezeNativeScope(NativeScopeReview{Host: review.Host, Services: []NativeServiceReview{review.Service}})
	if err != nil || len(review.Sockets) < 1 || len(review.Sockets) > api.RuntimeUpgradeNativeActivationSocketLimit {
		return NativeActivationReview{}, fmt.Errorf("%w: bounded historical service and selected sockets required", ErrNativeUnverified)
	}
	frozen := NativeActivationReview{Host: scope.Host, Service: scope.Services[0], Sockets: slices.Clone(review.Sockets)}
	slices.Sort(frozen.Sockets)
	for i, unit := range frozen.Sockets {
		if !nativeUnitSuffix(unit, ".socket") || (i > 0 && frozen.Sockets[i-1] == unit) {
			return NativeActivationReview{}, nativeReadError("distinct canonical socket units")
		}
	}
	return frozen, nil
}

func sameNativeActivation(a, b NativeActivationSnapshot) bool {
	a.CheckedAt, b.CheckedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

type nativeActivationReader interface {
	nativeHostReader
	MaskedUnit(context.Context, string) (NativeMaskedUnit, error)
	EmptyCgroup(context.Context, NativeServiceReview) (NativeEmptyCgroup, error)
}

func captureNativeActivation(ctx context.Context, reader nativeActivationReader, review NativeActivationReview) (NativeActivationSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeNativeScopeTimeout)
	defer cancel()
	if checkNativeHost(ctx, reader, review.Host) != nil {
		return NativeActivationSnapshot{}, nativeReadError("activation host identity")
	}
	before, err := captureNativeActivationUnits(ctx, reader, review)
	if err != nil {
		return NativeActivationSnapshot{}, err
	}
	rows := 0
	for _, family := range []string{"tcp", "tcp6"} {
		raw, err := reader.Read(ctx, "self/net/"+family, api.RuntimeUpgradeNativeProcMaxBytes)
		if err != nil {
			return NativeActivationSnapshot{}, nativeReadError("complete activation TCP tables")
		}
		table, err := parseNativeTCP(raw, family == "tcp6")
		rows += len(table)
		if err != nil || rows > api.RuntimeUpgradeNativeTCPRowLimit {
			return NativeActivationSnapshot{}, nativeReadError("bounded activation TCP table")
		}
		// Check ALL states, including established/upgraded traffic and TIME_WAIT.
		// This also catches listeners held by PID 1 or another process, without
		// relying on a disappeared main PID or an incomplete holder census.
		for _, row := range table {
			local, err := netip.ParseAddrPort(row.Address)
			if err != nil {
				return NativeActivationSnapshot{}, nativeReadError("canonical activation TCP endpoint")
			}
			// Accepted dual-stack connections can appear as IPv4-mapped tcp6
			// addresses. Match their IPv4 identity rather than hiding them.
			address := netip.AddrPortFrom(local.Addr().Unmap(), local.Port()).String()
			for _, selected := range review.Service.TCPListeners {
				if nativeTCPOverlap(address, selected) {
					return NativeActivationSnapshot{}, nativeReadError("selected TCP endpoint still present")
				}
			}
		}
	}
	after, err := captureNativeActivationUnits(ctx, reader, review)
	if err != nil || !sameNativeActivation(before, after) || checkNativeHost(ctx, reader, review.Host) != nil || ctx.Err() != nil {
		return NativeActivationSnapshot{}, nativeReadError("activation host, mask or cgroup changed")
	}
	before.CheckedAt = time.Now().UTC()
	return before, nil
}

func captureNativeActivationUnits(ctx context.Context, reader nativeActivationReader, review NativeActivationReview) (NativeActivationSnapshot, error) {
	var result NativeActivationSnapshot
	for _, unit := range append([]string{review.Service.Unit}, review.Sockets...) {
		got, err := reader.MaskedUnit(ctx, unit)
		if err != nil || got.Unit != unit || got.Mask.Inode == 0 || got.Mask.SHA256 != "" {
			return NativeActivationSnapshot{}, nativeReadError("inactive persistently masked unit")
		}
		result.Units = append(result.Units, got)
	}
	group, err := reader.EmptyCgroup(ctx, review.Service)
	if err != nil || (group.Present && (group.Identity.Inode != review.Service.CgroupInode || group.Identity.SHA256 != "")) || (!group.Present && group.Identity != (NativeFileIdentity{})) {
		return NativeActivationSnapshot{}, nativeReadError("historical cgroup absent or recursively empty")
	}
	result.Cgroup = group
	return result, nil
}

func matchNativeMaskedUnit(raw []byte, unit string) error {
	expected := map[string]string{"Id": unit, "LoadState": "masked", "ActiveState": "inactive", "SubState": "dead", "UnitFileState": "masked", "NeedDaemonReload": "no", "Job": "", "ControlPID": "0", "ControlGroup": ""}
	if nativeUnit(unit) {
		expected["MainPID"], expected["NFileDescriptorStore"] = "0", "0"
	} else if !nativeUnitSuffix(unit, ".socket") {
		return ErrNativeUnverified
	}
	return matchNativeProperties(raw, expected)
}

func matchNativeProperties(raw []byte, expected map[string]string) error {
	if len(raw) == 0 || len(raw) > api.RuntimeUpgradeNativeUnitMaxBytes || raw[len(raw)-1] != '\n' {
		return ErrNativeUnverified
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		want, exists := expected[key]
		if !ok || !exists || seen[key] || value != want {
			return ErrNativeUnverified
		}
		seen[key] = true
	}
	if len(seen) != len(expected) {
		return ErrNativeUnverified
	}
	return nil
}

func matchNativeEmptyEvents(raw []byte) error {
	if len(raw) == 0 || len(raw) > api.RuntimeUpgradeNativeMetadataMaxBytes || raw[len(raw)-1] != '\n' {
		return ErrNativeUnverified
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || seen[fields[0]] || (fields[0] != "populated" && fields[0] != "frozen") || fields[1] != "0" {
			return ErrNativeUnverified
		}
		seen[fields[0]] = true
	}
	if !seen["populated"] || !seen["frozen"] {
		return ErrNativeUnverified
	}
	return nil
}
