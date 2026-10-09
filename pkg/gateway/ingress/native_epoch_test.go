package ingress

// adr: 710

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativeStartupFixture() NativePublicStartup {
	return NativePublicStartup{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64), Epoch: NativeProcessEpoch{MachineID: strings.Repeat("1", 32), BootID: uuid.NewString(), PID: 1234, StartTicks: 56789, PIDNamespace: "pid:[4026531836]", NetNamespace: "net:[4026531840]"}}
}

func nativeStatFixture(epoch NativeProcessEpoch, comm string) []byte {
	fields := []string{"R"}
	for range 18 {
		fields = append(fields, "0")
	}
	fields = append(fields, strconv.FormatUint(epoch.StartTicks, 10), "0", "0")
	return []byte(fmt.Sprintf("%d (%s) %s\n", epoch.PID, comm, strings.Join(fields, " ")))
}

type epochFixtureReader struct {
	epoch NativeProcessEpoch
	reads int
	links int
	read  func(*epochFixtureReader, string) ([]byte, error)
	link  func(*epochFixtureReader, string) (string, error)
}

func (r *epochFixtureReader) PID() int { return r.epoch.PID }
func (r *epochFixtureReader) Read(_ context.Context, name string) ([]byte, error) {
	r.reads++
	if r.read != nil {
		return r.read(r, name)
	}
	return r.metadata(name), nil
}
func (r *epochFixtureReader) metadata(name string) []byte {
	switch name {
	case "machine-id":
		return []byte(r.epoch.MachineID + "\n")
	case "sys/kernel/random/boot_id":
		return []byte(r.epoch.BootID + "\n")
	case "self/stat":
		return nativeStatFixture(r.epoch, "gateway ) with\n spaces")
	}
	return nil
}
func (r *epochFixtureReader) Link(name string) (string, error) {
	r.links++
	if name != "self/ns/pid" && name != "self/ns/net" {
		return "", errors.New("other process namespaces unavailable to gateway")
	}
	if r.link != nil {
		return r.link(r, name)
	}
	if strings.HasSuffix(name, "/pid") {
		return r.epoch.PIDNamespace, nil
	}
	return r.epoch.NetNamespace, nil
}

func TestNativeEpochCaptureBindsTwoEqualKernelSnapshots(t *testing.T) {
	want := nativeStartupFixture().Epoch
	r := &epochFixtureReader{epoch: want}
	got, err := stableNativeProcessEpoch(t.Context(), r)
	if err != nil || got != want || r.reads != 6 || r.links != 4 {
		t.Fatal(got, err, r.reads, r.links)
	}
}

func TestNativeEpochCaptureRejectsChangesMissingMetadataAndMalformedNamespaces(t *testing.T) {
	for _, kind := range []string{"machine-change", "boot-change", "pid-change", "start-change", "pidns-change", "netns-change", "read-failure", "link-failure", "invalid-pidns", "invalid-netns", "machine-newline", "boot-newline", "oversized-stat", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := &epochFixtureReader{epoch: nativeStartupFixture().Epoch}
			r.read = func(r *epochFixtureReader, name string) ([]byte, error) {
				if r.reads == 4 {
					switch kind {
					case "machine-change":
						r.epoch.MachineID = strings.Repeat("2", 32)
					case "boot-change":
						r.epoch.BootID = uuid.NewString()
					case "pid-change":
						r.epoch.PID++
					case "start-change":
						r.epoch.StartTicks++
					case "pidns-change":
						r.epoch.PIDNamespace = "pid:[9000]"
					case "netns-change":
						r.epoch.NetNamespace = "net:[9000]"
					case "cancel":
						cancel()
					}
				}
				if kind == "read-failure" && r.reads == 6 {
					return nil, errors.New("fixture unavailable")
				}
				raw := r.metadata(name)
				if (kind == "machine-newline" && name == "machine-id") || (kind == "boot-newline" && name == "sys/kernel/random/boot_id") {
					return raw[:len(raw)-1], nil
				}
				if kind == "oversized-stat" && name == "self/stat" {
					return []byte(strings.Repeat("a", api.RuntimeUpgradeNativeMetadataMaxBytes+1)), nil
				}
				return raw, nil
			}
			r.link = func(r *epochFixtureReader, name string) (string, error) {
				if kind == "link-failure" && r.links == 4 {
					return "", errors.New("fixture unavailable")
				}
				if (kind == "invalid-pidns" && name == "self/ns/pid") || (kind == "invalid-netns" && name == "self/ns/net") {
					return "other:[9000]", nil
				}
				if strings.HasSuffix(name, "/pid") {
					return r.epoch.PIDNamespace, nil
				}
				return r.epoch.NetNamespace, nil
			}
			got, err := stableNativeProcessEpoch(ctx, r)
			if !errors.Is(err, ErrNativeStartupUnverified) || got != (NativeProcessEpoch{}) {
				t.Fatal("changed/incomplete source emitted identity", got, err)
			}
		})
	}
	r := &epochFixtureReader{epoch: nativeStartupFixture().Epoch}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := stableNativeProcessEpoch(ctx, r); !errors.Is(err, ErrNativeStartupUnverified) || got != (NativeProcessEpoch{}) || r.reads != 0 {
		t.Fatal("cancelled capture read metadata", got, err, r.reads)
	}
}

func TestNativeEpochStatParsingHandlesCommAndRejectsPIDReuseOrAmbiguity(t *testing.T) {
	epoch := nativeStartupFixture().Epoch
	raw := nativeStatFixture(epoch, "command ) with\n ) odd spaces")
	if got, err := nativeProcessStart(raw, epoch.PID); err != nil || got != epoch.StartTicks {
		t.Fatal(got, err)
	}
	for _, bad := range [][]byte{nil, raw[:len(raw)-1], []byte(strings.Replace(string(raw), "1234 (", "1235 (", 1)), []byte(strings.Replace(string(raw), ") R ", ") Z ", 1)), []byte(strings.Replace(string(raw), "56789", "056789", 1)), []byte(strings.Replace(string(raw), "56789", "18446744073709551616", 1)), []byte(strings.Replace(string(raw), "56789", "0", 1)), []byte("1234 (comm) R 1 2\n")} {
		if got, err := nativeProcessStart(bad, epoch.PID); !errors.Is(err, ErrNativeStartupUnverified) || got != 0 {
			t.Fatal(got, err, string(bad))
		}
	}
}

func TestNativeEpochStartupRejectsNoncanonicalOrIncompleteIdentities(t *testing.T) {
	for name, mutate := range map[string]func(*NativePublicStartup){
		"slot": func(s *NativePublicStartup) { s.SlotID = "bad" }, "session": func(s *NativePublicStartup) { s.SessionID = uuid.Nil.String() }, "config": func(s *NativePublicStartup) { s.ConfigSHA256 = "bad" },
		"machine": func(s *NativePublicStartup) { s.Epoch.MachineID = strings.Repeat("A", 32) }, "zero-machine": func(s *NativePublicStartup) { s.Epoch.MachineID = strings.Repeat("0", 32) }, "boot": func(s *NativePublicStartup) { s.Epoch.BootID = uuid.Nil.String() }, "pid": func(s *NativePublicStartup) { s.Epoch.PID = 1 }, "start": func(s *NativePublicStartup) { s.Epoch.StartTicks = 0 },
		"pid-ns": func(s *NativePublicStartup) { s.Epoch.PIDNamespace = "pid:[0001]" }, "wrong-ns": func(s *NativePublicStartup) { s.Epoch.NetNamespace = "pid:[1]" }, "empty-ns": func(s *NativePublicStartup) { s.Epoch.NetNamespace = "net:[]" }, "huge-ns": func(s *NativePublicStartup) { s.Epoch.NetNamespace = "net:[18446744073709551616]" },
	} {
		t.Run(name, func(t *testing.T) {
			startup := nativeStartupFixture()
			mutate(&startup)
			if err := ValidateNativePublicStartup(startup); !errors.Is(err, ErrNativeStartupUnverified) {
				t.Fatal(startup, err)
			}
			if h, err := newNativePublicIdentityHandler(testToken, startup); !errors.Is(err, ErrNativeStartupUnverified) || h != nil {
				t.Fatal(h, err)
			}
		})
	}
	if h, err := newNativePublicIdentityHandler("bad", nativeStartupFixture()); !errors.Is(err, ErrNativeStartupUnverified) || h != nil {
		t.Fatal(h, err)
	}
}
