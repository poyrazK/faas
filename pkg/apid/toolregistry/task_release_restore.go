package toolregistry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// TaskReleaseRestore delegates through all ordinary native traffic gates. The
// traffic store consumes the owner approval in the same transaction as the CAS.
func (e *PrivateExecutor) TaskReleaseRestore(traffic httpkit.AccountHandler) httpkit.AccountHandler {
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
		principal, _ := httpkit.PrincipalFrom(r)
		journal, ok := e.store.(state.TaskToolReleaseIntentStore)
		atomic, supported := e.store.(state.TaskToolRecoveryTrafficStore)
		if principal.Key == nil || !ok || !supported || !atomic.TaskToolRecoveryTrafficSupported() || !toolpublication.ValidID(r.PathValue("id")) {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		var in api.TaskToolRecoveryRestoreRequest
		if httpkit.DecodeJSONSized(r, &in, api.TaskToolReleaseIntentMaxBytes) != nil {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		e.acceptTaskRestore(w, r, acct, in, principal.Key.ID, journal, traffic)
	}
}

func (e *PrivateExecutor) acceptTaskRestore(w http.ResponseWriter, r *http.Request, acct state.Account, in api.TaskToolRecoveryRestoreRequest, key string, journal state.TaskToolReleaseIntentStore, traffic httpkit.AccountHandler) {
	record, err := journal.GetTaskToolReleaseIntent(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if record.RequesterKeyID != key {
		respond(w, nil, state.ErrNotFound)
		return
	}
	if !tasktoolrelease.RestoreIdentity(record, in) {
		respond(w, nil, state.ErrConflict)
		return
	}
	if record.Execution.Recovery.Result != nil {
		if !tasktoolrelease.ValidRecoveryResult(record) {
			respond(w, nil, state.ErrConflict)
			return
		}
		respond(w, record, nil)
		return
	}
	if _, err := tasktoolrelease.Restore(record, in, time.Now()); err != nil {
		respond(w, nil, state.ErrConflict)
		return
	}
	e.executeTaskRestore(w, r, acct, record, in, key, journal, traffic)
}

func (e *PrivateExecutor) executeTaskRestore(w http.ResponseWriter, r *http.Request, acct state.Account, record api.TaskToolReleaseIntentRecord, in api.TaskToolRecoveryRestoreRequest, key string, journal state.TaskToolReleaseIntentStore, traffic httpkit.AccountHandler) {
	q := record.Execution.Recovery.Qualification
	payload, _ := json.Marshal(api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &q.ExpectedServingDeploymentID, ExpectedMCPAccessPolicy: in.ExpectedMCPAccessPolicy})
	request := r.Clone(state.WithTaskToolRecoveryTraffic(r.Context(), acct.ID, key, record.ID, in))
	request.SetPathValue("id", q.TargetDeploymentID)
	request.Body, request.ContentLength = io.NopCloser(bytes.NewReader(payload)), int64(len(payload))
	capture := &taskRestoreWriter{header: make(http.Header), status: 200}
	traffic(capture, request, acct)
	// Even an error/lost response can follow a committed write. Return only the
	// retained receipt; never replay the traffic request or infer success from 2xx.
	latest, err := journal.GetTaskToolReleaseIntent(r.Context(), acct.ID, record.ID)
	if err == nil && tasktoolrelease.RestoreIdentity(latest, in) && tasktoolrelease.ValidRecoveryResult(latest) {
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

type taskRestoreWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *taskRestoreWriter) Header() http.Header    { return w.header }
func (w *taskRestoreWriter) WriteHeader(status int) { w.status = status }
func (w *taskRestoreWriter) Write(p []byte) (int, error) {
	if w.body.Len()+len(p) > 2*api.TaskToolReleaseIntentMaxBytes {
		w.status = 500
		return 0, errors.New("native traffic response exceeded its bound")
	}
	return w.body.Write(p)
}
