package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	objruntime "github.com/onebox-faas/faas/pkg/rehearsalobj/runtime"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const scenarioObjectCleanupTimeout = 5 * time.Second

func (s *server) configureRehearsalObjects(ctx context.Context, getenv func(string) string) error {
	s.rehearsalObjectsEnabled = getenv("FAAS_REHEARSAL_OBJECT_FIXTURES") == "1"
	catalog, ok := s.store.(rehearsalobj.Catalog)
	if !ok {
		if s.rehearsalObjectsEnabled {
			return errors.New("rehearsal object catalog unavailable")
		}
		return nil
	}
	s.rehearsalObjects = objruntime.NewService(catalog)
	go s.rehearsalObjects.Run(ctx)
	return nil
}
func (s *server) createScenarioObjects(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.rehearsalObjectsEnabled || s.rehearsalObjects == nil || !s.rehearsalCommandsEnabled || !s.rehearsalNetworkEnabled || !s.rehearsalSimulationEnabled {
		api.WriteProblem(w, api.ErrRehearsalNotEnabled("Isolated object storage fixtures"))
		return
	}
	var req rehearsalobj.CreateRequest
	if decodeJSON(r, &req) != nil || req.Validate() != nil {
		api.WriteProblem(w, api.ErrValidation("invalid object fixture contract"))
		return
	}
	app, e := s.store.AppBySlug(r.Context(), req.AppSlug)
	if e != nil || app.AccountID != acct.ID || !eligibleScenarioPostgresApp(app) {
		s.notFound(w, "no such test workload")
		return
	}
	member, e := s.store.ScenarioTestMemberByApp(r.Context(), app.ID)
	if e != nil || member.AccountID != acct.ID || member.RunID != r.PathValue("run_id") || member.NetworkMode != "contained" || member.SimulationJSON == "" {
		s.notFound(w, "no such contained test workload")
		return
	}
	s.allocateScenarioObjects(w, r, acct, app, member, req)
}
func (s *server) allocateScenarioObjects(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, member state.ScenarioTestMember, req rehearsalobj.CreateRequest) {
	if setSecretRecipient == nil || setSecretRecipient() == nil {
		api.WriteProblem(w, api.ErrCapacity("object fixture secret sealing unavailable"))
		return
	}
	access, secret, e := api.GenerateObjectS3Credential()
	if e != nil {
		api.WriteProblem(w, api.ErrCapacity("object fixture credential generation unavailable"))
		return
	}
	sealed, e := secretbox.SealBytes(setSecretRecipient(), rehearsalobj.CredentialNamespace, []byte(secret), 64)
	if e != nil {
		api.WriteProblem(w, api.ErrCapacity("object fixture secret sealing unavailable"))
		return
	}
	values := map[string]string{"AWS_ACCESS_KEY_ID": access, "AWS_SECRET_ACCESS_KEY": secret, "AWS_REGION": rehearsalobj.Region, "AWS_ENDPOINT_URL_S3": "http://" + member.Workload + ".svc.gregale:" + strconv.Itoa(rehearsalobj.ProxyPort), "S3_BUCKET": req.Spec.Bucket}
	limits, _ := api.LimitsFor(acct.Plan)
	for key := range values {
		if problem := s.checkSecretQuota(r.Context(), acct, app, "default", key, limits); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	receipt, e := s.rehearsalObjects.Create(r.Context(), acct.ID, member.RunID, app.ID, access, sealed, req)
	if e != nil {
		api.WriteProblem(w, api.ErrCapacity("create isolated object fixture"))
		return
	}
	for key, value := range values {
		if problem := s.sealAndPersist(r.Context(), acct, app, "default", key, value, api.SecretClassEphemeral, limits); problem != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), scenarioObjectCleanupTimeout)
			_ = s.rehearsalObjects.Delete(cleanup, acct.ID, member.RunID)
			cancel()
			api.WriteProblem(w, problem)
			return
		}
	}
	if e := s.bindScenarioObjectParticipants(r.Context(), acct, member, values); e != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), scenarioObjectCleanupTimeout)
		defer cancel()
		_ = s.rehearsalObjects.Delete(cleanup, acct.ID, member.RunID)
		api.WriteProblem(w, api.ErrCapacity("object participant binding failed; fixture cleanup is required"))
		return
	}
	writeJSON(w, http.StatusCreated, receipt)
}
func (s *server) sealScenarioObjects(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.rehearsalObjects == nil {
		api.WriteProblem(w, api.ErrCapacity("isolated object collector unavailable"))
		return
	}
	var req rehearsalobj.SealRequest
	if decodeJSON(r, &req) != nil {
		api.WriteProblem(w, api.ErrValidation("deployment_id is required"))
		return
	}
	resource, e := s.rehearsalObjects.Catalog.GetScenarioObjects(r.Context(), acct.ID, r.PathValue("run_id"))
	if e != nil {
		s.notFound(w, "no such object fixture")
		return
	}
	dep, e := s.store.DeploymentByID(r.Context(), req.DeploymentID)
	if e != nil || dep.AppID != resource.AppID || resource.Evidence != nil && resource.Evidence.DeploymentID != req.DeploymentID {
		api.WriteProblem(w, api.ErrValidation("deployment does not own this fixture"))
		return
	}
	actions, ok := s.store.(state.ScenarioActionSnapshotStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sealed action evidence unavailable"))
		return
	}
	snapshot, e := actions.SealScenarioTestActions(r.Context(), acct.ID, resource.RunID)
	if e != nil || snapshot.Status != "complete" {
		api.WriteProblem(w, api.ErrValidation("complete action evidence is required before object sealing"))
		return
	}
	evidence, e := s.rehearsalObjects.Seal(r.Context(), acct.ID, resource.RunID, dep.ID)
	if e != nil {
		api.WriteProblem(w, api.ErrCapacity("object evidence is incomplete; fixture cleanup is required"))
		return
	}
	writeJSON(w, http.StatusOK, evidence)
}
func (s *server) deleteScenarioObjects(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.rehearsalObjects == nil {
		api.WriteProblem(w, api.ErrCapacity("isolated object cleanup unavailable"))
		return
	}
	if e := s.rehearsalObjects.Delete(r.Context(), acct.ID, r.PathValue("run_id")); e != nil {
		api.WriteProblem(w, api.ErrCapacity("object fixture cleanup was not confirmed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
