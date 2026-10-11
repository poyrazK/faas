package edgetopology

// adr: 707

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type activationFixture struct {
	*nativeFixtureReader
	review NativeActivationReview
	masks  map[string]NativeFileIdentity
	group  NativeEmptyCgroup
	events []byte
	opens  int
	probe  *NativeActivationProbe
}

func newActivationFixture(t *testing.T) *activationFixture {
	t.Helper()
	scope := nativeTestScope("127.0.0.1:8080", "127.0.0.1:2019")
	f := &activationFixture{nativeFixtureReader: newNativeFixtureReader(t, scope), review: NativeActivationReview{Host: scope.Host, Service: scope.Services[1], Sockets: []string{"faas-gatewayd-public.socket"}}, masks: make(map[string]NativeFileIdentity), events: []byte("populated 0\nfrozen 0\n")}
	f.group = NativeEmptyCgroup{Present: true, Identity: NativeFileIdentity{Device: 42, Inode: scope.Services[1].CgroupInode}}
	for i, unit := range []string{f.review.Service.Unit, f.review.Sockets[0]} {
		f.units[unit] = activationUnitBody(unit)
		f.masks[unit] = NativeFileIdentity{Device: 43, Inode: uint64(100 + i)}
	}
	f.reads["self/net/tcp"], f.reads["self/net/tcp6"] = []byte(nativeTCPHeader), []byte(nativeTCPHeader)
	f.probe = &NativeActivationProbe{open: func(ctx context.Context, review NativeActivationReview) (nativeActivationSession, error) {
		f.opens++
		return f, nil
	}}
	return f
}

func activationUnitBody(unit string) []byte {
	raw := fmt.Sprintf("Id=%s\nLoadState=masked\nActiveState=inactive\nSubState=dead\nUnitFileState=masked\nNeedDaemonReload=no\nJob=\nControlPID=0\nControlGroup=\n", unit)
	if nativeUnit(unit) {
		raw += "MainPID=0\nNFileDescriptorStore=0\n"
	}
	return []byte(raw)
}

func (f *activationFixture) MaskedUnit(ctx context.Context, unit string) (NativeMaskedUnit, error) {
	if err := f.call("masked:" + unit); err != nil || ctx.Err() != nil || matchNativeMaskedUnit(f.units[unit], unit) != nil {
		return NativeMaskedUnit{}, ErrNativeUnverified
	}
	return NativeMaskedUnit{Unit: unit, Mask: f.masks[unit]}, nil
}

func (f *activationFixture) EmptyCgroup(ctx context.Context, service NativeServiceReview) (NativeEmptyCgroup, error) {
	if err := f.call("empty:" + service.Cgroup); err != nil || ctx.Err() != nil || (f.group.Present && matchNativeEmptyEvents(f.events) != nil) {
		return NativeEmptyCgroup{}, ErrNativeUnverified
	}
	return f.group, nil
}

func (f *activationFixture) Capture(ctx context.Context, review NativeActivationReview) (NativeActivationSnapshot, error) {
	f.captures++
	if f.captureHook != nil {
		f.captureHook(f.captures)
	}
	return captureNativeActivation(ctx, f, review)
}

func TestNativeActivationRequiresBothPersistentMasksAndEmptyTCPBookends(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			f := newActivationFixture(t)
			if !present {
				f.group = NativeEmptyCgroup{}
			}
			// Other endpoints remain explicitly outside this selected scope.
			f.reads["self/net/tcp"] = []byte(nativeTCPHeader + nativeTCPLine("127.0.0.1:8084", 500, "0A"))
			got, err := f.probe.Observe(t.Context(), f.review)
			if err != nil || len(got.Snapshots) != 2 || !sameNativeActivation(got.Snapshots[0], got.Snapshots[1]) || got.Snapshots[0].Cgroup.Present != present || len(got.Snapshots[0].Units) != 2 || got.CheckedAt.IsZero() || f.captures != 2 || f.closes != 1 {
				t.Fatal(got, err, f.captures, f.closes)
			}
			if f.calls["alive"] != 0 || f.calls["read:102/stat"] != 0 || f.calls["entries:102/fd"] != 0 {
				t.Fatal("dead main PID substituted for socket activation evidence")
			}
		})
	}
}

func TestNativeActivationRejectsRetainedSocketsAndEveryTCPState(t *testing.T) {
	for _, state := range []string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "0A", "0B", "0C"} {
		for _, address := range []string{"127.0.0.1:8080", "0.0.0.0:8080", "[::]:8080", "127.0.0.1:9092"} {
			t.Run(state+address, func(t *testing.T) {
				f := newActivationFixture(t)
				family := "tcp"
				if strings.HasPrefix(address, "[") {
					family = "tcp6"
				}
				// No holder lookup: a PID 1 or unrelated-process listener must
				// fail even when the previous service's descriptor set is gone.
				f.reads["self/net/"+family] = []byte(nativeTCPHeader + nativeTCPLine(address, 700, state))
				got, err := f.probe.Observe(t.Context(), f.review)
				if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) || f.closes != 1 {
					t.Fatal(got, err, f.closes)
				}
			})
		}
	}
}

func TestNativeActivationRejectsMappedEstablishedConnection(t *testing.T) {
	f := newActivationFixture(t)
	f.reads["self/net/tcp6"] = []byte(nativeTCPHeader + nativeTCPLine("[::ffff:127.0.0.1]:8080", 800, "01"))
	if got, err := f.probe.Observe(t.Context(), f.review); !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) {
		t.Fatal(got, err)
	}
}

func TestNativeActivationRejectsMaskStateAndPropertyAmbiguity(t *testing.T) {
	for _, unit := range []string{"faas-gatewayd-public.service", "faas-gatewayd-public.socket"} {
		for _, mutation := range []string{"runtime", "active", "job", "control-pid", "cgroup", "reload", "disabled", "alias", "missing", "duplicate", "extra", "truncated", "overflow", "main-pid", "fdstore"} {
			t.Run(unit+mutation, func(t *testing.T) {
				f := newActivationFixture(t)
				raw := string(f.units[unit])
				switch mutation {
				case "runtime":
					raw = strings.Replace(raw, "UnitFileState=masked", "UnitFileState=masked-runtime", 1)
				case "active":
					raw = strings.Replace(raw, "ActiveState=inactive", "ActiveState=active", 1)
				case "job":
					raw = strings.Replace(raw, "Job=\n", "Job=1\n", 1)
				case "control-pid":
					raw = strings.Replace(raw, "ControlPID=0", "ControlPID=123", 1)
				case "cgroup":
					raw = strings.Replace(raw, "ControlGroup=\n", "ControlGroup=/system.slice/other.service\n", 1)
				case "reload":
					raw = strings.Replace(raw, "NeedDaemonReload=no", "NeedDaemonReload=yes", 1)
				case "disabled":
					raw = strings.Replace(raw, "LoadState=masked", "LoadState=loaded", 1)
				case "alias":
					raw = strings.Replace(raw, "Id="+unit, "Id=other.service", 1)
				case "missing":
					raw = strings.Replace(raw, "Job=\n", "", 1)
				case "duplicate":
					raw += "Job=\n"
				case "extra":
					raw += "Unknown=1\n"
				case "truncated":
					raw = strings.TrimSuffix(raw, "\n")
				case "overflow":
					raw = strings.Repeat("a", api.RuntimeUpgradeNativeUnitMaxBytes+1)
				case "main-pid":
					if nativeUnit(unit) {
						raw = strings.Replace(raw, "MainPID=0", "MainPID=102", 1)
					} else {
						raw += "MainPID=102\n"
					}
				case "fdstore":
					if nativeUnit(unit) {
						raw = strings.Replace(raw, "NFileDescriptorStore=0", "NFileDescriptorStore=1", 1)
					} else {
						raw += "NFileDescriptorStore=1\n"
					}
				}
				f.units[unit] = []byte(raw)
				got, err := f.probe.Observe(t.Context(), f.review)
				if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) || f.closes != 1 {
					t.Fatal(got, err, f.closes)
				}
			})
		}
	}
}

func TestNativeActivationRejectsPopulationFrozenReplacementAndMalformedEvents(t *testing.T) {
	for _, events := range []string{"populated 1\nfrozen 0\n", "populated 0\nfrozen 1\n", "populated 0\n", "frozen 0\n", "populated 0\npopulated 0\nfrozen 0\n", "populated 0\nfrozen 0", "populated 0\nfrozen 0\nunknown 0\n", "populated 00\nfrozen 0\n"} {
		t.Run(events, func(t *testing.T) {
			f := newActivationFixture(t)
			f.events = []byte(events)
			if got, err := f.probe.Observe(t.Context(), f.review); !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) {
				t.Fatal(got, err)
			}
		})
	}
	for _, mutate := range []func(*activationFixture){
		func(f *activationFixture) { f.group.Identity.Inode++ },
		func(f *activationFixture) { f.group.Identity.SHA256 = strings.Repeat("a", 64) },
		func(f *activationFixture) { f.group.Present = false },
		func(f *activationFixture) { f.masks[f.review.Service.Unit] = NativeFileIdentity{} },
	} {
		f := newActivationFixture(t)
		mutate(f)
		if got, err := f.probe.Observe(t.Context(), f.review); !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) {
			t.Fatal(got, err)
		}
	}
}

func TestNativeActivationRejectsMissingNativeReadsAndCancellation(t *testing.T) {
	for _, fail := range []string{"read:machine-id", "read:sys/kernel/random/boot_id", "link:1/ns/net", "link:self/ns/pid", "addresses", "masked:faas-gatewayd-public.service", "masked:faas-gatewayd-public.socket", "empty:/faas-cp.slice/faas-gatewayd-public.service", "read:self/net/tcp", "read:self/net/tcp6"} {
		t.Run(fail, func(t *testing.T) {
			f := newActivationFixture(t)
			f.fail = fail
			got, err := f.probe.Observe(t.Context(), f.review)
			if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) || f.closes != 1 || strings.Contains(err.Error(), "secret") {
				t.Fatal(got, err, f.closes)
			}
		})
	}
	for _, capture := range []int{1, 2} {
		f := newActivationFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		f.captureHook = func(call int) {
			if call == capture {
				cancel()
			}
		}
		got, err := f.probe.Observe(ctx, f.review)
		cancel()
		if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) || f.closes != 1 {
			t.Fatal(got, err, f.closes)
		}
	}
}

func TestNativeActivationRejectsDriftWithinAndBetweenCaptures(t *testing.T) {
	for _, between := range []bool{false, true} {
		for _, kind := range []string{"mask", "cgroup", "absent", "host", "boot", "namespace", "tcp"} {
			t.Run(fmt.Sprint(between)+kind, func(t *testing.T) {
				f := newActivationFixture(t)
				mutate := func() {
					switch kind {
					case "mask":
						id := f.masks[f.review.Sockets[0]]
						id.Inode++
						f.masks[f.review.Sockets[0]] = id
					case "cgroup":
						f.group.Identity.Device++
					case "absent":
						f.group = NativeEmptyCgroup{}
					case "host":
						f.reads["machine-id"] = []byte(strings.Repeat("a", 32) + "\n")
					case "boot":
						f.reads["sys/kernel/random/boot_id"] = []byte("671f3d34-377e-4c3f-b9a1-06c9a6d606f5\n")
					case "namespace":
						f.links["1/ns/net"] = "net:[12]"
					case "tcp":
						f.reads["self/net/tcp"] = []byte(nativeTCPHeader + nativeTCPLine("127.0.0.1:8080", 900, "0A"))
					}
				}
				if between {
					f.captureHook = func(call int) {
						if call == 2 {
							mutate()
						}
					}
				} else {
					f.hook = func(name string, call int) {
						if name == "read:self/net/tcp" && call == 1 {
							mutate()
						}
					}
				}
				got, err := f.probe.Observe(t.Context(), f.review)
				if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) || f.closes != 1 {
					t.Fatal(got, err, f.closes)
				}
			})
		}
	}
}

func TestNativeActivationRejectsMalformedOrTruncatedTCPInventory(t *testing.T) {
	for _, raw := range []string{"", "wrong header\n", nativeTCPHeader + "truncated\n", strings.TrimSuffix(nativeTCPHeader, "\n"), strings.Repeat("a", api.RuntimeUpgradeNativeProcMaxBytes+1)} {
		f := newActivationFixture(t)
		f.reads["self/net/tcp6"] = []byte(raw)
		if got, err := f.probe.Observe(t.Context(), f.review); !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) {
			t.Fatal(got, err)
		}
	}
}

func TestNativeActivationFreezesHistoricalReviewBeforeOpening(t *testing.T) {
	f := newActivationFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	f.captureHook = func(call int) {
		if call == 1 {
			close(started)
			<-release
		}
	}
	type result struct {
		got NativeActivationObservation
		err error
	}
	done := make(chan result, 1)
	go func() { got, err := f.probe.Observe(t.Context(), f.review); done <- result{got, err} }()
	<-started
	f.review.Sockets[0] = "changed.socket"
	f.review.Service.TCPListeners[0] = "127.0.0.1:9999"
	f.review.Host.Addresses[0].IP = "192.0.2.99"
	close(release)
	r := <-done
	if r.err != nil || slices.Contains(r.got.Review.Sockets, "changed.socket") || slices.Contains(r.got.Review.Service.TCPListeners, "127.0.0.1:9999") || r.got.Review.Host.Addresses[0].IP == "192.0.2.99" {
		t.Fatal(r.got, r.err)
	}
}

func TestNativeActivationRejectsInvalidReviewsBeforeOpening(t *testing.T) {
	for name, mutate := range map[string]func(*NativeActivationReview){
		"no-sockets":        func(r *NativeActivationReview) { r.Sockets = nil },
		"duplicate":         func(r *NativeActivationReview) { r.Sockets = append(r.Sockets, r.Sockets[0]) },
		"service-as-socket": func(r *NativeActivationReview) { r.Sockets[0] = r.Service.Unit },
		"socket-option":     func(r *NativeActivationReview) { r.Sockets[0] = "--remote.socket" },
		"path":              func(r *NativeActivationReview) { r.Sockets[0] = "../bad.socket" },
		"socket-limit": func(r *NativeActivationReview) {
			r.Sockets = make([]string, api.RuntimeUpgradeNativeActivationSocketLimit+1)
		},
		"boot":           func(r *NativeActivationReview) { r.Host.BootID = "bad" },
		"old-pid":        func(r *NativeActivationReview) { r.Service.PID = 1 },
		"old-invocation": func(r *NativeActivationReview) { r.Service.InvocationID = "" },
		"old-cgroup":     func(r *NativeActivationReview) { r.Service.Cgroup = "/../bad" },
		"no-endpoints":   func(r *NativeActivationReview) { r.Service.TCPListeners = nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := newActivationFixture(t)
			mutate(&f.review)
			got, err := f.probe.Observe(t.Context(), f.review)
			if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) || f.opens != 0 {
				t.Fatal(got, err, f.opens)
			}
		})
	}
	var zero NativeActivationProbe
	var nilProbe *NativeActivationProbe
	for _, probe := range []*NativeActivationProbe{&zero, nilProbe} {
		f := newActivationFixture(t)
		if got, err := probe.Observe(t.Context(), f.review); !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeActivationObservation{}) {
			t.Fatal(got, err)
		}
	}
}
