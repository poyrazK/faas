package edgetopology

// adr: 620

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type nativeFixtureReader struct {
	reads       map[string][]byte
	links       map[string]string
	entries     map[string][]string
	executables map[string]NativeFileIdentity
	cgroups     map[string]NativeFileIdentity
	units       map[string][]byte
	addresses   []NativeHostAddress
	calls       map[string]int
	fail        string
	hook        func(string, int)
	captureHook func(int)
	captures    int
	closes      int
}

func (r *nativeFixtureReader) call(name string) error {
	r.calls[name]++
	if r.hook != nil {
		r.hook(name, r.calls[name])
	}
	if r.fail == name {
		return errors.New("secret-arbitrary-native-error")
	}
	return nil
}

func (r *nativeFixtureReader) Read(ctx context.Context, name string, limit int) ([]byte, error) {
	if err := r.call("read:" + name); err != nil {
		return nil, err
	}
	value, ok := r.reads[name]
	if !ok || len(value) > limit || ctx.Err() != nil {
		return nil, ErrNativeUnverified
	}
	return slices.Clone(value), nil
}

func (r *nativeFixtureReader) Link(name string) (string, error) {
	if err := r.call("link:" + name); err != nil {
		return "", err
	}
	value, ok := r.links[name]
	if !ok {
		return "", ErrNativeUnverified
	}
	return value, nil
}

func (r *nativeFixtureReader) Entries(name string, limit int) ([]string, error) {
	if err := r.call("entries:" + name); err != nil {
		return nil, err
	}
	value, ok := r.entries[name]
	if !ok || len(value) > limit {
		return nil, ErrNativeUnverified
	}
	return slices.Clone(value), nil
}

func (r *nativeFixtureReader) Executable(ctx context.Context, name string) (NativeFileIdentity, error) {
	if err := r.call("exe:" + name); err != nil {
		return NativeFileIdentity{}, err
	}
	value, ok := r.executables[name]
	if !ok || ctx.Err() != nil {
		return NativeFileIdentity{}, ErrNativeUnverified
	}
	return value, nil
}

func (r *nativeFixtureReader) Cgroup(name string) (NativeFileIdentity, error) {
	if err := r.call("cgroup:" + name); err != nil {
		return NativeFileIdentity{}, err
	}
	value, ok := r.cgroups[name]
	if !ok {
		return NativeFileIdentity{}, ErrNativeUnverified
	}
	return value, nil
}

func (r *nativeFixtureReader) Unit(ctx context.Context, name string) ([]byte, error) {
	if err := r.call("unit:" + name); err != nil {
		return nil, err
	}
	value, ok := r.units[name]
	if !ok || ctx.Err() != nil {
		return nil, ErrNativeUnverified
	}
	return slices.Clone(value), nil
}

func (r *nativeFixtureReader) Addresses(ctx context.Context) ([]NativeHostAddress, error) {
	if err := r.call("addresses"); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return slices.Clone(r.addresses), nil
}

func (r *nativeFixtureReader) Alive() error { return r.call("alive") }
func (r *nativeFixtureReader) Close() error { r.closes++; return nil }
func (r *nativeFixtureReader) Capture(ctx context.Context, review NativeScopeReview) (NativeScopeObservation, error) {
	r.captures++
	if r.captureHook != nil {
		r.captureHook(r.captures)
	}
	return captureNativeScope(ctx, r, review)
}

func nativeTestScope(listen, admin string) NativeScopeReview {
	return NativeScopeReview{
		Host: NativeHostReview{MachineID: strings.Repeat("1", 32), BootID: "671f3d34-377e-4c3f-b9a1-06c9a6d606f4", NetNamespace: "net:[4026531992]", PIDNamespace: "pid:[4026531836]", Addresses: []NativeHostAddress{
			{IP: "192.0.2.10", Interface: "eth0", Index: 2, Prefix: 24}, {IP: "192.0.2.11", Interface: "eth0", Index: 2, Prefix: 24}, {IP: "2001:db8::10", Interface: "eth0", Index: 2, Prefix: 64},
		}},
		Services: []NativeServiceReview{
			{Unit: "caddy.service", InvocationID: strings.Repeat("2", 32), PID: 101, StartTicks: 456, UID: 997, ExeSHA256: strings.Repeat("c", 64), Cgroup: "/system.slice/caddy.service", CgroupInode: 201, TCPListeners: []string{"0.0.0.0:443", "[::]:443", admin}},
			{Unit: "faas-gatewayd-public.service", InvocationID: strings.Repeat("3", 32), PID: 102, StartTicks: 789, UID: 998, ExeSHA256: strings.Repeat("d", 64), Cgroup: "/faas-cp.slice/faas-gatewayd-public.service", CgroupInode: 202, TCPListeners: []string{listen, "127.0.0.1:9092"}},
		},
	}
}

func nativeUnitBody(s NativeServiceReview) []byte {
	return []byte(fmt.Sprintf("Id=%s\nLoadState=loaded\nActiveState=active\nSubState=running\nMainPID=%d\nControlGroup=%s\nInvocationID=%s\nNeedDaemonReload=no\n", s.Unit, s.PID, s.Cgroup, s.InvocationID))
}

func nativeStatBody(s NativeServiceReview) []byte {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0], fields[19] = "S", strconv.FormatUint(s.StartTicks, 10)
	return []byte(fmt.Sprintf("%d (name ) with ( parens) %s\n", s.PID, strings.Join(fields, " ")))
}

const nativeTCPHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n"

func nativeHexEndpoint(address string) string {
	a := netip.MustParseAddrPort(address)
	raw := a.Addr().AsSlice()
	var words []string
	for i := 0; i < len(raw); i += 4 {
		words = append(words, fmt.Sprintf("%08X", binary.NativeEndian.Uint32(raw[i:i+4])))
	}
	return strings.Join(words, "") + fmt.Sprintf(":%04X", a.Port())
}

func nativeTCPLine(address string, inode uint64, state string) string {
	a := netip.MustParseAddrPort(address)
	remote := "00000000:0000"
	if a.Addr().Is6() {
		remote = "00000000000000000000000000000000:0000"
	}
	return fmt.Sprintf("  0: %s %s %s 00000000:00000000 00:00000000 00000000 0 0 %d 1 0 100 0 0 10 0\n", nativeHexEndpoint(address), remote, state, inode)
}

func newNativeFixtureReader(t *testing.T, scope NativeScopeReview) *nativeFixtureReader {
	t.Helper()
	r := &nativeFixtureReader{reads: make(map[string][]byte), links: make(map[string]string), entries: make(map[string][]string), executables: make(map[string]NativeFileIdentity), cgroups: make(map[string]NativeFileIdentity), units: make(map[string][]byte), calls: make(map[string]int), addresses: slices.Clone(scope.Host.Addresses)}
	r.reads["machine-id"], r.reads["sys/kernel/random/boot_id"] = []byte(scope.Host.MachineID+"\n"), []byte(scope.Host.BootID+"\n")
	for _, base := range []string{"self/", "1/"} {
		r.links[base+"ns/net"], r.links[base+"ns/pid"] = scope.Host.NetNamespace, scope.Host.PIDNamespace
	}
	v4, v6 := nativeTCPHeader, nativeTCPHeader
	for i, service := range scope.Services {
		base := strconv.Itoa(service.PID) + "/"
		r.reads[base+"stat"] = nativeStatBody(service)
		r.reads[base+"status"] = []byte(fmt.Sprintf("Name:\tfixture\nUid:\t%d\t%d\t%d\t%d\n", service.UID, service.UID, service.UID, service.UID))
		r.reads[base+"cgroup"] = []byte("0::" + service.Cgroup + "\n")
		r.links[base+"ns/net"], r.links[base+"ns/pid"] = scope.Host.NetNamespace, scope.Host.PIDNamespace
		r.units[service.Unit] = nativeUnitBody(service)
		r.executables[base+"exe"] = NativeFileIdentity{Device: 1, Inode: uint64(301 + i), SHA256: service.ExeSHA256}
		r.cgroups[service.Cgroup] = NativeFileIdentity{Device: 2, Inode: service.CgroupInode}
		r.entries[base+"fd"] = []string{"0", "1"}
		r.links[base+"fd/0"], r.links[base+"fd/1"] = "/dev/null", "socket:[90000]" // unrelated Unix/UDP descriptor is outside TCP scope.
		for j, address := range service.TCPListeners {
			fd, inode := strconv.Itoa(j+3), uint64(1000+i*100+j)
			r.entries[base+"fd"] = append(r.entries[base+"fd"], fd)
			r.links[base+"fd/"+fd] = fmt.Sprintf("socket:[%d]", inode)
			if netip.MustParseAddrPort(address).Addr().Is4() {
				v4 += nativeTCPLine(address, inode, "0A")
			} else {
				v6 += nativeTCPLine(address, inode, "0A")
			}
		}
	}
	r.reads["self/net/tcp"], r.reads["self/net/tcp6"] = []byte(v4), []byte(v6)
	return r
}

func TestNativeScopeCapturesExactServiceInvocationsAndActivatedTCPHoldings(t *testing.T) {
	scope, err := freezeNativeScope(nativeTestScope("127.0.0.1:8080", "127.0.0.1:2019"))
	if err != nil {
		t.Fatal(err)
	}
	r := newNativeFixtureReader(t, scope)
	// The socket inode's uid is 0, while the public process runs as faas. One
	// activated socket can be held by PID 1 and have duplicate process FDs.
	r.entries["102/fd"] = append(r.entries["102/fd"], "7")
	r.links["102/fd/7"] = r.links["102/fd/3"]
	r.reads["self/net/tcp"] = append(r.reads["self/net/tcp"], []byte(nativeTCPLine("192.0.2.10:32000", 0, "06"))...)
	got, err := r.Capture(t.Context(), scope)
	if err != nil || got.CheckedAt.IsZero() || len(got.Services) != 2 || len(got.Services[0].Listeners) != 3 || len(got.Services[1].Listeners) != 2 {
		t.Fatal(got, err)
	}
	if !slices.Equal(got.Services[1].Listeners[0].FDs, []int{3, 7}) || got.Services[1].Listeners[0].Inode != 1100 {
		t.Fatal(got.Services[1].Listeners)
	}
	if r.calls["unit:caddy.service"] != 2 || r.calls["addresses"] != 2 || r.calls["read:sys/kernel/random/boot_id"] != 2 {
		t.Fatal(r.calls)
	}
	again, err := r.Capture(t.Context(), scope)
	if err != nil || !sameNativeScope(got, again) {
		t.Fatal(again, err)
	}
	encoded := string(jsonConfig(t, got))
	for _, forbidden := range []string{"secret-arbitrary-native-error", "command_line", "environment", "valid_until", "fenced"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatal(encoded)
		}
	}
}

func TestNativeScopeRejectsUnreviewedChangedAndUnreadableFacts(t *testing.T) {
	cases := map[string]func(*nativeFixtureReader){
		"boot":              func(r *nativeFixtureReader) { r.reads["sys/kernel/random/boot_id"] = []byte("other\n") },
		"machine":           func(r *nativeFixtureReader) { r.reads["machine-id"] = []byte(strings.Repeat("2", 32) + "\n") },
		"host-netns":        func(r *nativeFixtureReader) { r.links["1/ns/net"] = "net:[99]" },
		"host-pidns":        func(r *nativeFixtureReader) { r.links["self/ns/pid"] = "pid:[99]" },
		"process-netns":     func(r *nativeFixtureReader) { r.links["101/ns/net"] = "net:[99]" },
		"process-pidns":     func(r *nativeFixtureReader) { r.links["102/ns/pid"] = "pid:[99]" },
		"unassigned-ip":     func(r *nativeFixtureReader) { r.addresses = r.addresses[1:] },
		"changed-interface": func(r *nativeFixtureReader) { r.addresses[0].Index++ },
		"duplicate-ip":      func(r *nativeFixtureReader) { r.addresses = append(r.addresses, r.addresses[0]) },
		"unit-invocation": func(r *nativeFixtureReader) {
			r.units["caddy.service"] = []byte(strings.ReplaceAll(string(r.units["caddy.service"]), strings.Repeat("2", 32), strings.Repeat("4", 32)))
		},
		"main-pid": func(r *nativeFixtureReader) {
			r.units["caddy.service"] = []byte(strings.ReplaceAll(string(r.units["caddy.service"]), "MainPID=101", "MainPID=103"))
		},
		"stopped": func(r *nativeFixtureReader) {
			r.units["caddy.service"] = []byte(strings.ReplaceAll(string(r.units["caddy.service"]), "ActiveState=active", "ActiveState=inactive"))
		},
		"reload-needed": func(r *nativeFixtureReader) {
			r.units["caddy.service"] = []byte(strings.ReplaceAll(string(r.units["caddy.service"]), "NeedDaemonReload=no", "NeedDaemonReload=yes"))
		},
		"start-ticks": func(r *nativeFixtureReader) {
			r.reads["101/stat"] = []byte(strings.ReplaceAll(string(r.reads["101/stat"]), "456", "457"))
		},
		"uid":    func(r *nativeFixtureReader) { r.reads["101/status"] = []byte("Uid:\t997\t0\t997\t997\n") },
		"cgroup": func(r *nativeFixtureReader) { r.reads["101/cgroup"] = []byte("0::/other.slice/caddy.service\n") },
		"hybrid-cgroup": func(r *nativeFixtureReader) {
			r.reads["101/cgroup"] = append(r.reads["101/cgroup"], []byte("1:cpu:/legacy\n")...)
		},
		"cgroup-inode": func(r *nativeFixtureReader) {
			r.cgroups["/system.slice/caddy.service"] = NativeFileIdentity{Device: 2, Inode: 999}
		},
		"executable": func(r *nativeFixtureReader) {
			r.executables["101/exe"] = NativeFileIdentity{Device: 1, Inode: 301, SHA256: strings.Repeat("f", 64)}
		},
		"fd-missing":    func(r *nativeFixtureReader) { delete(r.links, "101/fd/3") },
		"fd-malformed":  func(r *nativeFixtureReader) { r.links["101/fd/3"] = "socket:[001000]" },
		"fd-duplicate":  func(r *nativeFixtureReader) { r.entries["101/fd"] = append(r.entries["101/fd"], "3") },
		"fd-bound":      func(r *nativeFixtureReader) { r.entries["101/fd"] = make([]string, api.RuntimeUpgradeNativeFDLimit+1) },
		"socket-inode":  func(r *nativeFixtureReader) { r.links["101/fd/3"] = "socket:[9999]" },
		"table-missing": func(r *nativeFixtureReader) { delete(r.reads, "self/net/tcp6") },
		"competing-inode": func(r *nativeFixtureReader) {
			r.reads["self/net/tcp"] = append(r.reads["self/net/tcp"], []byte(nativeTCPLine("0.0.0.0:443", 9999, "0A"))...)
		},
		"competing-specific": func(r *nativeFixtureReader) {
			r.reads["self/net/tcp"] = append(r.reads["self/net/tcp"], []byte(nativeTCPLine("192.0.2.10:443", 9999, "0A"))...)
		},
		"extra-held-listener": func(r *nativeFixtureReader) {
			r.links["101/fd/1"] = "socket:[9999]"
			r.reads["self/net/tcp"] = append(r.reads["self/net/tcp"], []byte(nativeTCPLine("0.0.0.0:444", 9999, "0A"))...)
		},
		"exit":              func(r *nativeFixtureReader) { r.fail = "alive" },
		"secret-unit-error": func(r *nativeFixtureReader) { r.fail = "unit:caddy.service" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			scope, err := freezeNativeScope(nativeTestScope("127.0.0.1:8080", "127.0.0.1:2019"))
			if err != nil {
				t.Fatal(err)
			}
			r := newNativeFixtureReader(t, scope)
			mutate(r)
			got, err := r.Capture(t.Context(), scope)
			if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeScopeObservation{}) || strings.Contains(err.Error(), "secret-arbitrary-native-error") {
				t.Fatal(got, err)
			}
		})
	}
}

func TestNativeScopeRejectsLateReplacementAndCancellation(t *testing.T) {
	for _, kind := range []string{"unit", "executable-inode", "cgroup-device", "host-ip", "pidfd-exit", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			scope, err := freezeNativeScope(nativeTestScope("127.0.0.1:8080", "127.0.0.1:2019"))
			if err != nil {
				t.Fatal(err)
			}
			r := newNativeFixtureReader(t, scope)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r.hook = func(name string, call int) {
				switch kind {
				case "unit":
					if name == "unit:caddy.service" && call == 2 {
						r.units["caddy.service"] = []byte(strings.ReplaceAll(string(r.units["caddy.service"]), "MainPID=101", "MainPID=103"))
					}
				case "executable-inode":
					if name == "exe:101/exe" && call == 2 {
						id := r.executables["101/exe"]
						id.Inode++
						r.executables["101/exe"] = id
					}
				case "cgroup-device":
					if name == "cgroup:/system.slice/caddy.service" && call == 2 {
						id := r.cgroups["/system.slice/caddy.service"]
						id.Device++
						r.cgroups["/system.slice/caddy.service"] = id
					}
				case "host-ip":
					if name == "addresses" && call == 2 {
						r.addresses = r.addresses[1:]
					}
				case "pidfd-exit":
					if name == "alive" && call == 2 {
						r.fail = "alive"
					}
				case "cancel":
					if name == "entries:101/fd" {
						cancel()
					}
				}
			}
			got, err := r.Capture(ctx, scope)
			if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeScopeObservation{}) {
				t.Fatal(got, err)
			}
		})
	}
}
