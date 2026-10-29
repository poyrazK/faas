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
			name:     "unknown main port compares companions only",
			mainPort: -1,
			sidecars: Sidecars{{Name: "web", Port: 8080}},
		},
		{
			name:     "unknown main port still catches companion collisions",
			mainPort: -1,
			sidecars: Sidecars{{Name: "a", Port: 9100}, {Name: "b", Port: 9100}},
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

func TestSidecarSummariesCarryNoEnvironment(t *testing.T) {
	got := Sidecars{{
		Name: "proxy", Image: "registry.example/proxy@sha256:bb", Type: SidecarTypeSidecar, Port: 8081, RamMB: 128,
		Env: map[string]string{"TOKEN": "s3cret"}, EnvSecrets: map[string]string{"DB": "secret:DB"},
	}}.Summaries()
	want := DeploymentCompanion{Name: "proxy", Image: "registry.example/proxy@sha256:bb", Type: string(SidecarTypeSidecar), Port: 8081, RAMMB: 128}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Summaries() = %+v, want [%+v]", got, want)
	}
	if Sidecars(nil).Summaries() != nil {
		t.Fatal("no companions should summarize to nil so the response omits the field")
	}
}
