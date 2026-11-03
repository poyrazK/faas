//go:build linux

package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildClockReportsStagesAndBuildKitSteps(t *testing.T) {
	clock := &buildClock{start: time.Now().Add(-2 * time.Second), guestStartMS: 1500}
	clock.stage("buildkit-ready")
	clock.finishBuild(250*time.Second, []byte("#1 npm ci\n#1 DONE 25.5s\n#2 exporting to oci image format\n#2 DONE 12.5s\n#3 exporting cache to client directory\n#3 DONE 115.0s\n"))
	got := clock.timings()
	if len(got.Stages) != 1 || got.Stages[0].Stage != "buildkit-ready" || got.Stages[0].AtMS < 2000 {
		t.Fatalf("stages = %+v", got.Stages)
	}
	if got.GuestStartMS != 1500 || got.BuildMS != 250_000 || got.ImageExportMS != 12_500 || got.CacheExportMS != 115_000 {
		t.Fatalf("timings = %+v", got)
	}
	want := []api.BuildStepTiming{{Name: "exporting cache to client directory", MS: 115_000}, {Name: "npm ci", MS: 25_500}, {Name: "exporting to oci image format", MS: 12_500}}
	if !reflect.DeepEqual(got.SlowestSteps, want) {
		t.Fatalf("slowest = %+v, want %+v", got.SlowestSteps, want)
	}
	if uptimeMS() <= 0 {
		t.Fatal("no uptime on a Linux host")
	}
}
