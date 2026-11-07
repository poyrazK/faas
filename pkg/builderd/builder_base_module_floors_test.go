package builderd

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// BuildKit's own module graph selects dependency versions with published
// advisories, so the builder-base BuildKit stage raises them before it builds
// buildkitd and buildctl. A floor that drops below its fixed release, or stops
// being applied in that stage, ships a binary the image publication gate
// rejects (GO-2026-6865 failed the images workflow on main on 2026-11-06).
func TestBuilderBaseBuildKitModuleFloors(t *testing.T) {
	body, err := os.ReadFile("../../images/builder-base.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(body)
	stage := buildKitStage(t, dockerfile)
	for _, tc := range []struct {
		arg, module, fixed, advisory string
	}{
		{"BUILDKIT_COMPRESS_VERSION", "github.com/klauspost/compress", "v1.20.2", "GO-2026-6865"},
		{"CONTAINERD_VERSION", "github.com/containerd/containerd/v2", "v2.3.6", "GHSA-p7v4-vr35-mj6f"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			m := regexp.MustCompile(`(?m)^ARG ` + tc.arg + `=(\S+)$`).FindStringSubmatch(dockerfile)
			if m == nil {
				t.Fatalf("builder-base.Dockerfile declares no default for %s", tc.arg)
			}
			if got := "v" + m[1]; !semver.IsValid(got) || semver.Compare(got, tc.fixed) < 0 {
				t.Errorf("%s=%s is below %s, the first %s release fixing %s", tc.arg, m[1], tc.fixed, tc.module, tc.advisory)
			}
			for _, want := range []string{
				"ARG " + tc.arg + "\n",
				"go mod edit -require=" + tc.module + "@v${" + tc.arg + "}",
			} {
				if !strings.Contains(stage, want) {
					t.Errorf("the BuildKit stage does not apply %s: missing %q", tc.arg, want)
				}
			}
		})
	}
	if !strings.Contains(stage, `grep "github.com/klauspost/compress" | grep -vq "v${BUILDKIT_COMPRESS_VERSION}"`) {
		t.Error("the BuildKit stage no longer fails when buildctl or buildkitd links another klauspost/compress version")
	}
}

// buildKitStage returns the buildkit-client-build stage, up to the next FROM.
func buildKitStage(t *testing.T, dockerfile string) string {
	t.Helper()
	start := strings.Index(dockerfile, " AS buildkit-client-build\n")
	if start < 0 {
		t.Fatal("builder-base.Dockerfile has no buildkit-client-build stage")
	}
	stage := dockerfile[start:]
	if end := strings.Index(stage, "\nFROM "); end >= 0 {
		stage = stage[:end]
	}
	return stage
}
