package api

import "testing"

func TestSidecarsValidatePortConflicts(t *testing.T) {
	tests := []struct {
		name     string
		mainPort int
		sidecars Sidecars
		wantErr  bool
	}{
		{
			name:     "distinct ports",
			mainPort: 8080,
			sidecars: Sidecars{{Name: "metrics", Port: 9090}},
		},
		{
			name:     "worker without port",
			mainPort: 8080,
			sidecars: Sidecars{{Name: "worker", Type: SidecarTypeSidecar}},
		},
		{
			name:     "main default collision",
			sidecars: Sidecars{{Name: "metrics", Port: 8080}},
			wantErr:  true,
		},
		{
			name:     "sidecar collision",
			mainPort: 9000,
			sidecars: Sidecars{{Name: "a", Port: 9100}, {Name: "b", Port: 9100}},
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.sidecars.ValidatePortConflicts(tt.mainPort)
			if (got != nil) != tt.wantErr {
				t.Fatalf("ValidatePortConflicts() = %v, wantErr=%v", got, tt.wantErr)
			}
		})
	}
}

func TestValidateWorkloadPorts(t *testing.T) {
	tests := []struct {
		name    string
		ports   []WorkloadPort
		wantErr bool
	}{
		{
			name: "tcp and udp may share a number",
			ports: []WorkloadPort{
				{Name: "http", Port: 8080, Protocol: WorkloadPortTCP},
				{Name: "dns", Port: 8080, Protocol: WorkloadPortUDP},
			},
		},
		{
			name: "duplicate tuple",
			ports: []WorkloadPort{
				{Name: "a", Port: 8080, Protocol: WorkloadPortTCP},
				{Name: "b", Port: 8080, Protocol: WorkloadPortTCP},
			},
			wantErr: true,
		},
		{
			name: "duplicate name",
			ports: []WorkloadPort{
				{Name: "http", Port: 8080, Protocol: WorkloadPortTCP},
				{Name: "http", Port: 9090, Protocol: WorkloadPortTCP},
			},
			wantErr: true,
		},
		{
			name:  "omitted protocol defaults to tcp",
			ports: []WorkloadPort{{Port: 8080}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateWorkloadPorts(tt.ports); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateWorkloadPorts() = %v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
