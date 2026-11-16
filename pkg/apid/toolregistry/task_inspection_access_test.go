package toolregistry_test

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPTaskInspectionAuthorityMemory(t *testing.T) {
	exerciseTaskInspectionAuthority(t, state.NewMemStore())
}

func TestMCPTaskInspectionAuthorityPostgres(t *testing.T) {
	exerciseTaskInspectionAuthority(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func exerciseTaskInspectionAuthority(t *testing.T, store state.Store) {
	t.Helper()
	for _, scenario := range []string{"original", "another_key", "another_account", "project_limited", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			f := newTaskRestoreFixture(t, store)
			account, principal := f.account, f.caller
			expected := http.StatusNotFound
			switch scenario {
			case "original":
				expected = http.StatusOK
			case "another_key", "another_account":
				var err error
				if scenario == "another_account" {
					account, err = store.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
					if err != nil {
						t.Fatal(err)
					}
				}
				sum := sha256.Sum256([]byte(uuid.NewString()))
				principal, err = store.CreateAPIKey(t.Context(), account.ID, sum[:], "inspection peer", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
				if err != nil {
					t.Fatal(err)
				}
			case "project_limited":
				if err := store.(state.KeyProjectLimitStore).SetAPIKeyProjectLimit(t.Context(), f.caller.ID, []string{uuid.NewString()}); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if _, err := store.MarkAPIKeyRevoked(t.Context(), f.account.ID, f.caller.ID); err != nil {
					t.Fatal(err)
				}
			}
			journal := store.(state.TaskToolReleaseIntentStore)
			before, err := journal.GetTaskToolReleaseIntent(t.Context(), f.account.ID, f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			// The principal intentionally predates revocation/project changes.
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/task-tool-release-intents/"+f.record.ID, nil)
			req.SetPathValue("id", f.record.ID)
			req = req.WithContext(middleware.WithPrincipal(req.Context(), account, &principal, nil))
			response := httptest.NewRecorder()
			toolregistry.NewPrivateExecutor(store, toolregistry.PrivateExecutionOptions{}).TaskReleaseIntent()(response, req, account)
			if response.Code != expected {
				t.Fatalf("native inspection %s = %d, want %d: %s", scenario, response.Code, expected, response.Body.String())
			}
			after, err := journal.GetTaskToolReleaseIntent(t.Context(), f.account.ID, f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			first, _ := json.Marshal(before)
			last, _ := json.Marshal(after)
			if string(first) != string(last) {
				t.Fatal("inspection changed the retained operation")
			}
			if expected != http.StatusOK && (json.Valid(response.Body.Bytes()) && len(response.Body.Bytes()) > 0) {
				var body map[string]any
				_ = json.Unmarshal(response.Body.Bytes(), &body)
				if _, found := body["execution"]; found {
					t.Fatal("denied requester received execution metadata")
				}
			}
		})
	}
}
