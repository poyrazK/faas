package apidserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/dataapiinspect"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createDataAPIInspection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	if !middleware.HasScope(r, api.ScopesManagedPostgresReadSurface...) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Inspection unavailable", "Managed PostgreSQL read access is required."))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.CreateDataAPIInspectionRequest
	if decodeJSONSized(r, &req, 4096) != nil || !dataapiinspectioncommand.Valid([]string{dataapiinspectioncommand.Command, req.Table, strings.Repeat("a", 64)}, false) {
		api.WriteProblem(w, api.ErrValidation("invalid inspection request"))
		return
	}
	admissions, ok := s.store.(state.DataAPIInspectionAdmissionStore)
	reviews, hasReview := s.store.(state.ReleaseReviewStore)
	if !ok || !hasReview || s.managedPostgresBindings == nil {
		api.WriteProblem(w, api.ErrCapacity("Data API inspection is unavailable"))
		return
	}
	review, err := reviews.ReadReleaseReview(r.Context(), app.ID, api.DefaultEnvScope)
	if err != nil || review.AppID != app.ID || review.Scope != api.DefaultEnvScope || review.LatestDeploymentID == "" || !slices.Equal(review.ServingDeploymentIDs, []string{review.LatestDeploymentID}) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Inspection unavailable", "A single current live deployment is required."))
		return
	}
	fence, err := admissions.ReadDataAPIInspectionFence(r.Context(), acct.ID, app.ID, review.LatestDeploymentID, req.DatabaseID, req.BindingID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Inspection unavailable", "The selected binding, database or deployment is unavailable."))
		return
	}
	expectation, err := s.managedPostgresBindings.DataAPIBindingExpectation(r.Context(), acct.ID, app.ID, req.DatabaseID, req.BindingID)
	if err != nil || !dataapiinspectioncommand.ValidTrustBundleFingerprint(expectation.Target.TrustBundleSHA256) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Inspection unavailable", "The provider cannot establish the selected target and inspection trust."))
		return
	}
	var nonceBytes [32]byte
	if _, err = rand.Read(nonceBytes[:]); err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create inspection nonce"))
		return
	}
	nonce := hex.EncodeToString(nonceBytes[:])
	target, err := dataapiinspect.TargetDigest(nonce, expectation.Target.Host, expectation.Target.Port, expectation.Target.Database, expectation.Target.Login)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Data API inspection is unavailable"))
		return
	}
	taskID, err := admissions.AdmitDataAPIInspection(r.Context(), state.DataAPIInspectionAdmission{
		AccountID: acct.ID, AppID: app.ID, DeploymentID: review.LatestDeploymentID, DatabaseID: req.DatabaseID, BindingID: req.BindingID, Table: req.Table, Nonce: nonce,
		TargetDigest: target, TrustDigest: expectation.Target.TrustBundleSHA256, Fence: fence, CreatedAt: time.Now().UTC(),
		CredentialGeneration: expectation.CredentialGeneration, CredentialRef: expectation.CredentialRef, ProviderIdentityID: expectation.ProviderIdentityID,
		DatabaseResourceID: expectation.DatabaseResourceID, BackendID: expectation.BackendID, BackendFingerprint: expectation.BackendFingerprint,
		BindingUpdatedAt: expectation.BindingUpdatedAt, DatabaseUpdatedAt: expectation.DatabaseUpdatedAt, DatabaseDesiredGeneration: expectation.DatabaseDesiredGeneration, DatabaseObservedGeneration: expectation.DatabaseObservedGeneration,
	})
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Inspection unavailable", "The selected binding, database or deployment changed during admission."))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, api.DataAPIInspectionResponse{TaskID: taskID, AppID: app.ID, DeploymentID: review.LatestDeploymentID, DatabaseID: req.DatabaseID, BindingID: req.BindingID, CredentialGeneration: expectation.CredentialGeneration, TaskStatus: api.AppTaskStatusQueued, BindingIdentity: api.DataAPIInspectionIdentity{Status: "unknown", Reason: "inspection_pending"}})
}

func (s *server) getDataAPIInspection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	if !middleware.HasScope(r, api.ScopesManagedPostgresReadSurface...) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Inspection unavailable", "Managed PostgreSQL read access is required."))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	admissions, ok := s.store.(state.DataAPIInspectionAdmissionStore)
	if !ok || s.managedPostgresBindings == nil {
		api.WriteProblem(w, api.ErrCapacity("Data API inspection is unavailable"))
		return
	}
	record, err := admissions.ReadDataAPIInspectionRecord(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Inspection not found", "The requested inspection was not found."))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Data API inspection is unavailable"))
		return
	}
	var expected *managedpostgres.DataAPIBindingExpectation
	var after *state.DataAPIInspectionRecord
	if record.Current && record.Status == state.AppTaskSucceeded && record.ProtocolVersion == apptaskproto.DataAPIInspectionVersion {
		value, providerErr := s.managedPostgresBindings.DataAPIBindingExpectation(r.Context(), acct.ID, app.ID, record.DatabaseID, record.BindingID)
		if providerErr == nil {
			expected = &value
			current, currentErr := admissions.ReadDataAPIInspectionRecord(r.Context(), acct.ID, app.ID, record.TaskID)
			if currentErr == nil {
				after = &current
			}
		}
	}
	identity, observation := evaluateDataAPIInspection(record, expected, after, time.Now())
	response := api.DataAPIInspectionResponse{TaskID: record.TaskID, AppID: record.AppID, DeploymentID: record.DeploymentID, DatabaseID: record.DatabaseID, BindingID: record.BindingID, CredentialGeneration: record.CredentialGeneration, TaskStatus: api.AppTaskStatus(record.Status), BindingIdentity: identity, Observation: observation}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

// evaluateDataAPIInspection returns only a point-in-time target identity; it
// never claims that user identities or rows are authorized for writes.
func evaluateDataAPIInspection(record state.DataAPIInspectionRecord, expected *managedpostgres.DataAPIBindingExpectation, after *state.DataAPIInspectionRecord, now time.Time) (api.DataAPIInspectionIdentity, json.RawMessage) {
	unknown := func(reason string) (api.DataAPIInspectionIdentity, json.RawMessage) {
		return api.DataAPIInspectionIdentity{Status: "unknown", Reason: reason}, nil
	}
	if !record.Current {
		return unknown("binding_or_deployment_changed")
	}
	if !record.Status.Terminal() {
		return unknown("inspection_pending")
	}
	if record.Status != state.AppTaskSucceeded || record.OutputTruncated || record.ExitCode == nil || *record.ExitCode != 0 || record.FinishedAt == nil || now.Sub(*record.FinishedAt) > 5*time.Minute || record.FinishedAt.Sub(now) > 30*time.Second {
		return unknown("inspection_failed_or_stale")
	}
	if record.ProtocolVersion != apptaskproto.DataAPIInspectionVersion || !dataapiinspectioncommand.Valid(record.Command, false) || len(record.StdoutSHA256) != sha256.Size {
		return unknown("execution_evidence_unavailable")
	}
	digest := sha256.Sum256([]byte(record.Stdout))
	if subtle.ConstantTimeCompare(digest[:], record.StdoutSHA256) != 1 {
		return unknown("execution_evidence_unavailable")
	}
	observation, err := dataapiinspect.ParseObservation([]byte(record.Stdout), record.Command[1])
	if err != nil {
		return unknown("inspection_output_invalid")
	}
	if expected == nil || expected.AppID != record.AppID || expected.BindingID != record.BindingID || expected.DatabaseID != record.DatabaseID || expected.CredentialGeneration != record.CredentialGeneration {
		return unknown("provider_expectation_unavailable")
	}
	targetCurrent, targetErr := expected.MatchesObservedTarget(record.Command[2], record.TargetDigest)
	trustCurrent, trustErr := expected.MatchesObservedTrust(record.TrustDigest)
	if targetErr != nil || trustErr != nil || !targetCurrent || !trustCurrent {
		return unknown("provider_expectation_changed")
	}
	// The second read must still name exactly the pinned task and its output.
	if after == nil || !after.Current || after.TaskID != record.TaskID || after.AppID != record.AppID || after.DeploymentID != record.DeploymentID || after.DatabaseID != record.DatabaseID || after.BindingID != record.BindingID || after.Status != record.Status || after.OutputTruncated != record.OutputTruncated || after.ExitCode == nil || *after.ExitCode != 0 || after.FinishedAt == nil || !after.FinishedAt.Equal(*record.FinishedAt) || after.ProtocolVersion != record.ProtocolVersion || after.Stdout != record.Stdout || !slices.Equal(after.Command, record.Command) || after.TargetDigest != record.TargetDigest || after.TrustDigest != record.TrustDigest || after.CredentialGeneration != record.CredentialGeneration || !slices.Equal(after.StdoutSHA256, record.StdoutSHA256) {
		return unknown("binding_or_deployment_changed")
	}
	if subtle.ConstantTimeCompare([]byte(observation.TrustBundleSHA256), []byte(record.TrustDigest)) != 1 {
		return unknown("inspection_trust_mismatch")
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return unknown("inspection_output_invalid")
	}
	var public map[string]json.RawMessage
	if json.Unmarshal(raw, &public) != nil {
		return unknown("inspection_output_invalid")
	}
	delete(public, "target_digest")
	delete(public, "trust_bundle_sha256")
	publicJSON, err := json.Marshal(public)
	if err != nil {
		return unknown("inspection_output_invalid")
	}
	if subtle.ConstantTimeCompare([]byte(observation.TargetDigest), []byte(record.TargetDigest)) != 1 {
		return api.DataAPIInspectionIdentity{Status: "mismatch", Reason: "observed_target_differs"}, publicJSON
	}
	return api.DataAPIInspectionIdentity{Status: "verified", Reason: "provider_target_and_trust_matched"}, publicJSON
}
