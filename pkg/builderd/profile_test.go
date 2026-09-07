package builderd

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPersistedProfileFramework(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		framework string
		want      Framework
		used      bool
	}{
		{name: "express", framework: "express", want: FrameworkNode, used: true},
		{name: "fastapi", framework: "fastapi", want: FrameworkPython, used: true},
		{name: "gin", framework: "gin", want: FrameworkGo, used: true},
		{name: "docker", framework: "oci", want: FrameworkDocker, used: true},
		{name: "unknown", framework: "unknown", want: FrameworkUnknown, used: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(frameworkprofile.Profile{
				Version: frameworkprofile.Version, Framework: tc.framework, FrameworkVer: "1.2.3",
			})
			if err != nil {
				t.Fatal(err)
			}
			fw, ver, used := persistedProfileFramework(state.Deployment{InferredProfile: raw})
			if fw != tc.want || used != tc.used {
				t.Fatalf("persistedProfileFramework = (%q, %t), want (%q, %t)", fw, used, tc.want, tc.used)
			}
			if used && ver != "1.2.3" {
				t.Errorf("framework version = %q, want 1.2.3", ver)
			}
		})
	}
}

func TestPersistedProfileFrameworkFallsBackForUnknownVersion(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(frameworkprofile.Profile{Version: "v2", Framework: "express"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, used := persistedProfileFramework(state.Deployment{InferredProfile: raw}); used {
		t.Fatal("newer profile version must use detector fallback")
	}
}
