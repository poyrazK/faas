package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// rollbackFixtureService is one service's production release and rollback
// target in projectRollbackAPI.
type rollbackFixtureService struct {
	revision, target int // target 0: no earlier release
	createdAt        time.Time
	commit           string
}

// projectRollbackAPI serves a three-service project (web → api → billing)
// with the given releases and records the order of per-app rollbacks;
// failSlug answers 409.
func projectRollbackAPI(t *testing.T, failSlug string, services map[string]rollbackFixtureService) (*[]string, *[]map[string]any) {
	t.Helper()
	var mu sync.Mutex
	calls, bodies := &[]string{}, &[]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && path == "/v1/projects/shop":
			_, _ = w.Write([]byte(`{"slug":"shop","workloads":[
				{"slug":"shop-web","workload_name":"web","status":"active","depends_on":["api"]},
				{"slug":"shop-api","workload_name":"api","status":"active","depends_on":["billing"]},
				{"slug":"shop-billing","workload_name":"billing","status":"active"}],"exclusions":[]}`))
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/deployments"):
			slug := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/apps/"), "/deployments")
			svc := services[slug]
			items := []map[string]any{{"id": "cur-" + slug, "revision": svc.revision, "status": "live", "traffic_percent": 100,
				"scope": "default", "created_at": svc.createdAt.Format(time.RFC3339Nano), "commit_sha": svc.commit}}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/summary"):
			slug := strings.Split(strings.TrimPrefix(path, "/v1/apps/"), "/")[0]
			out := map[string]any{"deployment": map[string]any{"id": "cur-" + slug}, "changes": []any{}}
			if svc := services[slug]; svc.target > 0 {
				out["rollback_target_id"], out["rollback_target_revision"] = "tgt-"+slug, svc.target
			}
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/rollback"):
			slug := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/apps/"), "/rollback")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			*calls, *bodies = append(*calls, slug), append(*bodies, body)
			mu.Unlock()
			if slug == failSlug {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"type":"about:blank","title":"Conflict","status":409,"code":"conflict","detail":"rollback refused"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"tgt-` + slug + `","status":"live"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	return calls, bodies
}

// oneDeploy is a project whose three services the latest deploy changed.
func oneDeploy() map[string]rollbackFixtureService {
	now := time.Now().UTC()
	return map[string]rollbackFixtureService{
		"shop-web":     {revision: 4, target: 3, createdAt: now, commit: "c2"},
		"shop-api":     {revision: 7, target: 6, createdAt: now.Add(-time.Second), commit: "c2"},
		"shop-billing": {revision: 2, target: 1, createdAt: now.Add(-2 * time.Second), commit: "c2"},
	}
}

func runProjectRollback(t *testing.T, args ...string) (int, string) {
	t.Helper()
	out, restore := main.CaptureStdout(t)
	code := main.Run(append([]string{"projects", "rollback", "shop"}, args...))
	restore()
	return code, out.String()
}

// Callers roll back before the services they call, each pinned to the
// planned current and target release.
func TestProjectRollbackRollsCallersBackFirst(t *testing.T) {
	calls, bodies := projectRollbackAPI(t, "", oneDeploy())
	code, out := runProjectRollback(t, "--yes")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := strings.Join(*calls, ","); got != "shop-web,shop-api,shop-billing" {
		t.Fatalf("rollback order = %s, want web before api before billing", got)
	}
	if body := (*bodies)[0]; body["target_deployment_id"] != "tgt-shop-web" || body["expected_current_deployment_id"] != "cur-shop-web" {
		t.Fatalf("rollback not pinned to the plan: %v", body)
	}
	if !strings.Contains(out, "Project shop rolled back: 3 services") {
		t.Fatalf("output:\n%s", out)
	}
}

// A service the latest deploy did not change keeps its release unless --all.
func TestProjectRollbackLeavesServicesTheLatestDeployDidNotChange(t *testing.T) {
	services := oneDeploy()
	billing := services["shop-billing"]
	billing.createdAt, billing.commit = billing.createdAt.Add(-30*24*time.Hour), "c1"
	services["shop-billing"] = billing
	calls, _ := projectRollbackAPI(t, "", services)
	code, out := runProjectRollback(t, "--yes")
	if code != 0 || strings.Join(*calls, ",") != "shop-web,shop-api" {
		t.Fatalf("exit %d, rollbacks %v:\n%s", code, *calls, out)
	}
	if !strings.Contains(out, "not changed by the latest deploy") {
		t.Fatalf("no skip reason for billing:\n%s", out)
	}
	*calls = nil
	if code, out := runProjectRollback(t, "--yes", "--all"); code != 0 || strings.Join(*calls, ",") != "shop-web,shop-api,shop-billing" {
		t.Fatalf("--all: exit %d, rollbacks %v:\n%s", code, *calls, out)
	}
}

// A service added since the previous deploy has nothing to roll back to and
// does not stop the others.
func TestProjectRollbackSkipsServiceAddedSince(t *testing.T) {
	services := oneDeploy()
	billing := services["shop-billing"]
	billing.target = 0
	services["shop-billing"] = billing
	calls, _ := projectRollbackAPI(t, "", services)
	code, out := runProjectRollback(t, "--yes")
	if code != 0 || strings.Join(*calls, ",") != "shop-web,shop-api" || !strings.Contains(out, "no earlier release") {
		t.Fatalf("exit %d, rollbacks %v:\n%s", code, *calls, out)
	}
}

// A rerun after a partial rollback finishes it instead of rolling the
// already rolled-back services forward again.
func TestProjectRollbackRerunSkipsServicesAlreadyRolledBack(t *testing.T) {
	services := oneDeploy()
	web := services["shop-web"]
	web.revision, web.target = 3, 4 // web serves v3 again; its "rollback" target is the newer v4
	services["shop-web"] = web
	calls, _ := projectRollbackAPI(t, "", services)
	code, out := runProjectRollback(t, "--yes")
	if code != 0 || strings.Join(*calls, ",") != "shop-api,shop-billing" || !strings.Contains(out, "already rolled back") {
		t.Fatalf("exit %d, rollbacks %v:\n%s", code, *calls, out)
	}
}

func TestProjectRollbackDryRunChangesNothing(t *testing.T) {
	calls, _ := projectRollbackAPI(t, "", oneDeploy())
	code, out := runProjectRollback(t, "--dry-run")
	if code != 0 || len(*calls) != 0 {
		t.Fatalf("exit %d, rollbacks %v:\n%s", code, *calls, out)
	}
	if !strings.Contains(out, "1. web (shop-web): v4 -> v3 (before api)") || !strings.Contains(out, "3. billing (shop-billing): v2 -> v1") {
		t.Fatalf("plan:\n%s", out)
	}
}

func TestProjectRollbackStopsAtTheFirstFailure(t *testing.T) {
	calls, _ := projectRollbackAPI(t, "shop-api", oneDeploy())
	code, _ := runProjectRollback(t, "--yes")
	if code == 0 {
		t.Fatal("exit 0 after a failed service rollback")
	}
	if got := strings.Join(*calls, ","); got != "shop-web,shop-api" {
		t.Fatalf("rollbacks = %s, want billing left alone after api failed", got)
	}
}

func TestProjectRollbackRequiresDryRunOrYes(t *testing.T) {
	calls, _ := projectRollbackAPI(t, "", oneDeploy())
	if code := main.Run([]string{"projects", "rollback", "shop"}); code == 0 || len(*calls) != 0 {
		t.Fatalf("exit %d, rollbacks %v; want a usage error and no change", code, *calls)
	}
}
