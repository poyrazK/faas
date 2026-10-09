package dashboard

import (
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestCPUChartDrillDownPreservesScopeAndMissingIntervals(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	q := api.ProfileQuery{DeploymentID: "chosen-deployment", Runtime: "node24", Start: now, End: now.Add(4 * time.Minute)}
	chart := BuildProfileCPUChart("an-app", q, q, []promql.QueryRangeSample{
		{Timestamp: now.Add(time.Minute).Unix(), Value: .2},
		{Timestamp: now.Add(2 * time.Minute).Unix(), Value: math.NaN()},
		{Timestamp: now.Add(3 * time.Minute).Unix(), Value: 0},
		{Timestamp: now.Add(4 * time.Minute).Unix(), Value: 2},
	})
	if chart.Error != "" || len(chart.Points) != 3 || chart.PeakCores != 2 || chart.Points[1].Height <= 0 {
		t.Fatal(chart)
	}
	point := chart.Points[2]
	u, err := url.Parse(point.URL)
	if err != nil {
		t.Fatal(err)
	}
	values := u.Query()
	if values.Get("deployment_id") != q.DeploymentID || values.Get("runtime") != q.Runtime || values.Get("chart_start") != chart.Start || values.Get("chart_end") != chart.End || values.Get("start") != now.Add(3*time.Minute).Format(time.RFC3339Nano) || values.Get("end") != now.Add(4*time.Minute).Format(time.RFC3339Nano) {
		t.Fatal(values)
	}
	if chart.Points[1].X <= chart.Points[0].X+chart.Points[0].Width {
		t.Fatal("missing samples were filled in")
	}
	if !strings.Contains(ProfileCPUQuery("tenant-app", time.Minute), `app="tenant-app"`) {
		t.Fatal("CPU query omitted app scope")
	}
}

func TestCPUChartBoundsLongWindowsAndShowsUnavailableData(t *testing.T) {
	window := 14 * 24 * time.Hour
	if got := window / ProfileChartStep(window); got > api.ProfileMaxChartPoints {
		t.Fatal("unbounded chart", got)
	}
	now := time.Now()
	q := api.ProfileQuery{Start: now, End: now.Add(time.Hour)}
	chart := BuildProfileCPUChart("app", q, q, nil)
	if chart.Error == "" || len(chart.Points) != 0 {
		t.Fatal("missing data became zero", chart)
	}
}
