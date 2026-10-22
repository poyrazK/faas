// adr: 1027 — write evidence is permission-pinned and requires successful cleanup.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestObjectStorageWriteVerificationAdmissionAndEvidence(t *testing.T) {
	for _, mode := range []string{"write", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			apidserver.EnableAppTaskAPIForTest(&e)
			app, _ := apidserver.SeedAppTaskDeployment(t, e, "storage-write")
			binding := apidserver.ReadyObjectStorageVerificationBinding(t, e, app, "default", mode)
			request := api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageWriteProbeCommand, "ASSETS", mode}, MaxOutputBytes: 4096}
			passed := api.ObjectStorageBindingProbeCheck{Status: "passed"}
			for _, failure := range []string{"", "legacy", "wrong mode", "missing cleanup", "failed cleanup", "download skipped"} {
				task := apidserver.CreateAppTaskForTest(t, e, app.Slug, request)
				stored, err := (*e.Store()).AppTaskByID(t.Context(), (*e.Acct()).ID, app.ID, task.ID)
				if err != nil || stored.BindingVerification == nil || stored.BindingVerification.ObjectStorageMode != mode {
					t.Fatal("permission missing from admission pin")
				}
				report := api.ObjectStorageBindingProbeReport{Prefix: "ASSETS", Mode: mode, Environment: passed, Configuration: passed, Connection: passed, Authorization: passed, BucketAccess: passed, Upload: &passed, Cleanup: &passed, Download: &api.ObjectStorageBindingProbeCheck{Status: "not_checked"}}
				if mode == "read_write" {
					report.Download = &passed
				}
				switch failure {
				case "legacy":
					report.Mode = ""
					report.Upload = nil
					report.Download = nil
					report.Cleanup = nil
				case "wrong mode":
					report.Mode = "other"
				case "missing cleanup":
					report.Cleanup = nil
				case "failed cleanup":
					report.Cleanup = &api.ObjectStorageBindingProbeCheck{Status: "failed", Detail: "PRIVATE_ERROR"}
				case "download skipped":
					report.Download = &api.ObjectStorageBindingProbeCheck{Status: "not_checked"}
					if mode == "write" {
						report.Download = &passed
					}
				}
				body, _ := json.Marshal(report)
				apidserver.CompleteVerificationTask(t, e, apidserver.BeginVerificationTask(t, e, task.ID), string(body))
				item, inventory := apidserver.SelectedObjectStorageVerification(t, e, app, binding.ID)
				if (item.VerificationStatus == "passed") != (failure == "") {
					t.Fatalf("%s qualified incorrectly: %+v", failure, item.Verification)
				}
				raw, _ := json.Marshal(inventory)
				if strings.Contains(string(raw), "PRIVATE_") {
					t.Fatal("private diagnostic leaked")
				}
			}
			request.Command[2] = "write"
			if mode == "write" {
				request.Command[2] = "read_write"
			}
			response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", request, nil)
			if response.Code != 403 {
				t.Fatalf("permission escalation admitted: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

type lockedStorageProbeStore struct{ MemStore *state.MemStore }

func (s *lockedStorageProbeStore) GetObjectBucketObjectLock(ctx context.Context, account, app, bucket string) (state.ObjectBucketObjectLock, error) {
	lock, err := s.MemStore.GetObjectBucketObjectLock(ctx, account, app, bucket)
	lock.EnabledRequired = true
	return lock, err
}

func TestObjectStorageWriteVerificationRejectsKnownRetention(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "storage-locked")
	apidserver.ReadyObjectStorageVerificationBinding(t, e, app, "default", "read_write")
	(*(*e.S()).Store()) = statetest.NewMem(&lockedStorageProbeStore{MemStore: (*e.Store())})
	r := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageWriteProbeCommand, "ASSETS", "read_write"}}, nil)
	if r.Code != 403 {
		t.Fatalf("write probe admitted into locked bucket: %d %s", r.Code, r.Body.String())
	}
}
