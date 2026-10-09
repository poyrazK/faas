package dashboard

import (
	"html/template"

	"github.com/onebox-faas/faas/pkg/api"
)

// AppChangesData is the /dashboard/apps/{slug}/changes page (ADR-741). The
// charts are pre-rendered SVG on one shared time axis: error rate, p95
// latency, and a strip of change markers.
type AppChangesData struct {
	Enabled  bool
	AppSlug  string
	Range    string
	Ranges   []string
	Timeline *api.AppChangeTimelineResponse
	// MetricsNote explains why the charts are absent (plan or telemetry);
	// empty when they rendered.
	MetricsNote  string
	ErrorRateSVG template.HTML
	P95SVG       template.HTML
	MarkersSVG   template.HTML
}
