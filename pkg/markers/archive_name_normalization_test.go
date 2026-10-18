// adr: 088
package markers

import "testing"

func TestSourceMarkersWithRepeatedLeadingDot(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prefix     string
		sourceRoot string
	}{
		{"flat", "././", ""},
		{"wrapped", "././project/", ""},
		{"wrapped workspace", "././project/apps/api/", "apps/api"},
		{"direct workspace", "././project/apps/api/", "project/apps/api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := tempTarball(t)
			writeTarGz(t, archive, map[string]string{
				tc.prefix + "package.json": `{"engines":{"node":">=22.11.0"}}`,
				tc.prefix + ".nvmrc":       "v24.1.0\n",
			})
			if framework, err := DetectFromTarballAtRoot(archive, tc.sourceRoot); err != nil || framework != FrameworkNode {
				t.Errorf("source framework = %q, %v; want node", framework, err)
			}
			if version := VersionFromTarballAtRoot(archive, FrameworkNode, tc.sourceRoot); version != "24.1.0" {
				t.Errorf("source version = %q, want 24.1.0 from .nvmrc", version)
			}
		})
	}
}
