package edgetopology

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The only production implementation uses fixed local Linux paths and retained
// pidfds. The unexported interface also permits portable synthetic contracts.
type nativeScopeReader interface {
	nativeHostReader
	Entries(string, int) ([]string, error)
	Executable(context.Context, string) (NativeFileIdentity, error)
	Cgroup(string) (NativeFileIdentity, error)
	Unit(context.Context, string) ([]byte, error)
	Alive() error
}

type nativeHostReader interface {
	Read(context.Context, string, int) ([]byte, error)
	Link(string) (string, error)
	Addresses(context.Context) ([]NativeHostAddress, error)
}

func captureNativeScope(ctx context.Context, reader nativeScopeReader, review NativeScopeReview) (NativeScopeObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeNativeScopeTimeout)
	defer cancel()
	if reader.Alive() != nil || checkNativeHost(ctx, reader, review.Host) != nil {
		return NativeScopeObservation{}, fmt.Errorf("%w: host or retained process identity", ErrNativeUnverified)
	}
	var tcp []nativeTCPRow
	for _, family := range []string{"tcp", "tcp6"} {
		raw, err := reader.Read(ctx, "self/net/"+family, api.RuntimeUpgradeNativeProcMaxBytes)
		if err != nil {
			return NativeScopeObservation{}, fmt.Errorf("%w: complete native TCP table unavailable", ErrNativeUnverified)
		}
		rows, err := parseNativeTCP(raw, family == "tcp6")
		if err != nil || len(tcp)+len(rows) > api.RuntimeUpgradeNativeTCPRowLimit {
			return NativeScopeObservation{}, fmt.Errorf("%w: bounded native TCP table required", ErrNativeUnverified)
		}
		tcp = append(tcp, rows...)
	}
	observation := NativeScopeObservation{Host: review.Host}
	fdBudget := api.RuntimeUpgradeNativeFDLimit
	for _, service := range review.Services {
		got, err := captureNativeService(ctx, reader, review.Host, service, tcp, &fdBudget)
		if err != nil {
			return NativeScopeObservation{}, err
		}
		observation.Services = append(observation.Services, got)
	}
	if reader.Alive() != nil || checkNativeHost(ctx, reader, review.Host) != nil || ctx.Err() != nil {
		return NativeScopeObservation{}, fmt.Errorf("%w: host/process changed during capture", ErrNativeUnverified)
	}
	observation.CheckedAt = time.Now().UTC()
	return observation, nil
}

func checkNativeHost(ctx context.Context, reader nativeHostReader, host NativeHostReview) error {
	for _, item := range []struct{ file, value string }{{"machine-id", host.MachineID}, {"sys/kernel/random/boot_id", host.BootID}} {
		raw, err := reader.Read(ctx, item.file, api.RuntimeUpgradeNativeMetadataMaxBytes)
		if err != nil || strings.TrimSuffix(string(raw), "\n") != item.value {
			return ErrNativeUnverified
		}
	}
	for _, item := range []struct{ file, value string }{{"self/ns/net", host.NetNamespace}, {"1/ns/net", host.NetNamespace}, {"self/ns/pid", host.PIDNamespace}, {"1/ns/pid", host.PIDNamespace}} {
		value, err := reader.Link(item.file)
		if err != nil || value != item.value {
			return ErrNativeUnverified
		}
	}
	addresses, err := reader.Addresses(ctx)
	if err != nil {
		return ErrNativeUnverified
	}
	for _, wanted := range host.Addresses {
		matches := 0
		for _, actual := range addresses {
			if actual.IP == wanted.IP {
				if actual != wanted {
					return ErrNativeUnverified
				}
				matches++
			}
		}
		if matches != 1 {
			return ErrNativeUnverified
		}
	}
	return ctx.Err()
}

func captureNativeService(ctx context.Context, reader nativeScopeReader, host NativeHostReview, service NativeServiceReview, tcp []nativeTCPRow, fdBudget *int) (NativeServiceObservation, error) {
	before, err := checkNativeService(ctx, reader, host, service)
	if err != nil {
		return NativeServiceObservation{}, err
	}
	base := strconv.Itoa(service.PID) + "/"
	entries, err := reader.Entries(base+"fd", *fdBudget)
	if err != nil || len(entries) > *fdBudget {
		return NativeServiceObservation{}, fmt.Errorf("%w: complete bounded process descriptor set required", ErrNativeUnverified)
	}
	*fdBudget -= len(entries)
	fds := make(map[uint64][]int)
	seenFDs := make(map[string]bool)
	for _, entry := range entries {
		if ctx.Err() != nil {
			return NativeServiceObservation{}, nativeReadError("descriptor enumeration context")
		}
		fd, err := strconv.ParseUint(entry, 10, 31)
		if err != nil || strconv.FormatUint(fd, 10) != entry || seenFDs[entry] {
			return NativeServiceObservation{}, fmt.Errorf("%w: canonical descriptor set required", ErrNativeUnverified)
		}
		seenFDs[entry] = true
		link, err := reader.Link(base + "fd/" + entry)
		if err != nil {
			return NativeServiceObservation{}, fmt.Errorf("%w: descriptor disappeared or unreadable", ErrNativeUnverified)
		}
		if strings.HasPrefix(link, "socket:") {
			if !nativeNamespace(link, "socket") {
				return NativeServiceObservation{}, fmt.Errorf("%w: malformed socket identity", ErrNativeUnverified)
			}
			inode, _ := nativeDecimal(link[len("socket:[") : len(link)-1])
			fds[inode] = append(fds[inode], int(fd))
		}
	}
	listeners, err := matchNativeListeners(tcp, fds, service.TCPListeners)
	if err != nil {
		return NativeServiceObservation{}, err
	}
	after, err := checkNativeService(ctx, reader, host, service)
	if err != nil || before.Executable != after.Executable || before.Cgroup != after.Cgroup || reader.Alive() != nil || ctx.Err() != nil {
		return NativeServiceObservation{}, fmt.Errorf("%w: service/process changed during capture", ErrNativeUnverified)
	}
	before.Listeners = listeners
	return before, nil
}

func checkNativeService(ctx context.Context, reader nativeScopeReader, host NativeHostReview, service NativeServiceReview) (NativeServiceObservation, error) {
	raw, err := reader.Unit(ctx, service.Unit)
	if err != nil || matchNativeUnit(raw, service) != nil {
		return NativeServiceObservation{}, fmt.Errorf("%w: active exact service invocation required", ErrNativeUnverified)
	}
	base := strconv.Itoa(service.PID) + "/"
	stat, err := reader.Read(ctx, base+"stat", api.RuntimeUpgradeNativeMetadataMaxBytes)
	if err != nil || matchNativeStat(stat, service.PID, service.StartTicks) != nil {
		return NativeServiceObservation{}, fmt.Errorf("%w: exact live main-process start required", ErrNativeUnverified)
	}
	status, err := reader.Read(ctx, base+"status", api.RuntimeUpgradeNativeMetadataMaxBytes)
	if err != nil || matchNativeUID(status, service.UID) != nil {
		return NativeServiceObservation{}, fmt.Errorf("%w: exact process credentials required", ErrNativeUnverified)
	}
	cgroup, err := reader.Read(ctx, base+"cgroup", api.RuntimeUpgradeNativeMetadataMaxBytes)
	if err != nil || string(cgroup) != "0::"+service.Cgroup+"\n" {
		return NativeServiceObservation{}, fmt.Errorf("%w: exact unified service cgroup required", ErrNativeUnverified)
	}
	for _, item := range []struct{ name, want string }{{"net", host.NetNamespace}, {"pid", host.PIDNamespace}} {
		got, err := reader.Link(base + "ns/" + item.name)
		if err != nil || got != item.want {
			return NativeServiceObservation{}, fmt.Errorf("%w: process namespace differs from reviewed host", ErrNativeUnverified)
		}
	}
	exe, err := reader.Executable(ctx, base+"exe")
	if err != nil || exe.SHA256 != service.ExeSHA256 || exe.Inode == 0 {
		return NativeServiceObservation{}, fmt.Errorf("%w: exact executable identity required", ErrNativeUnverified)
	}
	cgroupID, err := reader.Cgroup(service.Cgroup)
	if err != nil || cgroupID.Inode != service.CgroupInode || cgroupID.SHA256 != "" {
		return NativeServiceObservation{}, fmt.Errorf("%w: exact native cgroup identity required", ErrNativeUnverified)
	}
	return NativeServiceObservation{Review: service, Executable: exe, Cgroup: cgroupID}, nil
}

func matchNativeListeners(rows []nativeTCPRow, fds map[uint64][]int, expected []string) ([]NativeTCPListener, error) {
	byAddress, byInode := make(map[string]bool), make(map[uint64]bool)
	var listeners []NativeTCPListener
	for _, row := range rows {
		if !row.Listening {
			continue
		}
		if len(fds[row.Inode]) == 0 {
			continue
		}
		if byAddress[row.Address] || byInode[row.Inode] || !slices.Contains(expected, row.Address) {
			return nil, fmt.Errorf("%w: unexpected or ambiguous held TCP listener", ErrNativeUnverified)
		}
		byAddress[row.Address], byInode[row.Inode] = true, true
		owned := slices.Clone(fds[row.Inode])
		slices.Sort(owned)
		listeners = append(listeners, NativeTCPListener{Address: row.Address, Inode: row.Inode, FDs: owned})
	}
	if len(listeners) != len(expected) {
		return nil, fmt.Errorf("%w: reviewed TCP listener not held by main process", ErrNativeUnverified)
	}
	// Other processes may hold the SAME activated socket inode. A separate
	// overlapping LISTEN inode cannot borrow this selected identity observation.
	for _, row := range rows {
		if row.Listening && !byInode[row.Inode] {
			for address := range byAddress {
				if nativeTCPOverlap(row.Address, address) {
					return nil, fmt.Errorf("%w: competing TCP listener inode", ErrNativeUnverified)
				}
			}
		}
	}
	slices.SortFunc(listeners, func(a, b NativeTCPListener) int { return strings.Compare(a.Address, b.Address) })
	return listeners, nil
}

func sameNativeScope(a, b NativeScopeObservation) bool {
	a.CheckedAt, b.CheckedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}
