// adr: 568 — device handoff receipts preserve exact original descriptor authority.
package jailsetup

import (
	"encoding/json"
	"strings"
	"testing"
)

func deviceScopeFixture() DeviceSetupScope {
	return DeviceSetupScope{Generation: "372bbbc0-9e2e-407e-b8f4-0900f1ce1669", BootID: "d3124d9a-657b-4f37-a9df-f5dfb2e50764", PID: 222, StartTime: 234, UID: 20003, GID: 20003, ParentPID: 333, ParentStartTime: 888, ParentFDs: [4]int{8, 9, 10, 11}, Root: DeviceFDIdentity{Device: 1, Inode: 2}, Namespace: DeviceFDIdentity{Device: 3, Inode: 4}, PIDHandle: DeviceFDIdentity{Device: 5, Inode: 6}, Tun: DeviceFDIdentity{Device: 7, Inode: 8}, RootMountID: 23, TunMountID: 24, TunMode: 0o666}
}
func TestDeviceScopeStrictDecodingAndReceiptIdentity(t *testing.T) {
	s := deviceScopeFixture()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, damage := range []string{
		strings.Replace(string(data), `"start_time":234,`, "", 1),
		strings.Replace(string(data), `"start_time":234`, `"start_time":234,"start_time":235`, 1),
		strings.Replace(string(data), `"parent_fds":[8,9,10,11]`, `"parent_fds":[8,9,10]`, 1),
		strings.Replace(string(data), `"parent_fds":[8,9,10,11]`, `"parent_fds":[8,9,10,11,12]`, 1),
		strings.Replace(string(data), `"inode":4`, `"inode":null`, 1),
		strings.Replace(string(data), `"inode":4`, `"unknown":4`, 1),
		string(data) + "{}",
	} {
		var decoded DeviceSetupScope
		if err := json.Unmarshal([]byte(damage), &decoded); err == nil {
			t.Fatalf("damaged original scope accepted: %s", damage)
		}
	}
	r := DeviceSetupReceipt{Scope: s, DevMountID: 25, TunMountID: 26, KVM: DeviceFDIdentity{Device: 27, Inode: 28}, KVMAPI: 12, TunAccessible: true}
	if err := r.Validate(s); err != nil {
		t.Fatal(err)
	}
	r.Scope.Namespace.Inode++
	if err := r.Validate(s); err == nil {
		t.Fatal("another namespace's receipt accepted")
	}
	r.Scope = s
	r.TunAccessible = false
	if err := r.Validate(s); err == nil {
		t.Fatal("device presence substituted for jail UID access")
	}
}

// adr: 20261114010000 — version-2 KVM-only scope cannot inherit or publish a TUN capability.
func TestDeviceScopeKVMOnlyRequiresAbsentTUN(t *testing.T) {
	s := deviceScopeFixture()
	s.Version, s.Tun, s.TunMountID, s.TunMode = 2, s.Root, s.RootMountID, 0
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got DeviceSetupScope
	if err := json.Unmarshal(data, &got); err != nil || got != s {
		t.Fatalf("KVM-only scope roundtrip: %v", err)
	}
	r := DeviceSetupReceipt{Scope: s, DevMountID: 25, KVM: DeviceFDIdentity{Device: 27, Inode: 28}, KVMAPI: 12}
	if err := r.Validate(s); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*DeviceSetupScope){
		"version":   func(x *DeviceSetupScope) { x.Version = 3 },
		"legacy":    func(x *DeviceSetupScope) { x.Version = 0 },
		"TUN mode":  func(x *DeviceSetupScope) { x.TunMode = 0o666 },
		"TUN input": func(x *DeviceSetupScope) { x.Tun.Inode++ },
		"TUN mount": func(x *DeviceSetupScope) { x.TunMountID++ },
	} {
		t.Run(name, func(t *testing.T) {
			bad := s
			change(&bad)
			if bad.Validate() == nil {
				t.Fatal("invalid KVM-only scope accepted")
			}
		})
	}
	r.TunAccessible = true
	if r.Validate(s) == nil {
		t.Fatal("KVM-only receipt accepted TUN access")
	}
	r.TunAccessible = false
	r.TunMountID = 26
	if r.Validate(s) == nil {
		t.Fatal("KVM-only receipt accepted TUN mount")
	}
}
