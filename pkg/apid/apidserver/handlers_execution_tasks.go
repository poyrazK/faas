package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) WithExecutionTasksEnabled(enabled bool) *server {
	s.executionTasksEnabled = enabled
	s.refreshRunAgentTools()
	return s
}
func (s *server) executionTaskService(r *http.Request) runtasks.Service {
	store, _ := s.store.(state.ExecutionTaskStore)
	admissions, _ := s.store.(state.ExecutionAdmissionStore)
	return runtasks.Service{Store: store, Admissions: admissions, Enabled: s.executionAPIEnabled && s.executionV2Enabled && s.executionTasksEnabled, HMACKey: hostHMACKeyBytes(), AgentFiles: s.agentFileSigner(),
		Validate: func(ctx context.Context, a runtasks.Access, task api.PublishExecutionTaskRequest) error {
			return s.validateExecutionTaskPublication(ctx, r, a, task)
		},
		Seal: func(_ context.Context, v state.ExecutionTaskVersion, task api.PublishExecutionTaskRequest) ([]byte, string, error) {
			recipients, kid, err := workflowPlanRecipients()
			if err != nil {
				return nil, "", err
			}
			blob, err := runtask.Seal(recipients, v.AccountID, v.ID, v.Digest, task)
			return blob, kid, err
		},
		Open: func(_ context.Context, v state.ExecutionTaskVersion) (api.PublishExecutionTaskRequest, error) {
			if !holdsIdentityFor(accountIdentities(), v.PayloadKID) {
				return api.PublishExecutionTaskRequest{}, errors.New("task definition reader identity unavailable")
			}
			return runtask.Open(accountIdentities(), v.AccountID, v.ID, v.Digest, v.SealedDefinition)
		},
		Prepare: func(ctx context.Context, reader state.ExecutionAdmissionReader, a runtasks.Access, request api.CreateExecutionRequest) (state.CreateExecutionParams, error) {
			access := executionAccess{broad: a.Wide, controlWide: a.ControlWide, principal: a.Principal}
			if a.Principal != nil {
				access.principalID = *a.Principal
			}
			params, _, err := s.prepareExecutionAdmission(ctx, reader, a.Account, access, request)
			return params, err
		}}
}
func (s *server) validateExecutionTaskPublication(ctx context.Context, r *http.Request, a runtasks.Access, task api.PublishExecutionTaskRequest) error {
	if err := s.requireExecutionRequestFeatures(task.Definition); err != nil {
		return err
	}
	if id := task.Definition.EnvironmentID; id != "" {
		store, ok := s.store.(state.ExecutionEnvironmentStore)
		if !ok {
			return api.ErrCapacity("Run environment storage unavailable")
		}
		v, err := store.ExecutionEnvironmentByID(ctx, a.Account.ID, id)
		if err != nil || v.Status != "ready" || v.Runtime != task.Definition.Runtime {
			return state.ErrExecutionEnvironmentUnavailable
		}
	}
	inbox, _ := s.store.(agents.CallbackInbox)
	for _, id := range task.AgentEndpointIDs {
		if inbox == nil {
			return api.ErrCapacity("agent endpoints unavailable")
		}
		endpoint, err := inbox.ManagedRealtimeEndpointByID(ctx, id)
		if err != nil || endpoint.AccountID != a.Account.ID {
			return state.ErrNotFound
		}
		if r != nil {
			if p := s.authorizeCallbackEndpoint(r, a.Account, endpoint); p != nil {
				return p
			}
		}
	}
	return nil
}
func executionTaskAccess(r *http.Request, acct state.Account) (runtasks.Access, *api.Problem) {
	a, p := executionAccessForRequest(r)
	return runtasks.Access{Account: acct, Principal: a.principal, Wide: a.broad, ControlWide: a.controlWide}, p
}
func decodeExecutionTaskBody(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("one JSON value required")
	}
	return nil
}
func (s *server) publishExecutionTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, p := executionTaskAccess(r, acct)
	if p != nil {
		writeExecutionAccessError(w, p)
		return
	}
	var request api.PublishExecutionTaskRequest
	if decodeExecutionTaskBody(w, r, &request, api.ExecutionTaskPublishMaxBytes) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid bounded task definition"))
		return
	}
	out, replayed, err := s.executionTaskService(r).Publish(r.Context(), a, request)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *server) listExecutionTasks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, p := executionTaskAccess(r, acct); p != nil {
		writeExecutionAccessError(w, p)
		return
	}
	out, err := s.executionTaskService(r).List(r.Context(), acct.ID, r.URL.Query().Get("cursor"))
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) getExecutionTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, p := executionTaskAccess(r, acct); p != nil {
		writeExecutionAccessError(w, p)
		return
	}
	version, _ := strconv.Atoi(r.PathValue("version"))
	out, err := s.executionTaskService(r).Get(r.Context(), acct.ID, r.PathValue("name"), version)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) retireExecutionTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, p := executionTaskAccess(r, acct)
	if p != nil {
		writeExecutionAccessError(w, p)
		return
	}
	version, _ := strconv.Atoi(r.PathValue("version"))
	if err := s.executionTaskService(r).Retire(r.Context(), a, r.PathValue("name"), version); err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "retired"})
}
func (s *server) invokeExecutionTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, p := executionTaskAccess(r, acct)
	if p != nil {
		writeExecutionAccessError(w, p)
		return
	}
	var request api.InvokeExecutionTaskRequest
	if decodeExecutionTaskBody(w, r, &request, api.ExecutionSealedPayloadMaxBytes) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid task invocation"))
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		api.WriteProblem(w, api.ErrValidation("one Idempotency-Key is required"))
		return
	}
	row, replayed, err := s.executionTaskService(r).Invoke(r.Context(), a, request, keys[0], nil)
	if err != nil {
		s.writeExecutionTaskAdmissionError(w, acct, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(w, http.StatusAccepted, executionResponse(row))
}
func (s *server) writeExecutionTaskAdmissionError(w http.ResponseWriter, acct state.Account, err error) {
	var p *api.Problem
	if errors.As(err, &p) {
		api.WriteProblem(w, p)
		return
	}
	if errors.Is(err, state.ErrExecutionTaskConflict) || errors.Is(err, state.ErrExecutionTaskUnavailable) || errors.Is(err, state.ErrExecutionAdmissionKeyChanged) || errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	s.writeExecutionCreateError(w, acct, err)
}
