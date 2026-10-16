package apidserver

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCachePolicyHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	p, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "cache-http"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "cache-http", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/cache-http/cache-policy"
	for _, query := range []string{"", "?scope=default", "?environment=staging&scope=default", "?environment=staging&environment=staging", "?environment=staging&since=1h", "?environment=staging&bad=%zz"} {
		if r := e.do(t, http.MethodGet, path+query, nil, nil); r.Code != 400 {
			t.Fatalf("selector %s=%d %s", query, r.Code, r.Body)
		}
	}
	r := e.do(t, http.MethodGet, path+"?environment=staging", nil, nil)
	var initial api.EnvironmentCachePolicy
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &initial) != nil || initial.EnvironmentID != env.ID || !initial.Inherited {
		t.Fatalf("get=%d %s", r.Code, r.Body)
	}
	req := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &initial.Revision, Rules: []api.EnvironmentCacheRule{{Path: "/**", Priority: 1, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}}
	first := e.do(t, http.MethodPut, path+"?environment=staging", req, nil)
	if first.Code != 200 {
		t.Fatalf("put=%d %s", first.Code, first.Body)
	}
	retry := e.do(t, http.MethodPut, path+"?environment=staging", req, nil)
	if retry.Code != 200 || retry.Body.String() != first.Body.String() {
		t.Fatalf("replay=%d %s", retry.Code, retry.Body)
	}
	req.RequestID = uuid.NewString()
	if r = e.do(t, http.MethodPut, path+"?environment=staging", req, nil); r.Code != 409 {
		t.Fatalf("stale=%d %s", r.Code, r.Body)
	}
	dest, err := e.store.GetEnvironmentCachePolicy(t.Context(), e.acct.ID, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	copyReq := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: p.ID, EnvironmentID: dest.EnvironmentID, RequestID: uuid.NewString(), ExpectedRevision: &dest.Revision, Rules: req.Rules, CopyFrom: &api.EnvironmentCachePolicySource{Scope: "staging", EnvironmentID: env.ID, Revision: 1}}
	copied := e.do(t, http.MethodPut, path+"?environment=production", copyReq, nil)
	if copied.Code != 200 {
		t.Fatalf("copy=%d %s", copied.Code, copied.Body)
	}
	rev := int64(1)
	restore := req
	restore.ExpectedRevision = &rev
	restore.RequestID = uuid.NewString()
	restore.Inherited = true
	restore.Rules = []api.EnvironmentCacheRule{}
	restored := e.do(t, http.MethodPut, path+"?environment=staging", restore, nil)
	var policy api.EnvironmentCachePolicy
	if restored.Code != 200 || json.Unmarshal(restored.Body.Bytes(), &policy) != nil || !policy.Inherited || policy.Revision != 2 {
		t.Fatalf("restore=%d %s", restored.Code, restored.Body)
	}
	replayed := e.do(t, http.MethodPut, path+"?environment=staging", restore, nil)
	if replayed.Code != 200 || replayed.Body.String() != restored.Body.String() {
		t.Fatalf("restore replay=%d %s", replayed.Code, replayed.Body)
	}
	copyRetry := e.do(t, http.MethodPut, path+"?environment=production", copyReq, nil)
	if copyRetry.Code != 200 || copyRetry.Body.String() != copied.Body.String() {
		t.Fatalf("copy recovery=%d %s", copyRetry.Code, copyRetry.Body)
	}
	copyReq.RequestID = uuid.NewString()
	copyReq.ExpectedRevision = &rev
	if r = e.do(t, http.MethodPut, path+"?environment=production", copyReq, nil); r.Code != 409 {
		t.Fatalf("stale source=%d %s", r.Code, r.Body)
	}
	restore.RequestID = uuid.NewString()
	restore.Rules = req.Rules
	if r = e.do(t, http.MethodPut, path+"?environment=staging", restore, nil); r.Code != 400 {
		t.Fatalf("inherited override=%d %s", r.Code, r.Body)
	}
	for _, query := range []string{"?environment=staging&limit=0", "?environment=staging&limit=51", "?environment=staging&limit=1&limit=2", "?environment=staging&before_revision=-1", "?environment=staging&since=1h"} {
		if r = e.do(t, http.MethodGet, path+"/history"+query, nil, nil); r.Code != 400 {
			t.Fatalf("history selector %s=%d %s", query, r.Code, r.Body)
		}
	}
	r = e.do(t, http.MethodGet, path+"/history?environment=staging&limit=1", nil, nil)
	var history api.EnvironmentCachePolicyHistory
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &history) != nil || len(history.Entries) != 1 || history.Entries[0].Revision != 2 || history.NextBeforeRevision == nil || *history.NextBeforeRevision != 2 {
		t.Fatalf("history=%d %s", r.Code, r.Body)
	}
	r = e.do(t, http.MethodGet, path+"/history/1?environment=staging", nil, nil)
	var historical api.EnvironmentCachePolicy
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &historical) != nil || historical.Inherited || historical.Rules[0].Cache.MaxAgeSeconds != 120 {
		t.Fatalf("historical=%d %s", r.Code, r.Body)
	}
	if r = e.do(t, http.MethodGet, path+"/history/99?environment=staging", nil, nil); r.Code != 404 {
		t.Fatalf("missing history=%d %s", r.Code, r.Body)
	}
	currentRevision, historicalRevision := int64(2), int64(1)
	rollback := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &currentRevision, RestoreRevision: &historicalRevision, Rules: historical.Rules, Inherited: historical.Inherited}
	rolledBack := e.do(t, http.MethodPut, path+"?environment=staging", rollback, nil)
	if rolledBack.Code != 200 || json.Unmarshal(rolledBack.Body.Bytes(), &policy) != nil || policy.Revision != 3 || policy.Inherited {
		t.Fatalf("rollback=%d %s", rolledBack.Code, rolledBack.Body)
	}
	rollbackRetry := e.do(t, http.MethodPut, path+"?environment=staging", rollback, nil)
	if rollbackRetry.Code != 200 || rollbackRetry.Body.String() != rolledBack.Body.String() {
		t.Fatalf("rollback replay=%d %s", rollbackRetry.Code, rollbackRetry.Body)
	}
	rollback.RequestID = uuid.NewString()
	if r = e.do(t, http.MethodPut, path+"?environment=staging", rollback, nil); r.Code != 409 {
		t.Fatalf("stale rollback=%d %s", r.Code, r.Body)
	}
	purgePath := "/v1/apps/cache-http/cache/purges?environment=staging"
	purgeRequest := api.CreateEnvironmentCachePurgeRequest{AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), Path: "/catalog/item"}
	purged := e.do(t, http.MethodPost, purgePath, purgeRequest, nil)
	var purgeReceipt api.EnvironmentCachePurgeReceipt
	if purged.Code != 200 || json.Unmarshal(purged.Body.Bytes(), &purgeReceipt) != nil || purgeReceipt.Generation < 1 || purgeReceipt.Path != purgeRequest.Path {
		t.Fatalf("purge=%d %s", purged.Code, purged.Body)
	}
	purgeRetry := e.do(t, http.MethodPost, purgePath, purgeRequest, nil)
	if purgeRetry.Code != 200 || purgeRetry.Body.String() != purged.Body.String() {
		t.Fatalf("purge replay=%d %s", purgeRetry.Code, purgeRetry.Body)
	}
	r = e.do(t, http.MethodGet, "/v1/apps/cache-http/cache/purges/"+purgeRequest.RequestID+"?environment=staging", nil, nil)
	var purgeStatus api.EnvironmentCachePurgeStatus
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &purgeStatus) != nil || purgeStatus.Status != "degraded" {
		t.Fatalf("purge status=%d %s", r.Code, r.Body)
	}
	purgeRequest.Path = ""
	if r = e.do(t, http.MethodPost, purgePath, purgeRequest, nil); r.Code != 409 {
		t.Fatalf("purge reuse=%d %s", r.Code, r.Body)
	}
	missingPath := map[string]any{"app_id": app.ID, "project_id": p.ID, "environment_id": env.ID, "request_id": uuid.NewString()}
	if r = e.do(t, http.MethodPost, purgePath, missingPath, nil); r.Code != 400 {
		t.Fatalf("missing purge path=%d %s", r.Code, r.Body)
	}

	scheduleID := uuid.NewString()
	scheduleReq := api.CreateEnvironmentCacheScheduleRequest{ScheduleID: scheduleID, AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, ExecuteAt: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond), Purge: &api.CreateEnvironmentCachePurgeRequest{AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, RequestID: scheduleID, Path: ""}}
	schedulePath := "/v1/apps/cache-http/cache/schedules?environment=staging"
	accepted := e.do(t, http.MethodPost, schedulePath, scheduleReq, nil)
	var scheduled api.EnvironmentCacheSchedule
	if accepted.Code != 200 || json.Unmarshal(accepted.Body.Bytes(), &scheduled) != nil || scheduled.Status != "scheduled" || scheduled.Purge == nil || scheduled.Purge.Path != "" {
		t.Fatalf("schedule=%d %s", accepted.Code, accepted.Body)
	}
	scheduleRetry := e.do(t, http.MethodPost, schedulePath, scheduleReq, nil)
	if scheduleRetry.Code != 200 || scheduleRetry.Body.String() != accepted.Body.String() {
		t.Fatalf("schedule replay=%d %s", scheduleRetry.Code, scheduleRetry.Body)
	}
	exactSchedule := "/v1/apps/cache-http/cache/schedules/" + scheduleID + "?environment=staging"
	replacementID := uuid.NewString()
	replacementReq := api.RescheduleEnvironmentCacheRequest{ReplacementID: replacementID, AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, ExpectedExecuteAt: scheduleReq.ExecuteAt, ExecuteAt: scheduleReq.ExecuteAt.Add(time.Hour)}
	replacementPath := "/v1/apps/cache-http/cache/schedules/" + scheduleID + "/reschedule?environment=staging"
	replacement := e.do(t, http.MethodPost, replacementPath, replacementReq, nil)
	var replaced api.EnvironmentCacheSchedule
	if replacement.Code != 200 || json.Unmarshal(replacement.Body.Bytes(), &replaced) != nil || replaced.ReplacesScheduleID != scheduleID || replaced.ScheduleID != replacementID || replaced.Status != "scheduled" {
		t.Fatalf("reschedule=%d %s", replacement.Code, replacement.Body)
	}
	replacementRetry := e.do(t, http.MethodPost, replacementPath, replacementReq, nil)
	if replacementRetry.Code != 200 || replacementRetry.Body.String() != replacement.Body.String() {
		t.Fatalf("reschedule replay=%d %s", replacementRetry.Code, replacementRetry.Body)
	}
	discovered := e.do(t, http.MethodGet, "/v1/apps/cache-http/cache/schedules?environment=staging&status=scheduled&limit=1", nil, nil)
	var schedules api.EnvironmentCacheScheduleList
	if discovered.Code != 200 || json.Unmarshal(discovered.Body.Bytes(), &schedules) != nil || len(schedules.Entries) != 1 || schedules.Entries[0].ScheduleID != replacementID {
		t.Fatalf("discovery=%d %s", discovered.Code, discovered.Body)
	}
	for _, query := range []string{"status=bad", "limit=0", "limit=51", "limit=1&limit=2", "from=invalid", "after=forged"} {
		if r = e.do(t, http.MethodGet, "/v1/apps/cache-http/cache/schedules?environment=staging&"+query, nil, nil); r.Code != 400 {
			t.Fatalf("schedule selector %s=%d %s", query, r.Code, r.Body)
		}
	}

	if r = e.do(t, http.MethodGet, exactSchedule, nil, nil); r.Code != 200 {
		t.Fatalf("schedule get=%d %s", r.Code, r.Body)
	}
	for i := 0; i < 2; i++ {
		if r = e.do(t, http.MethodDelete, exactSchedule, nil, nil); r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &scheduled) != nil || scheduled.Status != "cancelled" {
			t.Fatalf("schedule cancel=%d %s", r.Code, r.Body)
		}
	}

	activityPath := "/v1/apps/cache-http/cache/schedules/activity?environment=staging"
	activityResponse := e.do(t, http.MethodGet, activityPath+"&limit=1", nil, nil)
	var activity api.EnvironmentCacheScheduleActivity
	if activityResponse.Code != 200 || json.Unmarshal(activityResponse.Body.Bytes(), &activity) != nil || len(activity.Entries) != 1 || activity.Entries[0].Kind != "accepted" || activity.Entries[0].Sequence != 1 || !activity.HasMore || activity.Cursor == "" {
		t.Fatalf("activity=%d %s", activityResponse.Code, activityResponse.Body)
	}
	activityResponse = e.do(t, http.MethodGet, activityPath+"&after="+activity.Cursor+"&limit=50", nil, nil)
	if activityResponse.Code != 200 || json.Unmarshal(activityResponse.Body.Bytes(), &activity) != nil || len(activity.Entries) != 2 || activity.Entries[0].Kind != "rescheduled" || activity.Entries[0].RelatedScheduleID != replacementID || activity.Entries[1].RelatedScheduleID != scheduleID || activity.HasMore {
		t.Fatalf("activity continuation=%d %s", activityResponse.Code, activityResponse.Body)
	}
	checkpoint := activity.Cursor
	activityResponse = e.do(t, http.MethodGet, activityPath+"&after="+checkpoint, nil, nil)
	if activityResponse.Code != 200 || json.Unmarshal(activityResponse.Body.Bytes(), &activity) != nil || len(activity.Entries) != 0 || activity.Cursor != checkpoint {
		t.Fatalf("activity tail=%d %s", activityResponse.Code, activityResponse.Body)
	}
	for _, query := range []string{"limit=0", "limit=51", "limit=1&limit=2", "after=forged", "status=failed", "from=2026-10-15T00:00:00Z"} {
		if r = e.do(t, http.MethodGet, activityPath+"&"+query, nil, nil); r.Code != 400 {
			t.Fatalf("activity selector %s=%d %s", query, r.Code, r.Body)
		}
	}

	if err := e.store.PruneEnvironmentCacheScheduleActivity(t.Context(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	expiredResponse := e.do(t, http.MethodGet, activityPath, nil, nil)
	var expiredProblem api.Problem
	if expiredResponse.Code != 410 || json.Unmarshal(expiredResponse.Body.Bytes(), &expiredProblem) != nil || expiredProblem.Code != api.CodeActivityCursorExpired || expiredProblem.ActivityRecovery == nil {
		t.Fatalf("expired activity=%d %s", expiredResponse.Code, expiredResponse.Body)
	}
	recovery := expiredProblem.ActivityRecovery
	if recovery.AppID != app.ID || recovery.EnvironmentID != env.ID || recovery.RestartCursor != checkpoint {
		t.Fatal("wrong recovery scope", recovery)
	}
	recoveredResponse := e.do(t, http.MethodGet, activityPath+"&after="+recovery.RestartCursor, nil, nil)
	if recoveredResponse.Code != 200 || json.Unmarshal(recoveredResponse.Body.Bytes(), &activity) != nil || len(activity.Entries) != 0 {
		t.Fatalf("recovery=%d %s", recoveredResponse.Code, recoveredResponse.Body)
	}

	checkpointPath := "/v1/apps/cache-http/cache/schedules/activity/checkpoint?environment=staging"
	pointResponse := e.do(t, http.MethodGet, checkpointPath, nil, nil)
	var point api.EnvironmentCacheActivityRecovery
	if pointResponse.Code != 200 || json.Unmarshal(pointResponse.Body.Bytes(), &point) != nil || point.RestartCursor != checkpoint || point.OldestCursor != checkpoint || point.EnvironmentID != env.ID {
		t.Fatalf("activity checkpoint=%d %s", pointResponse.Code, pointResponse.Body)
	}
	for _, query := range []string{"&limit=1", "&after=forged", "&environment=staging"} {
		if bad := e.do(t, http.MethodGet, checkpointPath+query, nil, nil); bad.Code != 400 {
			t.Fatalf("checkpoint selector=%d %s", bad.Code, bad.Body)
		}
	}
	rawSchedule := map[string]any{"schedule_id": uuid.NewString(), "app_id": app.ID, "project_id": p.ID, "environment_id": env.ID, "execute_at": scheduleReq.ExecuteAt, "purge": map[string]any{"app_id": app.ID, "project_id": p.ID, "environment_id": env.ID, "request_id": uuid.NewString()}}
	if r = e.do(t, http.MethodPost, schedulePath, rawSchedule, nil); r.Code != 400 {
		t.Fatalf("missing scheduled purge path=%d %s", r.Code, r.Body)
	}
	req.EnvironmentID = uuid.NewString()
	if r = e.do(t, http.MethodPut, path+"?environment=staging", req, nil); r.Code != 409 {
		t.Fatal("wrong immutable identity accepted")
	}
	if r = e.do(t, http.MethodGet, path+"?environment=production", nil, nil); r.Code != 200 || !json.Valid(r.Body.Bytes()) {
		t.Fatalf("neighbor=%d %s", r.Code, r.Body)
	}
}
