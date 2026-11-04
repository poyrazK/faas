// adr: 20261103222927
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAgentWorkflowNativeResourceInventoryNeedsOnlyAppRead(t *testing.T) {
	base := newLocalFixture(t, true, "release-base")
	var global, writes atomic.Int64
	var mode atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		if r.URL.Path == "/v1/domains" || r.URL.Path == "/v1/postgres/databases" {
			global.Add(1)
			w.WriteHeader(403)
			return
		}
		if r.URL.Path != "/v1/apps/alpha/resource-inventory" {
			base.serveHTTP(w, r)
			return
		}
		if mode.Load() == 1 {
			w.WriteHeader(404)
			return
		}
		kind := r.URL.Query().Get("kind")
		row := api.AppResourceInventoryItem{ID: "resource-own", AppID: base.app.ID, Scope: "default"}
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		switch kind {
		case "domains":
			v := false
			row.Domain = row.ID
			row.Verified = &v
			row.CertStatus = "pending"
		case "crons":
			v := true
			row.Enabled = &v
			row.Schedule = "*/5 * * * *"
			row.Timezone = "UTC"
		case "buckets":
			row.Name = "assets"
			row.Region = "test"
			row.State = "ready"
		case "storage_bindings":
			v := false
			row.BucketID = "bucket-own"
			row.Bucket = "assets"
			row.RotationPending = &v
		case "databases":
			row.Name = "primary"
			row.Region = "test"
			row.State = "ready"
			row.PostgresMajor = 17
			row.Binding = &api.AppResourceInventoryBinding{ID: "binding-own", DatabaseID: row.ID, AppID: base.app.ID, Scope: "default", State: "ready", Access: "read_only", EnvironmentKey: "DATABASE_URL"}
		}
		if mode.Load() == 2 {
			row.AppID = "another-app"
		}
		out := api.AppResourceInventoryResponse{SchemaVersion: 1, AppID: base.app.ID, Scope: "default", Kind: kind, Limit: limit, Offset: off, Total: 1, Items: []api.AppResourceInventoryItem{row}, GeneratedAt: time.Now().UTC()}
		if off > 0 {
			out.Items = []api.AppResourceInventoryItem{}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer backend.Close()
	agent := workflowAgent(t, backend, Options{APIBase: backend.URL, Token: testToken, agentKey: true, Capabilities: []string{api.ScopeAppsRead}, AllowedApps: map[string]string{"alpha": base.app.ID}, Toolsets: []string{ToolsetProvision}})
	for _, name := range []string{"gregale_list_domains", "gregale_list_crons", "gregale_list_buckets", "gregale_get_storage_bindings", "gregale_list_databases"} {
		out := agent.call(name, map[string]any{"app": "alpha", "limit": 1}, false)
		var pagination struct {
			HasMore bool `json:"has_more"`
		}
		if err := json.Unmarshal(out["pagination"], &pagination); err != nil || pagination.HasMore {
			t.Fatalf("%s: missing end-of-list evidence", name)
		}
	}
	for _, bad := range []int32{1, 2} {
		mode.Store(bad)
		agent.call("gregale_list_databases", map[string]any{"app": "alpha"}, true)
	}
	if global.Load() != 0 || writes.Load() != 0 {
		t.Fatalf("metadata broadened reads or wrote: global=%d writes=%d", global.Load(), writes.Load())
	}
}
