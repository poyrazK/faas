package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production 2026-11-05 (rc.324, #6639): a wake of an app whose instance was
// frozen queued a new wake id, schedd thawed the VM under its own wake_id,
// and `gregale app wake --wait` waited out its timeout for an id no instance
// carried. The 202 now names the frozen VM, and the wake request asks schedd
// to thaw that one.
func TestWakeAppNamesTheFrozenInstanceItResumes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		instance   string // state of the app's one instance; "" seeds none
		otherDep   bool   // the instance belongs to a deployment that is not live
		wantResume bool
	}{
		{name: "frozen instance of the live deployment", instance: string(state.StateFrozen), wantResume: true},
		{name: "frozen instance of an older deployment", instance: string(state.StateFrozen), otherDep: true},
		{name: "no instance", instance: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, notif := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
			store := *e.Store()
			dep := apidserver.MustSeedDeployment(t, e, "wake-frozen")
			if err := store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
				t.Fatal(err)
			}
			var seeded state.Instance
			if tc.instance != "" {
				deploymentID := dep.ID
				if tc.otherDep {
					old, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: dep.AppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:old"})
					if err != nil {
						t.Fatal(err)
					}
					deploymentID = old.ID
				}
				var err error
				seeded, err = store.CreateInstance(t.Context(), dep.AppID, deploymentID, tc.instance, 256, "node-1", "")
				if err != nil {
					t.Fatal(err)
				}
			}
			rec := e.Do(t, http.MethodPost, "/v1/apps/wake-frozen/wake", nil, nil)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var response api.AppWakeResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			wantInstance := ""
			if tc.wantResume {
				wantInstance = seeded.ID
			}
			if response.AlreadyRunning || response.WakeID == "" || response.WakeID == seeded.WakeID || response.InstanceID != wantInstance {
				t.Fatalf("response = %+v, want a new wake id resuming instance %q", response, wantInstance)
			}
			var request struct {
				AppID      string `json:"app_id"`
				WakeID     string `json:"wake_id"`
				InstanceID string `json:"instance_id"`
			}
			notif.Mu().Lock()
			defer notif.Mu().Unlock()
			queued := 0
			for _, emitted := range *notif.Emitted() {
				if emitted.Channel != db.NotifyAppWake {
					continue
				}
				queued++
				if err := json.Unmarshal([]byte(emitted.Payload), &request); err != nil {
					t.Fatal(err)
				}
			}
			if queued != 1 || request.AppID != dep.AppID || request.WakeID != response.WakeID || request.InstanceID != wantInstance {
				t.Fatalf("app_wake requests = %d, last %+v; want one for wake %s resuming %q", queued, request, response.WakeID, wantInstance)
			}
		})
	}
}
