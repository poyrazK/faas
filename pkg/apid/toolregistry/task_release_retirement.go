package toolregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// TaskReleaseRetire delegates only through native conditional parking. Its
// concrete transaction consumes approval with the actual app park transition.
func (e *PrivateExecutor) TaskReleaseRetire(park httpkit.AccountHandler) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		r, err := e.freshPublicationRequest(r, acct, false)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if !personalAccess(w, r, acct, e.store, true) {
			return
		}
		p, _ := httpkit.PrincipalFrom(r)
		journal, ok := e.store.(state.TaskToolReleaseIntentStore)
		atomic, supported := e.store.(state.TaskToolRetirementParkStore)
		if p.Key == nil || !ok || !supported || !atomic.TaskToolRetirementParkSupported() || !toolpublication.ValidID(r.PathValue("id")) {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		var in api.TaskToolRetirementRequest
		if httpkit.DecodeJSONSized(r, &in, api.TaskToolReleaseIntentMaxBytes) != nil || in.Action != "park" {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		e.acceptTaskRetirement(w, r, acct, p.Key.ID, in, journal, park)
	}
}
func (e *PrivateExecutor) acceptTaskRetirement(w http.ResponseWriter, r *http.Request, acct state.Account, key string, in api.TaskToolRetirementRequest, journal state.TaskToolReleaseIntentStore, park httpkit.AccountHandler) {
	record, err := journal.GetTaskToolReleaseIntent(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if record.RequesterKeyID != key {
		respond(w, nil, state.ErrNotFound)
		return
	}
	if !tasktoolrelease.RetirementIdentity(record, in.QualificationSHA256) || !tasktoolrelease.ValidRetirement(record) {
		respond(w, nil, state.ErrConflict)
		return
	}
	q := record.Execution.Recovery.Retirement
	if q.State != "qualified" {
		respond(w, record, nil)
		return
	}
	if _, err := tasktoolrelease.Retirement(record, api.TaskToolReleaseIntentDecision{ReleaseFingerprint: record.Intent.ReleaseFingerprint, Retirement: &in}, true, time.Now(), uuid.NewString()); err != nil {
		respond(w, nil, state.ErrConflict)
		return
	}
	e.executeTaskRetirement(w, r, acct, key, in, record, journal, park)
}
func (e *PrivateExecutor) executeTaskRetirement(w http.ResponseWriter, r *http.Request, acct state.Account, key string, in api.TaskToolRetirementRequest, record api.TaskToolReleaseIntentRecord, journal state.TaskToolReleaseIntentStore, park httpkit.AccountHandler) {
	worker := ""
	for _, pin := range record.Intent.Apps {
		if pin.Role == "worker" {
			worker = pin.App
		}
	}
	payload, _ := json.Marshal(map[string]string{"expected_deployment_id": record.Execution.Receipt.WorkerDeploymentID})
	request := r.Clone(state.WithTaskToolRetirementPark(r.Context(), acct.ID, key, record.ID, in))
	request.SetPathValue("slug", worker)
	request.URL.RawQuery = ""
	request.Body = io.NopCloser(bytes.NewReader(payload))
	request.ContentLength = int64(len(payload))
	capture := &taskRestoreWriter{header: make(http.Header), status: 200}
	park(capture, request, acct)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), api.DefaultClientTimeout)
	defer cancel()
	latest, err := journal.GetTaskToolReleaseIntent(cleanup, acct.ID, record.ID)
	if err == nil && tasktoolrelease.RetirementIdentity(latest, in.QualificationSHA256) && tasktoolrelease.ValidRetirement(latest) && latest.Execution.Recovery.Retirement.State != "qualified" {
		respond(w, latest, nil)
		return
	}
	if capture.status >= 400 && capture.body.Len() > 0 {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
		return
	}
	respond(w, nil, state.ErrConflict)
}
