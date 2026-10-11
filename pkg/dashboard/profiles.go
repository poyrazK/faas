package dashboard

import (
	"fmt"
	"math"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

type ProfileCPUChart struct {
	Points     []ProfileCPUPoint
	Start, End string
	PeakCores  float64
	Error      string
}

type ProfileCPUPoint struct {
	X, Y, Width, Height float64
	Cores               float64
	Start, End          string
	URL                 string
	Selected            bool
}

// ProfileChartStep bounds query and rendering work for every plan window.
func ProfileChartStep(window time.Duration) time.Duration {
	step := time.Duration(math.Ceil(window.Seconds()/float64(api.ProfileMaxChartPoints))) * time.Second
	return max(api.ProfileChartMinStep, step)
}

// BuildProfileCPUChart leaves missing samples as gaps. Each clickable bar
// represents the same preceding interval used by the Prometheus rate query.
func BuildProfileCPUChart(slug string, selected, overview api.ProfileQuery, samples []promql.QueryRangeSample) ProfileCPUChart {
	chart := ProfileCPUChart{Start: overview.Start.UTC().Format(time.RFC3339Nano), End: overview.End.UTC().Format(time.RFC3339Nano)}
	window := overview.End.Sub(overview.Start)
	if window <= 0 || len(samples) > api.ProfileMaxChartPoints+1 {
		chart.Error = "CPU chart exceeds supported bounds."
		return chart
	}
	step := ProfileChartStep(window)
	for _, sample := range samples {
		if math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) || sample.Value < 0 {
			continue
		}
		end := time.Unix(sample.Timestamp, 0)
		if end.After(overview.End) || !end.After(overview.Start) {
			continue
		}
		start := maxTime(overview.Start, end.Add(-step))
		point := ProfileCPUPoint{Cores: sample.Value, Start: start.UTC().Format(time.RFC3339Nano), End: end.UTC().Format(time.RFC3339Nano), Selected: selected.Start.Equal(start) && selected.End.Equal(end)}
		values := url.Values{"route": {selected.Route}, "deployment_id": {selected.DeploymentID}, "runtime": {selected.Runtime}, "start": {point.Start}, "end": {point.End}, "chart_start": {chart.Start}, "chart_end": {chart.End}}
		point.URL = "/dashboard/apps/" + url.PathEscape(slug) + "/profiles?" + values.Encode()
		point.X = 1100 * start.Sub(overview.Start).Seconds() / window.Seconds()
		point.Width = max(1, 1100*end.Sub(start).Seconds()/window.Seconds()-1)
		chart.PeakCores = max(chart.PeakCores, point.Cores)
		chart.Points = append(chart.Points, point)
	}
	for i := range chart.Points {
		height := float64(0)
		if chart.PeakCores > 0 {
			height = 140 * chart.Points[i].Cores / chart.PeakCores
		}
		chart.Points[i].Height = max(2, height)
		chart.Points[i].Y = 150 - chart.Points[i].Height
	}
	if len(chart.Points) == 0 {
		chart.Error = "No CPU measurements are available for this chart window."
	}
	return chart
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func ProfileCPUQuery(appID string, step time.Duration) string {
	return fmt.Sprintf("sum(rate(schedd_instance_cpu_seconds_total{app=%q}[%ds]))", appID, int64(step.Seconds()))
}
