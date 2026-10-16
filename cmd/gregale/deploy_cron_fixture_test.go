package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Older CLI fixtures expose individual cron CRUD. Adapt those fixture routes
// to the new protocol; production atomicity is tested against both stores.
func withFixtureCronStaging(handler http.HandlerFunc) http.HandlerFunc {
	var mu sync.Mutex
	apps := map[string]string{}
	type metadata struct {
		revision      int64
		owner, digest string
		current       []api.DeploymentCronConfig
		aliases       map[string]string
	}
	states := map[string]*metadata{}
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/deployment-cron-staging") {
			if r.URL.Path == "/v1/apps" && r.Method == "POST" || strings.Count(r.URL.Path, "/") == 3 && strings.HasPrefix(r.URL.Path, "/v1/apps/") {
				recorder := httptest.NewRecorder()
				handler(recorder, r)
				var app api.AppResponse
				_ = json.Unmarshal(recorder.Body.Bytes(), &app)
				if app.ID != "" {
					slug := app.Slug
					if slug == "" {
						slug = strings.TrimPrefix(r.URL.Path, "/v1/apps/")
					}
					mu.Lock()
					apps[slug] = app.ID
					mu.Unlock()
				}
				for key, values := range recorder.Header() {
					w.Header()[key] = values
				}
				w.WriteHeader(recorder.Code)
				_, _ = w.Write(recorder.Body.Bytes())
				return
			}
			handler(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/deployment-cron-staging")
		dispatch := func(ctx context.Context, method, path string, body any) *httptest.ResponseRecorder {
			var data bytes.Buffer
			if body != nil {
				_ = json.NewEncoder(&data).Encode(body)
			}
			req := httptest.NewRequestWithContext(ctx, method, path, &data)
			req.Header = r.Header.Clone()
			recorder := httptest.NewRecorder()
			handler(recorder, req)
			return recorder
		}
		forwardError := func(recorder *httptest.ResponseRecorder) {
			w.WriteHeader(recorder.Code)
			_, _ = w.Write(recorder.Body.Bytes())
		}
		m := states[slug]
		if m == nil {
			m = &metadata{current: []api.DeploymentCronConfig{}, aliases: map[string]string{}}
			rec := dispatch(r.Context(), "GET", "/v1/crons?slug="+slug, nil)
			if rec.Code >= 300 {
				forwardError(rec)
				return
			}
			var rows []api.CronResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &rows)
			for _, c := range rows {
				if c.Kind == "command" {
					continue
				}
				id := uuid.NewSHA1(uuid.Nil, []byte(c.ID)).String()
				tz := c.Timezone
				if tz == "" {
					tz = "UTC"
				}
				m.aliases[id] = c.ID
				m.current = append(m.current, api.DeploymentCronConfig{ID: id, Schedule: c.Schedule, Path: c.Path, Enabled: c.Enabled, Timezone: tz, SkipIfRunning: c.SkipIfRunning})
			}
			_ = dispatch(r.Context(), "GET", "/v1/account", nil)
			states[slug] = m
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(api.DeploymentCronStagingSnapshot{AppID: apps[slug], Revision: m.revision, Crons: m.current})
			return
		}
		var req api.DeploymentCronStagingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		target := req.Desired
		status := "staged"
		if req.Operation == "restore" {
			target = req.Before
			status = "cleaned"
		}
		if req.Operation == "prepare" || req.Operation == "cancel" || req.Operation == "track_upload" {
			status := "prepared"
			if req.Operation == "cancel" {
				status = "cleaned"
			}
			if m.revision == req.ExpectedRevision {
				m.revision++
				m.owner = req.SessionID
				m.digest = req.Fingerprint()
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentStagingReceipt{SessionID: req.SessionID, AppID: req.ExpectedAppID, Revision: req.ExpectedRevision + 1, Status: status})
			return
		}
		digest := req.Fingerprint()
		reply := func() {
			_ = json.NewEncoder(w).Encode(api.DeploymentStagingReceipt{SessionID: req.SessionID, AppID: req.ExpectedAppID, Revision: m.revision, Status: status})
		}
		if req.Operation == "stage" && m.owner == req.SessionID && m.digest == digest {
			reply()
			return
		}
		if req.Operation == "restore" && m.revision != req.ExpectedRevision && api.DeploymentCronsEqual(m.current, req.Before) {
			reply()
			return
		}
		if req.Operation == "restore" && m.revision == req.ExpectedRevision && api.DeploymentCronsEqual(m.current, req.Before) {
			m.revision++
			reply()
			return
		}
		if req.Operation == "stage" && m.revision != req.ExpectedRevision || req.Operation == "restore" && (m.owner != req.SessionID || m.digest != digest) {
			api.WriteProblem(w, api.NewProblem(409, api.CodeDeploymentCronConflict, "Conflict", "fixture cron ownership changed"))
			return
		}
		desired := map[string]api.DeploymentCronConfig{}
		for _, c := range target {
			desired[c.ID] = c
		}
		current := map[string]api.DeploymentCronConfig{}
		for _, c := range m.current {
			current[c.ID] = c
			if _, keep := desired[c.ID]; !keep {
				rec := dispatch(r.Context(), "DELETE", "/v1/crons/"+m.aliases[c.ID], nil)
				if rec.Code >= 300 {
					forwardError(rec)
					return
				}
			}
		}
		for _, c := range target {
			if previous, exists := current[c.ID]; exists {
				if previous != c {
					rec := dispatch(r.Context(), "PATCH", "/v1/crons/"+m.aliases[c.ID], api.UpdateCronRequest{Schedule: &c.Schedule, Path: &c.Path, Enabled: &c.Enabled, Timezone: &c.Timezone, SkipIfRunning: &c.SkipIfRunning})
					if rec.Code >= 300 {
						forwardError(rec)
						return
					}
				}
			} else {
				rec := dispatch(r.Context(), "POST", "/v1/crons", api.CreateCronRequest{AppID: slug, Schedule: c.Schedule, Path: c.Path, Enabled: &c.Enabled, Timezone: c.Timezone, SkipIfRunning: &c.SkipIfRunning})
				if rec.Code >= 300 {
					forwardError(rec)
					return
				}
				var created api.CronResponse
				_ = json.Unmarshal(rec.Body.Bytes(), &created)
				m.aliases[c.ID] = created.ID
			}
		}
		m.current = append([]api.DeploymentCronConfig{}, target...)
		m.revision++
		if req.Operation == "stage" {
			m.owner, m.digest = req.SessionID, digest
		} else {
			m.owner, m.digest = "", ""
		}
		reply()
	}
}
