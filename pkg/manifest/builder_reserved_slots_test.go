package manifest

import (
	"strings"
	"testing"
)

// builder_reserved_slots is 1–4 on a compute-only host and refused anywhere
// else (ADR-20261118154632 amendment 1); unset keeps today's one slot.
//
// adr: 20261118154632
func TestBuilderReservedSlotsValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hosts   []Host
		wantErr string
	}{
		{name: "unset", hosts: []Host{{Name: "cn-1", Role: "compute-only"}}},
		{name: "two on compute", hosts: []Host{{Name: "cn-1", Role: "compute-only", BuilderReservedSlots: 2}}},
		{name: "four on compute", hosts: []Host{{Name: "cn-1", Role: "compute-only", BuilderReservedSlots: 4}}},
		{name: "five on compute", hosts: []Host{{Name: "cn-1", Role: "compute-only", BuilderReservedSlots: 5}}, wantErr: "fleet.hosts[0].builder_reserved_slots"},
		{name: "negative", hosts: []Host{{Name: "cn-1", Role: "compute-only", BuilderReservedSlots: -1}}, wantErr: "must be 1–4"},
		{name: "control plane", hosts: []Host{{Name: "cp-1", Role: "control-plane"}, {Name: "cp-2", Role: "control-plane", BuilderReservedSlots: 2}}, wantErr: "fleet.hosts[1].builder_reserved_slots"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Fleet: Fleet{Hosts: tc.hosts}}
			errs := m.validateBuilderReservedSlots()
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("errors = %v, want none", errs)
				}
				return
			}
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.wantErr) {
				t.Fatalf("errors = %v, want one naming %q", errs, tc.wantErr)
			}
		})
	}
}
