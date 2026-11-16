package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Hunt 34 H34-8: the __route_other__ series was reported as "1 routes beyond
// RouteMetricsPerAppCap", though it also holds every undeclared path, so an
// app with no declared routes always seemed over the cap.
func TestThrottleSuggestionsDescribeTheOtherRoutesBucket(t *testing.T) {
	var out bytes.Buffer
	main.RenderThrottleSuggestions(&out, "demo", api.ThrottleSuggestionsResponse{RoutesCollapsed: 1})
	got := out.String()
	if strings.Contains(got, "beyond RouteMetricsPerAppCap") || !strings.Contains(got, "requests to undeclared paths, or to routes beyond the 50-route cap") {
		t.Fatalf("output = %s", got)
	}
}
