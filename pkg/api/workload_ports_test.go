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
