package imaged

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCheckCompanionPortsUsesTheBuiltMainPort is the imaged half of Hunt 14
// H14-53: companions are compared with the main port the build resolved.
func TestCheckCompanionPortsUsesTheBuiltMainPort(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mainPort   int // the final guest manifest's port; 0 is the 8080 default
		companions string
		wantErr    string
	}{
		{name: "no companions", mainPort: 8000},
		{name: "null companions", mainPort: 8000, companions: "null"},
		{name: "framework port beside a companion on 8080", mainPort: 8000,
			companions: `[{"name":"web","port":8080}]`},
		{name: "companion on the framework port", mainPort: 8000,
			companions: `[{"name":"web","port":8000}]`, wantErr: `"main" and "web" both claim port 8000`},
		{name: "companion on the default port", mainPort: 0,
			companions: `[{"name":"web","port":8080}]`, wantErr: `"main" and "web" both claim port 8080`},
		{name: "companion without a port", mainPort: 8080,
			companions: `[{"name":"worker"}]`},
		{name: "undecodable companions", mainPort: 8080,
			companions: `{`, wantErr: "decode companions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep := state.Deployment{}
			if tc.companions != "" {
				dep.Sidecars = []byte(tc.companions)
			}
			err := checkCompanionPorts(api.AppManifest{Port: tc.mainPort}, dep)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("checkCompanionPorts: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("checkCompanionPorts error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
