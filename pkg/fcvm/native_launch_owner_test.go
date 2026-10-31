//go:build linux || darwin

// adr: 2026101710 amendment 1 — an ordinary launch names its vmmd owner.
package fcvm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func nativeOwnerRecordFixture() nativeLaunchRecord {
	return nativeLaunchRecord{Version: 3, Generation: uuid.NewString(), KernelBootID: "d3124d9a-657b-4f37-a9df-f5dfb2e50764",
		Lease: leaseForSlot("owner-shape", 2), Owner: nativeProducerIdentity{Protocol: nativeLaunchOwnerProtocol, PID: 4242, UID: 0, StartTime: 99}}
}

func TestNativeLaunchOwnerRecordShape(t *testing.T) {
	record := nativeOwnerRecordFixture()
	b, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var copy nativeLaunchRecord
	if err := json.Unmarshal(b, &copy); err != nil || copy != record {
		t.Fatal("owner record round trip changed value identity", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["producer"]; ok {
		t.Fatal("an ordinary owner record claims a repair producer")
	}
	for name, mutate := range map[string]func(map[string]json.RawMessage){
		"missing owner":      func(m map[string]json.RawMessage) { delete(m, "owner") },
		"legacy with owner":  func(m map[string]json.RawMessage) { m["version"] = json.RawMessage(`1`) },
		"repair with owner":  func(m map[string]json.RawMessage) { m["version"] = json.RawMessage(`2`) },
		"owner and producer": func(m map[string]json.RawMessage) { m["producer"] = m["owner"] },
		"repair protocol": func(m map[string]json.RawMessage) {
			m["owner"] = json.RawMessage(`{"protocol":"data-repair-coldboot-v1","pid":4242,"uid":0,"start_time":99}`)
		},
		"unknown protocol": func(m map[string]json.RawMessage) {
			m["owner"] = json.RawMessage(`{"protocol":"unknown","pid":4242,"uid":0,"start_time":99}`)
		},
		"no incarnation": func(m map[string]json.RawMessage) {
			m["owner"] = json.RawMessage(`{"protocol":"vmmd-launch-owner-v1","pid":4242,"uid":0,"start_time":0}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := make(map[string]json.RawMessage, len(fields)+1)
			for k, v := range fields {
				bad[k] = v
			}
			mutate(bad)
			blob, _ := json.Marshal(bad)
			var decoded nativeLaunchRecord
			if err := json.Unmarshal(blob, &decoded); err == nil {
				t.Fatal("damaged owner record accepted")
			}
		})
	}
	// A repair producer cannot carry the owner protocol either.
	repair := record
	repair.Version, repair.Owner = 2, nativeProducerIdentity{}
	repair.Producer = record.Owner
	repair.Lease.Networkless = true
	if repair.validateProducer() == nil {
		t.Fatal("an owner identity enrolled as a repair producer")
	}
	// Legacy frames keep their exact shape.
	legacy := record
	legacy.Version, legacy.Owner = 1, nativeProducerIdentity{}
	blob, _ := json.Marshal(legacy)
	if strings.Contains(string(blob), `"owner"`) || json.Unmarshal(blob, &copy) != nil {
		t.Fatal("legacy frame shape changed")
	}
}
