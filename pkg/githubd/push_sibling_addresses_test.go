// adr: 20261105104500
package githubd

import (
	"context"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// A push sends each service's Compose values that call a sibling, rewritten
// to the sibling's Gregale address, with its build, without the
// x-gregale-env-import opt-in; no other value travels.
func TestPushSendsSiblingAddressesWithTheBuild(t *testing.T) {
	compose := "services:\n" +
		"  web:\n    build: ./web\n    environment:\n      BILLING_URL: http://billing:8080/v1\n      LOG_LEVEL: debug\n" +
		"  billing:\n    build: ./billing\n"
	fsys := fstest.MapFS{
		"compose.yaml":       &fstest.MapFile{Data: []byte(compose)},
		"web/Dockerfile":     &fstest.MapFile{Data: []byte("FROM scratch\n")},
		"billing/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")},
	}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "web", RootDir: "web", DetectedBy: reposcan.Detection{Detector: "compose"}},
		{Class: reposcan.ClassHTTP, Name: "billing", RootDir: "billing", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	rig, svc, rec, _ := pushRig(t, &scan, fsys)
	push(t, svc, "c0ffee01")
	apps, err := rig.mem.AppsForProject(context.Background(), rig.acct, mustProjectID(t, rig))
	if err != nil || len(apps) != 2 {
		t.Fatalf("apps = %d, %v", len(apps), err)
	}
	byApp := map[string]map[string]string{}
	for _, call := range rec.calls {
		byApp[call.appID] = call.addresses
	}
	for _, app := range apps {
		got := byApp[app.ID]
		switch app.WorkloadName {
		case "web":
			if len(got) != 1 || got["BILLING_URL"] != "http://billing.svc.gregale:"+strconv.Itoa(api.ServiceBindingPort)+"/v1" {
				t.Fatalf("web addresses = %v, want only the rewritten BILLING_URL", got)
			}
		case "billing":
			if len(got) != 0 {
				t.Fatalf("billing addresses = %v, want none", got)
			}
		}
	}
}
