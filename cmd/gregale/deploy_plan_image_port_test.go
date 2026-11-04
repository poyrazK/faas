package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

// Production-us rc.294: `gregale deploy --image nginx@sha256:...` planned
// "listener: :8080 · TCP listener readiness" although the image declares
// 80/tcp and the deploy served on 80 (Hunt 14 H14-38).
func TestImageDeployPlanNamesTheImagesPort(t *testing.T) {
	render := func(image bool, defaults []string, port int) string {
		var out bytes.Buffer
		main.RenderDeployPreflight(&out, main.DeployPreflightSummary{
			Slug: "h14-img", Source: "OCI image docker.io/library/nginx@sha256:abc", Image: image,
			SimpleAppPlan: &simpleapp.Plan{Slug: "h14-img", Port: port, ExecutionMode: "request", DefaultsApplied: defaults},
		})
		return out.String()
	}
	if got := render(true, []string{"port=8080"}, 8080); !strings.Contains(got, "listener:      image's declared port") || strings.Contains(got, ":8080") {
		t.Fatalf("image deploy with a defaulted port:\n%s", got)
	}
	if got := render(true, nil, 9000); !strings.Contains(got, "listener:      :9000") {
		t.Fatalf("image deploy with --port 9000:\n%s", got)
	}
	if got := render(false, []string{"port=8080"}, 8080); !strings.Contains(got, "listener:      :8080") {
		t.Fatalf("source deploy keeps its port:\n%s", got)
	}
}
