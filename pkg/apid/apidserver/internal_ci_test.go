package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/ci"
)

func internalCIConfig(account string, enabled bool) ci.Config {
	return ci.ConfigFromEnv(func(key string) string {
		if key == ci.InternalEnv && enabled {
			return "1"
		}
		if key == ci.AccountsEnv {
			return account
		}
		return ""
	})
}

func TestInternalCIAdmissionIsMetadataOnly(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	const base = "/v1/internal/ci/"
	if got := e.Do(t, http.MethodGet, base+"capabilities", nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("CI enabled by default: %d", got.Code)
	}
	(*e.S()).WithInternalCIConfig(internalCIConfig("", true))
	if got := e.Do(t, http.MethodPost, base+"runs", map[string]any{}, nil); got.Code != http.StatusNotFound {
		t.Fatalf("missing allowlist accepted: %d", got.Code)
	}
	(*e.S()).WithInternalCIConfig(internalCIConfig(e.Acct().ID, true))
	capabilities := e.Do(t, http.MethodGet, base+"capabilities", nil, nil)
	if capabilities.Code != http.StatusOK || !strings.Contains(capabilities.Body.String(), `"compute_available":false`) {
		t.Fatalf("unexpected capabilities: %d %s", capabilities.Code, capabilities.Body)
	}
	definition := "version: 1\nrepository: acme/api\njobs:\n  test:\n    image: registry.example/ci@sha256:" + strings.Repeat("a", 64) + "\n    steps:\n      - name: test\n        run: npm test\n"
	put := e.Do(t, http.MethodPut, base+"pipelines/checks", map[string]string{"definition": definition}, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("put: %d %s", put.Code, put.Body)
	}
	var revision ci.Revision
	if err := json.Unmarshal(put.Body.Bytes(), &revision); err != nil {
		t.Fatal(err)
	}
	body := ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("b", 40)}
	headers := map[string]string{"Idempotency-Key": "internal-ci-test"}
	created := e.Do(t, http.MethodPost, base+"runs", body, headers)
	if created.Code != http.StatusAccepted {
		t.Fatalf("submit: %d %s", created.Code, created.Body)
	}
	var run ci.Run
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != ci.StatusBlocked || run.BlockedReason != ci.ComputeUnavailable {
		t.Fatalf("run escaped compute gate: %+v", run)
	}
	replay := e.Do(t, http.MethodPost, base+"runs", body, headers)
	if replay.Code != http.StatusAccepted || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("retry lost receipt: %d %s", replay.Code, replay.Body)
	}
	if jobs, err := (*e.Store()).JobListByAccount(context.Background(), e.Acct().ID, 1, 0); err != nil || len(jobs) != 0 {
		t.Fatalf("CI allocated Jobs: %d %v", len(jobs), err)
	}
	cancelled := e.Do(t, http.MethodPost, base+"runs/"+run.ID+"/cancel", nil, nil)
	if cancelled.Code != http.StatusOK || !strings.Contains(cancelled.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel: %d %s", cancelled.Code, cancelled.Body)
	}
	(*e.S()).WithInternalCIConfig(internalCIConfig(e.Acct().ID, false))
	if got := e.Do(t, http.MethodGet, base+"runs/"+run.ID, nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("disabled CI leaked history: %d", got.Code)
	}
}

func TestInternalCIRequiresAdminScopeAndHidesUnlistedAccounts(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithInternalCIConfig(internalCIConfig(e.Acct().ID, true))
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(context.Background(), e.Acct().ID, hash, "ci-read-only", []string{"apps:read"}); err != nil {
		t.Fatal(err)
	}
	if got := e.DoAs(t, http.MethodGet, "/v1/internal/ci/capabilities", nil, nil, plaintext); got.Code != http.StatusForbidden {
		t.Fatalf("read key reached internal service: %d %s", got.Code, got.Body)
	}
	(*e.S()).WithInternalCIConfig(internalCIConfig("a2b5b77c-c9cb-4f75-a7f4-c14f7d54be25", true))
	if got := e.Do(t, http.MethodGet, "/v1/internal/ci/capabilities", nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("unlisted admin reached internal service: %d", got.Code)
	}
}

func TestInternalCIGitHubIsSeparatelyGated(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	base := "/v1/internal/ci/github/bindings"
	(*e.S()).WithInternalCIConfig(internalCIConfig(e.Acct().ID, true))
	if got := e.Do(t, http.MethodGet, base, nil, nil); got.Code != http.StatusNotFound {
		t.Fatal("metadata flag enabled GitHub bindings")
	}
	if got := e.Do(t, http.MethodPut, base+"/checks", map[string]any{}, nil); got.Code != http.StatusNotFound {
		t.Fatal("disabled binding mutation accepted")
	}
	cfg := ci.ConfigFromEnv(func(key string) string {
		switch key {
		case ci.InternalEnv, ci.GitHubEnv:
			return "1"
		case ci.AccountsEnv:
			return e.Acct().ID
		}
		return ""
	})
	(*e.S()).WithInternalCIConfig(cfg)
	if got := e.Do(t, http.MethodGet, base, nil, nil); got.Code != http.StatusOK {
		t.Fatalf("binding read unavailable: %d %s", got.Code, got.Body)
	}
	if got := e.Do(t, http.MethodPut, base+"/checks", map[string]any{}, nil); got.Code != http.StatusBadRequest {
		t.Fatal("invalid binding accepted")
	}
}
