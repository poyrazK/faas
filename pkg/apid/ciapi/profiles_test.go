// ADR-1655: profile setup, isolation and recovery use the internal CI boundary.
package ciapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCIProfileAPISetupIsolationAndDisabledRecovery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := ci.NewPostgresStore(pool)
	account, other, job := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{account, other} {
		if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan) VALUES ($1,$2,'pro')", id, id+"@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	if _, err := pool.Exec(t.Context(), `INSERT INTO jobs(id,account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,retry_max,env_overrides,command,image_materialization_status,image_resolved_digest,image_storage_key)
 VALUES ($1,$2,'batch','profile-test',$3,$4,600,1,0,'{}',$5,'ready',$6,'private/profile-rootfs.ext4')`, job, account, image, api.CIJobsRAMMB, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, strings.Split(image, "@")[1]); err != nil {
		t.Fatal(err)
	}
	revision, err := store.PutRevision(t.Context(), account, "profile", ci.Definition{Version: 1, Repository: "gregale/profiles", Jobs: map[string]ci.Job{"test": {Image: image, Steps: []ci.Step{{Name: "unit", Run: "synthetic, never executed"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.PutGitHubBinding(t.Context(), account, "profile", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ci.ConfigFromEnv(func(key string) string {
		switch key {
		case ci.AccountsEnv:
			return account + "," + other
		case ci.InternalEnv, ci.GitHubEnv:
			return "1"
		}
		return ""
	})
	h := ciapi.Handler{Config: cfg, Store: store, VerifyGitHub: func(context.Context, string, int64, int64, string) error { return nil }}
	call := func(action ciapi.Action, owner, name, id string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
		r.SetPathValue("name", name)
		r.SetPathValue("id", id)
		r.SetPathValue("binding", id)
		r.Header.Set("Idempotency-Key", "profile-api-execution")
		w := httptest.NewRecorder()
		action(h, w, r, state.Account{ID: owner})
		return w
	}
	req := ci.ExecutionProfileRequest{RevisionID: revision.ID, BindingID: binding.ID, Templates: map[string]string{"test": job}}
	h.Config.GitHubEnabled = false
	if w := call(ciapi.Handler.PutExecutionProfile, account, "checks", "", req); w.Code != http.StatusNotFound {
		t.Fatal("disabled GitHub gate allowed profile setup")
	}
	h.Config = cfg
	w := call(ciapi.Handler.PutExecutionProfile, account, "checks", "", req)
	if w.Code != http.StatusOK {
		t.Fatalf("profile setup with compute disabled: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "private/") || strings.Contains(w.Body.String(), "installation_id") || strings.Contains(w.Body.String(), "synthetic, never executed") {
		t.Fatal("profile response exposed private provenance or commands")
	}
	var profile ci.ExecutionProfile
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	// ADR-1656: policy setup is dormant while every compute/automation gate is off.
	autoReq := ci.AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}}
	if w := call(ciapi.Handler.PutAutomationPolicy, account, "", binding.ID, autoReq); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private/") {
		t.Fatalf("dormant automation setup: %d %s", w.Code, w.Body)
	}
	if w := call(ciapi.Handler.PutAutomationPolicy, other, "", binding.ID, autoReq); w.Code != http.StatusNotFound {
		t.Fatal("foreign account configured automation")
	}
	if w := call(ciapi.Handler.ListAutomationPolicies, other, "", "", nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), profile.ID) {
		t.Fatal("automation policy listing crossed account boundary")
	}
	if w := call(ciapi.Handler.DisableAutomationPolicy, other, "", binding.ID, nil); w.Code != http.StatusNotFound {
		t.Fatal("foreign account disabled automation")
	}
	if w := call(ciapi.Handler.ListAutomationEvents, account, "", binding.ID, nil); w.Code != http.StatusOK {
		t.Fatal("automation event history unavailable")
	}
	if w := call(ciapi.Handler.PutExecutionProfile, other, "foreign", "", req); w.Code != http.StatusNotFound {
		t.Fatal("foreign binding reached profile setup")
	}
	if w := call(ciapi.Handler.ExecutionProfile, other, "", profile.ID, nil); w.Code != http.StatusNotFound {
		t.Fatal("profile leaked to foreign account")
	}
	if w := call(ciapi.Handler.DisableExecutionProfile, other, "", profile.ID, nil); w.Code != http.StatusNotFound {
		t.Fatal("foreign account disabled profile")
	}
	if w := call(ciapi.Handler.ListExecutionProfiles, other, "", "", nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), profile.ID) {
		t.Fatal("profile listing crossed account boundary")
	}
	submit := ci.JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: ci.SubmitRequest{CommitSHA: strings.Repeat("a", 40)}}
	if w := call(ciapi.Handler.SubmitExecution, account, "", "", submit); w.Code != http.StatusNotFound {
		t.Fatal("profile bypassed compute admission gates")
	}
	h.Config.DispatchEnabled, h.Config.WorkerEnabled, h.Config.TransportEnabled, h.ExecutionReady = true, true, true, true
	forged := map[string]any{"profile_id": profile.ID, "commit_sha": submit.CommitSHA, "automation": map[string]string{"event_id": uuid.NewString()}}
	if w := call(ciapi.Handler.SubmitExecution, account, "", "", forged); w.Code != http.StatusBadRequest {
		t.Fatal("client forged internal automation admission authority")
	}
	delete(forged, "automation")
	forged["capture_checks"] = true
	if w := call(ciapi.Handler.SubmitExecution, account, "", "", forged); w.Code != http.StatusBadRequest {
		t.Fatal("client forged internal Check publication opt-in")
	}
	if w := call(ciapi.Handler.SubmitExecution, account, "", "", submit); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), profile.ID) {
		t.Fatalf("profile submission: %d %s", w.Code, w.Body)
	}
	invalid := submit
	invalid.BindingID = binding.ID
	if w := call(ciapi.Handler.SubmitExecution, account, "", "", invalid); w.Code != http.StatusBadRequest {
		t.Fatal("profile submission accepted explicit overrides")
	}
	h.Config.GitHubEnabled, h.Config.DispatchEnabled, h.Config.WorkerEnabled, h.Config.TransportEnabled = false, false, false, false
	if w := call(ciapi.Handler.ListAutomationPolicies, account, "", "", nil); w.Code != http.StatusOK {
		t.Fatal("automation recovery read requires new admission")
	}
	if w := call(ciapi.Handler.DisableAutomationPolicy, account, "", binding.ID, nil); w.Code != http.StatusOK {
		t.Fatal("automation disable requires new admission")
	}
	if w := call(ciapi.Handler.ExecutionProfile, account, "", profile.ID, nil); w.Code != http.StatusOK {
		t.Fatal("profile recovery read requires new admission")
	}
	if w := call(ciapi.Handler.DisableExecutionProfile, account, "", profile.ID, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"disabled"`) {
		t.Fatal("profile disable requires new admission")
	}
	h.Config.Enabled = false
	if w := call(ciapi.Handler.ListExecutionProfiles, account, "", "", nil); w.Code != http.StatusNotFound {
		t.Fatal("disabled internal gate leaked profiles")
	}
}
