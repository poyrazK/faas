// adr: 1344 — warm-standby replica manifest contract (§8).
package manifest

import (
	"strings"
	"testing"
)

func TestValidatePostgresStandby(t *testing.T) {
	split := []Host{{Name: "cp-1", Role: "control-plane"}, {Name: "cn-1", Role: "compute-only"}}
	valid := PostgresStandby{Name: "standby-1", Address: "10.10.0.20"}
	for _, tc := range []struct {
		name    string
		standby *PostgresStandby
		hosts   []Host
		overlay string
		wantErr string
	}{
		{name: "absent", hosts: split, overlay: "10.10.0.0/20"},
		{name: "valid", standby: &valid, hosts: split, overlay: "10.10.0.0/20"},
		{name: "tailscale address without overlay cidr", standby: &PostgresStandby{Name: "standby-1", Address: "100.101.102.103"}, hosts: split},
		{name: "rfc1918 without overlay cidr", standby: &valid, hosts: split},
		{name: "missing name", standby: &PostgresStandby{Address: "10.10.0.20"}, hosts: split, overlay: "10.10.0.0/20", wantErr: "postgresql.standby.name"},
		{name: "fleet host name", standby: &PostgresStandby{Name: "cn-1", Address: "10.10.0.20"}, hosts: split, overlay: "10.10.0.0/20", wantErr: "already a fleet host"},
		{name: "hostname address", standby: &PostgresStandby{Name: "standby-1", Address: "standby.gregale.dev"}, hosts: split, overlay: "10.10.0.0/20", wantErr: "must be an IP address"},
		{name: "outside overlay", standby: &PostgresStandby{Name: "standby-1", Address: "10.20.0.5"}, hosts: split, overlay: "10.10.0.0/20", wantErr: "outside overlay.cidr"},
		{name: "public address", standby: &PostgresStandby{Name: "standby-1", Address: "203.0.113.7"}, hosts: split, wantErr: "must be a private address"},
		{name: "bad ssh host", standby: &PostgresStandby{Name: "standby-1", Address: "10.10.0.20", SSHHost: "a b"}, hosts: split, overlay: "10.10.0.0/20", wantErr: "postgresql.standby.ssh_host"},
		{name: "single box", standby: &valid, hosts: []Host{{Name: "box", Role: "single-box"}}, overlay: "10.10.0.0/20", wantErr: "exactly one control-plane host"},
		{name: "two control planes", standby: &valid, hosts: append([]Host{{Name: "cp-2", Role: "control-plane"}}, split...), overlay: "10.10.0.0/20", wantErr: "exactly one control-plane host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Fleet: Fleet{Hosts: tc.hosts}, Overlay: Overlay{CIDR: tc.overlay}, PostgreSQL: PostgreSQL{Standby: tc.standby}}
			errs := m.validatePostgresStandby()
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validatePostgresStandby = %v, want no errors", errs)
				}
				return
			}
			if !strings.Contains(errs.Error(), tc.wantErr) {
				t.Fatalf("validatePostgresStandby = %v, want an error mentioning %q", errs, tc.wantErr)
			}
		})
	}
}

func TestPostgresStandbyAnsibleHost(t *testing.T) {
	if got := (&PostgresStandby{Address: "10.10.0.20"}).AnsibleHost(); got != "10.10.0.20" {
		t.Errorf("AnsibleHost without ssh_host = %q, want the address", got)
	}
	if got := (&PostgresStandby{Address: "10.10.0.20", SSHHost: "34.1.2.3"}).AnsibleHost(); got != "34.1.2.3" {
		t.Errorf("AnsibleHost with ssh_host = %q, want the ssh_host", got)
	}
}
