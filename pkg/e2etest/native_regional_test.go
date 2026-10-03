// adr: 427
package e2etest

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type nativeProbeExit int

func (e nativeProbeExit) Error() string { return "probe exit" }
func (e nativeProbeExit) ExitCode() int { return int(e) }

func TestProbeNativeAcceptanceHost(t *testing.T) {
	for _, scenario := range []string{"native", "arm", "nested KVM", "google", "container", "missing probe", "probe transport failure", "ambiguous absence", "missing KVM", "missing marker", "missing identity", "invalid identity"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			run := func(_ context.Context, args ...string) ([]byte, error) {
				calls++
				key := strings.Join(args, " ")
				switch key {
				case "uname -sm":
					if scenario == "arm" {
						return []byte("Linux aarch64\n"), nil
					}
					return []byte("Linux x86_64\n"), nil
				case "systemd-detect-virt":
					switch scenario {
					case "nested KVM":
						return []byte("kvm\n"), nil
					case "google":
						return []byte("google\n"), nil
					case "container":
						return []byte("docker\n"), nil
					case "missing probe":
						return nil, nativeProbeExit(127)
					case "probe transport failure":
						return []byte("none\n"), errors.New("transport unavailable")
					case "ambiguous absence":
						return []byte("none\n"), nil
					}
					return []byte("none\n"), nativeProbeExit(1)
				case "sudo -n test -c /dev/kvm":
					if scenario == "missing KVM" {
						return nil, nativeProbeExit(1)
					}
				case "sudo -n test -f " + NativeRegionalAcceptanceMarker:
					if scenario == "missing marker" {
						return nil, nativeProbeExit(1)
					}
				case "cat /etc/machine-id":
					if scenario == "missing identity" {
						return nil, nativeProbeExit(1)
					}
					if scenario == "invalid identity" {
						return []byte("alias\n"), nil
					}
					return []byte("00112233445566778899aabbccddeeff\n"), nil
				default:
					t.Fatalf("unexpected probe %q", key)
				}
				return nil, nil
			}
			evidence, err := ProbeNativeAcceptanceHost(t.Context(), run)
			if scenario == "native" {
				if err != nil || evidence.MachineID == "" || calls != 5 {
					t.Fatalf("evidence=%+v calls=%d err=%v", evidence, calls, err)
				}
			} else if err == nil {
				t.Fatalf("accepted unqualified host: %+v", evidence)
			}
			if scenario == "google" && calls != 2 {
				t.Fatal("continued probing a nested host")
			}
		})
	}
}

func TestNativeAcceptanceSSHRejectsUnsafeTargets(t *testing.T) {
	for _, target := range []string{"", "-oProxyCommand=id", "worker;id", "worker\nother", "worker$(id)", "worker`id`", "worker name", "worker/path"} {
		if _, err := NativeAcceptanceSSH(target); err == nil {
			t.Errorf("accepted target %q", target)
		}
	}
	for _, target := range []string{"worker-a", "root@worker-a.example", "10.42.0.1", "[2001:db8::1]"} {
		if _, err := NativeAcceptanceSSH(target); err != nil {
			t.Errorf("rejected target %q: %v", target, err)
		}
	}
}

func TestJobBootOperations(t *testing.T) {
	const release = "# TYPE vmmd_faas_deploy_version gauge\nvmmd_faas_deploy_version{version=\"test\"} 1\n"
	for _, tc := range []struct {
		name, text string
		want       float64
		valid      bool
	}{
		{"unused", release, 0, true},
		{"both outcomes", release + "# TYPE vmmd_ops_total counter\nvmmd_ops_total{code=\"ok\",op=\"JobColdBoot\"} 2\nvmmd_ops_total{op=\"JobColdBoot\",code=\"err\"} 3\nvmmd_ops_total{op=\"Stats\",code=\"ok\"} 99\n", 5, true},
		{"wrong endpoint", "# TYPE schedd_faas_deploy_version gauge\nschedd_faas_deploy_version 1\n", 0, false},
		{"malformed", release + "vmmd_ops_total{op=\"JobColdBoot\"} nope\n", 0, false},
		{"negative", release + "# TYPE vmmd_ops_total counter\nvmmd_ops_total{op=\"JobColdBoot\"} -1\n", 0, false},
		{"nonfinite", release + "# TYPE vmmd_ops_total counter\nvmmd_ops_total{op=\"JobColdBoot\"} NaN\n", 0, false},
		{"wrong type", release + "# TYPE vmmd_ops_total gauge\nvmmd_ops_total{op=\"JobColdBoot\"} 1\n", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := JobBootOperations(tc.text)
			if (err == nil) != tc.valid || got != tc.want {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
}
