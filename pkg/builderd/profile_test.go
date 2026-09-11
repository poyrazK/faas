package builderd

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/apihostingcontract"
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

func TestFunctionRuntimeFramework(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		runtime string
		want    Framework
	}{
		{runtime: "node22", want: FrameworkNode},
		{runtime: "python313", want: FrameworkPython},
		{runtime: "go124-alpine", want: FrameworkGo},
	} {
		t.Run(tt.runtime, func(t *testing.T) {
			got, ok := functionRuntimeFramework(state.App{Type: state.AppTypeFunction, Runtime: tt.runtime})
			if !ok || got != tt.want {
				t.Fatalf("functionRuntimeFramework(%q) = (%q, %t), want (%q, true)", tt.runtime, got, ok, tt.want)
			}
		})
	}
	if got, ok := functionRuntimeFramework(state.App{Type: state.AppTypeApp, Runtime: "python313"}); ok || got != FrameworkUnknown {
		t.Fatalf("plain app runtime mapping = (%q, %t), want (unknown, false)", got, ok)
	}
}

func TestCatalogRuntimeProfilesMapToBuilderPipelines(t *testing.T) {
	t.Parallel()
	catalog, err := apihostingcontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range catalog.Fixtures {
		fixture := fixture
		if !hasProfileTag(fixture, "runtime") {
			continue
		}
		t.Run(fixture.ID, func(t *testing.T) {
			want, ok := frameworkFromProfile(fixture.Expected.Framework)
			if !ok {
				t.Fatalf("catalog framework %q has no builder pipeline", fixture.Expected.Framework)
			}
			raw, err := json.Marshal(frameworkprofile.Profile{
				Version:   frameworkprofile.Version,
				Framework: fixture.Expected.Framework,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, _, used := persistedProfileFramework(state.Deployment{InferredProfile: raw})
			if !used || got != want {
				t.Fatalf("catalog framework %q mapped to (%q, used=%t), want (%q, used=true)", fixture.Expected.Framework, got, used, want)
			}
		})
	}
}

func hasProfileTag(fixture apihostingcontract.Fixture, want string) bool {
	for _, tag := range fixture.Tags {
		if tag == want {
			return true
		}
	}
	return false
}
