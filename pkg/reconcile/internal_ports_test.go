// adr: 576
package reconcile

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func tcp(port int, internal bool) api.WorkloadPort {
	return api.WorkloadPort{Port: port, Protocol: api.WorkloadPortTCP, Internal: internal}
}

func TestPortsWithInternal(t *testing.T) {
	public := api.WorkloadPort{Name: "metrics", Port: 9100, Protocol: api.WorkloadPortTCP}
	udp := api.WorkloadPort{Port: 6379, Protocol: api.WorkloadPortUDP}
	tests := []struct {
		name     string
		existing []api.WorkloadPort
		internal []api.WorkloadPort
		want     []api.WorkloadPort
	}{
		{name: "source silent keeps everything", existing: []api.WorkloadPort{public, tcp(6379, true)}, internal: nil,
			want: []api.WorkloadPort{public, tcp(6379, true)}},
		{name: "replaces only the internal subset", existing: []api.WorkloadPort{public, tcp(5432, true)}, internal: []api.WorkloadPort{tcp(6379, true)},
			want: []api.WorkloadPort{public, tcp(6379, true)}},
		{name: "expose takes over a public TCP listener", existing: []api.WorkloadPort{tcp(6379, false), udp}, internal: []api.WorkloadPort{tcp(6379, true)},
			want: []api.WorkloadPort{udp, tcp(6379, true)}},
		{name: "empty expose clears internal listeners", existing: []api.WorkloadPort{public, tcp(6379, true)}, internal: []api.WorkloadPort{},
			want: []api.WorkloadPort{public}},
		{name: "new app", existing: nil, internal: []api.WorkloadPort{tcp(6379, true)}, want: []api.WorkloadPort{tcp(6379, true)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := portsWithInternal(tt.existing, tt.internal)
			if len(got) != len(tt.want) {
				t.Fatalf("ports = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ports = %+v, want %+v", got, tt.want)
				}
			}
		})
	}
	if got := portsWithInternal(nil, nil); got != nil {
		t.Fatalf("silent source on a new app = %+v, want nil (image listeners preserved)", got)
	}
}

func TestPortsWithInternalCapKeepsSourceListeners(t *testing.T) {
	var existing []api.WorkloadPort
	for port := 9000; port < 9000+api.WorkloadPortCapMax; port++ {
		existing = append(existing, tcp(port, false))
	}
	got := portsWithInternal(existing, []api.WorkloadPort{tcp(6379, true)})
	if len(got) != api.WorkloadPortCapMax || got[len(got)-1] != tcp(6379, true) {
		t.Fatalf("over the cap: %d ports, last %+v; want the internal listener kept", len(got), got[len(got)-1])
	}
	if err := api.ValidateWorkloadPorts(got); err != nil {
		t.Fatalf("result fails validation: %v", err)
	}
}

func TestInternalPortsChanged(t *testing.T) {
	existing := []api.WorkloadPort{tcp(9100, false), tcp(6379, true)}
	if internalPortsChanged(existing, nil) {
		t.Fatal("a silent source reported a change")
	}
	if internalPortsChanged(existing, []api.WorkloadPort{tcp(6379, true)}) {
		t.Fatal("an identical internal set reported a change")
	}
	if !internalPortsChanged(existing, []api.WorkloadPort{tcp(5432, true)}) {
		t.Fatal("a different internal set was not reported")
	}
	if !internalPortsChanged(existing, []api.WorkloadPort{}) {
		t.Fatal("clearing internal listeners was not reported")
	}
}
