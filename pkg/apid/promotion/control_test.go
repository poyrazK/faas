package promotion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type controlStore struct {
	state.Store // teststorecheck:allow implements the optional promotion control store, which statetest.Store would hide
	calls       int
	result      string
	err         error
}

func (s *controlStore) ProjectEnvironmentPromotionByID(_ context.Context, account, project, env, id string) (state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload, error) {
	if account != "account" || project != "shop" || env != "production" || id != "promotion" {
		return state.ProjectEnvironmentPromotion{}, nil, state.ErrNotFound
	}
	return state.ProjectEnvironmentPromotion{}, nil, nil
}
func (s *controlStore) RequestProjectPromotionControl(_ context.Context, account, id, action string, req api.ProjectPromotionControlRequest) (*api.ProjectPromotionControl, error) {
	s.calls++
	return &api.ProjectPromotionControl{Request: req, Action: action, Status: s.result, OperatorAccountID: account}, s.err
}

func TestHandlePromotionControl(t *testing.T) {
	for _, tc := range []struct {
		name, body, action, result, account string
		err                                 error
		status, calls                       int
	}{
		{name: "pending", result: "pending", status: 202, calls: 1},
		{name: "terminal", result: "aborted", status: 200, calls: 1},
		{name: "stale", err: state.ErrConflict, status: 409, calls: 1},
		{name: "unavailable", err: errors.New("offline"), status: 503, calls: 1},
		{name: "foreign", account: "foreign", status: 404},
		{name: "unknown field", body: `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":3,"force":true}`, status: 400},
		{name: "trailing body", body: `{} {}`, status: 400},
		{name: "bad generation", body: `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":0}`, status: 400},
		{name: "invalid action", action: "force", status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if body == "" {
				body = `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":3}`
			}
			action := tc.action
			if action == "" {
				action = "abort"
			}
			account := tc.account
			if account == "" {
				account = "account"
			}
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			for key, value := range map[string]string{"slug": "shop", "environment": "production", "promotion": "promotion", "action": action} {
				req.SetPathValue(key, value)
			}
			store := &controlStore{result: tc.result, err: tc.err}
			recorder := httptest.NewRecorder()
			HandleControl(recorder, req, state.Account{ID: account}, store, nil)
			if recorder.Code != tc.status || store.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, store.calls, recorder.Body.String())
			}
			if recorder.Code == 200 || recorder.Code == 202 {
				var out api.ProjectPromotionControl
				if json.Unmarshal(recorder.Body.Bytes(), &out) != nil || out.Request.ExpectedGeneration != 3 {
					t.Fatal("lost receipt")
				}
			}
		})
	}
}

func TestBaselineProbeInputsExcludeWorker(t *testing.T) {
	policy := api.ProjectPromotionHealthPolicy{Worker: &api.ProjectReleaseWorker{AppID: "worker"}}
	graph := state.ProjectReleaseSet{Members: []state.ProjectReleaseMember{{AppID: "api", DeploymentID: "http"}, {AppID: "worker", DeploymentID: "jobs"}}}
	receipt := BaselineProbeInputs(policy, graph)
	if len(receipt.Apply.Builds) != 1 || receipt.Apply.Builds[0].AppID != "api" {
		t.Fatal("worker became HTTP probe", receipt.Apply.Builds)
	}
}

type healthControlStore struct {
	state.Store // teststorecheck:allow implements the optional promotion health stores, which statetest.Store would hide
	state.ProjectReleaseSetReader
	p            state.ProjectEnvironmentPromotion
	h            *api.ProjectPromotionHealth
	baseline     state.ProjectReleaseSet
	observations []state.PromotionHealthObservation
}

func (s *healthControlStore) ProjectEnvironmentPromotionByID(context.Context, string, string, string, string) (state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload, error) {
	return s.p, nil, nil
}
func (s *healthControlStore) ClaimProjectEnvironmentPromotionHealth(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (s *healthControlStore) ReleaseProjectEnvironmentPromotionHealth(context.Context, string, string) error {
	return nil
}
func (s *healthControlStore) ProjectEnvironmentPromotionHealth(context.Context, string, string) (*api.ProjectPromotionHealth, error) {
	return s.h, nil
}
func (s *healthControlStore) PendingProjectEnvironmentPromotionHealth(context.Context) ([]state.PromotionHealthJob, error) {
	return nil, nil
}
func (s *healthControlStore) AdvanceProjectEnvironmentPromotionHealth(_ context.Context, _ string, _ string, o state.PromotionHealthObservation) (*api.ProjectPromotionHealth, error) {
	s.observations = append(s.observations, o)
	return s.h, nil
}
func (s *healthControlStore) ProjectReleaseSetByID(context.Context, string, string, string, string) (state.ProjectReleaseSet, error) {
	return s.baseline, nil
}

func TestReconcilePromotionControl(t *testing.T) {
	for _, scenario := range []string{"baseline_probe", "probe_failure", "unconfigured", "deadline", "interval", "expired_retention", "published_restart"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			deadline := now.Add(time.Minute)
			store := &healthControlStore{p: state.ProjectEnvironmentPromotion{ID: "promotion", AccountID: "account", ProjectID: "project", ProjectSlug: "shop", ToEnvironment: "production", Status: "running", PreviousTargetReleaseSetID: "baseline", TargetReleaseSetID: "candidate"}, h: &api.ProjectPromotionHealth{Generation: 77, Phase: "restoring", Deadline: deadline, Control: &api.ProjectPromotionControl{Status: "pending", Deadline: deadline}}, baseline: state.ProjectReleaseSet{ID: "baseline", ExpiresAt: &deadline}}
			probeCalls := 0
			a := HealthActions{Now: func() time.Time { return now }, RecoveryProbe: func(_ context.Context, p state.ProjectEnvironmentPromotion, policy api.ProjectPromotionHealthPolicy, graph state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error) {
				probeCalls++
				if graph.ID != "baseline" || p.ID != "promotion" {
					t.Fatal("wrong restoration target")
				}
				if scenario == "probe_failure" {
					return nil, errors.New("unavailable")
				}
				return []api.ProjectReleaseProbeEvidence{{StableRequests: 1}}, nil
			}}
			switch scenario {
			case "unconfigured":
				a.RecoveryProbe = nil
			case "deadline":
				store.h.Control.Deadline = now
			case "interval":
				checked := now.Add(-time.Second)
				store.h.CheckedAt = &checked
			case "expired_retention":
				store.baseline.ExpiresAt = &now
			case "published_restart":
				store.h.Phase = "rolling_back"
				store.h.Control.Deadline = now
				store.p.RestoredTargetReleaseSetID = "restored"
				store.p.RollbackStatus = "rolled_back"
			}
			if err := ReconcileHealth(t.Context(), store, state.PromotionHealthJob{AccountID: "account", PromotionID: "promotion", ProjectSlug: "shop", ToEnvironment: "production"}, a); err != nil {
				t.Fatal(err)
			}
			if scenario == "interval" {
				if len(store.observations) != 0 || probeCalls != 0 {
					t.Fatal("interval bypassed")
				}
				return
			}
			if len(store.observations) != 1 || store.observations[0].Generation != 77 {
				t.Fatal("lost generation", store.observations)
			}
			o := store.observations[0]
			switch scenario {
			case "baseline_probe":
				if probeCalls != 1 || len(o.Evidence) != 1 || o.Action != "" {
					t.Fatal(o)
				}
			case "probe_failure":
				if probeCalls != 1 || len(o.Evidence) != 0 || o.Action != "" {
					t.Fatal(o)
				}
			case "published_restart":
				if o.Action != "rolled_back" {
					t.Fatal("published restoration stranded", o)
				}
			default:
				if o.Action != "rollback_failed" || probeCalls != 0 {
					t.Fatal(o)
				}
			}
		})
	}
}
