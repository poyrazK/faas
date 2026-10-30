package buildprogress_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/buildprogress"
)

// A shortened Railpack build as buildctl --progress plain prints it,
// including interleaved vertices, logs, pull statuses and a cached step.
const railpackBuild = `+ /usr/local/bin/railpack prepare /build/src
#1 [internal] load metadata for ghcr.io/railwayapp/railpack-builder:mise-2026.8.13
#1 DONE 1.2s

#2 [builder 1/1] FROM ghcr.io/railwayapp/railpack-builder:mise-2026.8.13
#2 sha256:aaa 12.6MB / 50.1MB 2.0s
#3 local context
#3 transferring context: 2.1MB 0.3s done
#3 DONE 0.4s

#2 [builder 1/1] FROM ghcr.io/railwayapp/railpack-builder:mise-2026.8.13
#2 extracting sha256:aaa 54.0s done
#2 DONE 60.2s

#4 copy package.json
#4 CACHED

#5 npm ci
#5 0.512 npm warn deprecated glob@7
#5 25.401 added 312 packages in 25s
#5 DONE 25.5s

#6 exporting to oci image format
#6 exporting layers 10.2s done
#6 DONE 12.5s

#7 exporting cache to client directory
#7 preparing build cache for export 106.4s done
#7 DONE 115.0s
`

func TestStepsFollowsInterleavedVertices(t *testing.T) {
	got := buildprogress.Steps([]byte(railpackBuild))
	want := []buildprogress.Step{
		{Name: "[internal] load metadata for ghcr.io/railwayapp/railpack-builder:mise-2026.8.13", Duration: 1200 * time.Millisecond},
		{Name: "[builder 1/1] FROM ghcr.io/railwayapp/railpack-builder:mise-2026.8.13", Duration: 60200 * time.Millisecond},
		{Name: "local context", Duration: 400 * time.Millisecond},
		{Name: "npm ci", Duration: 25500 * time.Millisecond},
		{Name: "exporting to oci image format", Duration: 12500 * time.Millisecond},
		{Name: "exporting cache to client directory", Duration: 115 * time.Second},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Steps =\n%+v\nwant\n%+v", got, want)
	}
}

func TestSummarizeRanksAndTotals(t *testing.T) {
	s := buildprogress.Summarize([]byte(railpackBuild), 3, 20)
	if s.ImageExport != 12500*time.Millisecond || s.CacheExport != 115*time.Second {
		t.Fatalf("exports = %s image, %s cache", s.ImageExport, s.CacheExport)
	}
	want := []buildprogress.Step{
		{Name: "exporting cache to c", Duration: 115 * time.Second},
		{Name: "[builder 1/1] FROM g", Duration: 60200 * time.Millisecond},
		{Name: "npm ci", Duration: 25500 * time.Millisecond},
	}
	if !reflect.DeepEqual(s.Slowest, want) {
		t.Fatalf("Slowest = %+v, want %+v", s.Slowest, want)
	}
}

func TestSummarizeWithoutProgress(t *testing.T) {
	for _, out := range []string{"", "+ railpack prepare\nerror: no start command\n", "#x DONE 1s\n#3\n#4 DONE soon\n"} {
		if s := buildprogress.Summarize([]byte(out), 5, 100); !reflect.DeepEqual(s, buildprogress.Summary{}) {
			t.Fatalf("Summarize(%q) = %+v, want nothing", out, s)
		}
	}
}

func TestSummarizeCutsNamesOnRuneBoundaries(t *testing.T) {
	s := buildprogress.Summarize([]byte("#1 RUN echo héllo\n#1 DONE 1.0s\n"), 1, 11)
	if len(s.Slowest) != 1 || s.Slowest[0].Name != "RUN echo h" {
		t.Fatalf("Slowest = %+v", s.Slowest)
	}
}
