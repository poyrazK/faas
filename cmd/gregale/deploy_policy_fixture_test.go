package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// Adapt older command fixtures to the staging endpoint. Production locking and
// revision semantics are verified independently against both real stores.
func withFixturePolicyStaging(handler http.HandlerFunc) http.HandlerFunc {
	var mu sync.Mutex
	type metadata struct {
		revision           int64
		owner, fingerprint string
		nulls              map[string]bool
	}
	states := map[string]*metadata{}
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/deployment-staging") {
			handler(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		path := strings.TrimSuffix(r.URL.Path, "/deployment-staging")
		m := states[path]
		if m == nil {
			m = &metadata{nulls: map[string]bool{}}
			states[path] = m
		}
		get := httptest.NewRequest("GET", path, nil)
		get.Header = r.Header.Clone()
		appResponse := httptest.NewRecorder()
		handler(appResponse, get)
		if appResponse.Code != 200 {
			w.WriteHeader(appResponse.Code)
			_, _ = w.Write(appResponse.Body.Bytes())
			return
		}
		var app api.AppResponse
		_ = json.Unmarshal(appResponse.Body.Bytes(), &app)
		profile, _ := json.Marshal(app.Manifest.Profiling)
		trace, _ := json.Marshal(app.Manifest.Tracing)
		values := map[string]json.RawMessage{"profiling": profile, "tracing": trace}
		for field, nullable := range m.nulls {
			if nullable && (string(values[field]) == `{"enabled":false}` || string(values[field]) == "null") {
				values[field] = json.RawMessage("null")
			}
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(api.DeploymentStagingSnapshot{AppID: app.ID, Revision: m.revision, Settings: values})
			return
		}
		var req api.DeploymentStagingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		fingerprint, _ := req.Fingerprint()
		matches := func(wanted map[string]json.RawMessage) bool {
			for field, value := range wanted {
				if !api.PolicySettingEqual(field, values[field], value) {
					return false
				}
			}
			return true
		}
		conflict := func() {
			api.WriteProblem(w, api.NewProblem(409, api.CodeDeploymentStagingConflict, "Policy changed", "fixture conflict"))
		}
		var target map[string]json.RawMessage
		status := "staged"
		if req.ExpectedAppID != app.ID {
			conflict()
			return
		}
		if req.Operation == "stage" {
			if m.owner == req.SessionID && m.fingerprint == fingerprint && matches(req.Desired) {
				_ = json.NewEncoder(w).Encode(api.DeploymentStagingReceipt{SessionID: req.SessionID, AppID: app.ID, Status: status, Revision: m.revision})
				return
			}
			if m.revision != req.ExpectedRevision || !matches(req.Before) {
				conflict()
				return
			}
			m.owner, m.fingerprint = req.SessionID, fingerprint
			target = req.Desired
		} else {
			status = "cleaned"
			if m.revision != req.ExpectedRevision && matches(req.Before) {
				_ = json.NewEncoder(w).Encode(api.DeploymentStagingReceipt{SessionID: req.SessionID, AppID: app.ID, Status: status, Revision: m.revision})
				return
			}
			if m.revision == req.ExpectedRevision && matches(req.Before) {
				m.revision++
				_ = json.NewEncoder(w).Encode(api.DeploymentStagingReceipt{SessionID: req.SessionID, AppID: app.ID, Status: status, Revision: m.revision})
				return
			}
			if m.owner != req.SessionID || m.fingerprint != fingerprint || !matches(req.Desired) {
				conflict()
				return
			}
			m.owner, m.fingerprint = "", ""
			target = req.Before
		}
		for field, value := range target {
			// Existing fixture PATCH handlers express disabled defaults as a
			// zero config instead of an absent config; retain null identity here.
			m.nulls[field] = string(value) == "null"
			if m.nulls[field] {
				value = json.RawMessage(`{"enabled":false}`)
			}
			data, _ := json.Marshal(map[string]json.RawMessage{field: value})
			patch := httptest.NewRequest("PATCH", path, strings.NewReader(string(data)))
			patch.Header = r.Header.Clone()
			reply := httptest.NewRecorder()
			handler(reply, patch)
			if reply.Code >= 300 {
				w.WriteHeader(reply.Code)
				_, _ = w.Write(reply.Body.Bytes())
				return
			}
		}
		m.revision++
		_ = json.NewEncoder(w).Encode(api.DeploymentStagingReceipt{SessionID: req.SessionID, AppID: app.ID, Status: status, Revision: m.revision})
	}
}
